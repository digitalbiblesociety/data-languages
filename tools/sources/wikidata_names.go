// wikidata_names sets multilingual name fields from Wikidata's rdfs:label.
//
// Currently populates:
//
//   - name_zh — Simplified Chinese, preferring zh-Hans > zh-CN > zh-SG > zh
//
// Adding more languages later: append an entry to `nameTargets` below. The
// SPARQL filter and the per-row assignment loop pick it up automatically.
//
// Pipeline:
//
//	1. One chunked Wikidata SPARQL query gathers all candidate rdfs:label
//	   bindings whose xml:lang is in any target's priority list. Cached
//	   monthly as .cache/wikidata-names-<hash>-<YYYY-MM>.json.
//	2. For each ISO with at least one match, the highest-priority label per
//	   target field wins. Ties resolve in priority order (earlier wins).
//	3. Apply via corpus.SetScalars in Overwrite mode (Wikidata is canonical).
//
// Honors --only iso[,iso,...] to scope a test run.
package sources

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"languages/tools/corpus"
)

func init() { Register(wikidataNames{}) }

type wikidataNames struct{}

func (wikidataNames) Name() string { return "wikidata_names" }

// nameTargets maps a frontmatter field to the xml:lang tags (in priority
// order, earliest wins) that should populate it. Same target may appear
// multiple times for related variants.
type nameTarget struct {
	Field    string   // frontmatter key, e.g. "name_zh"
	LangTags []string // xml:lang values, highest priority first
}

var nameTargets = []nameTarget{
	{Field: "name_zh", LangTags: []string{"zh-hans", "zh-cn", "zh-sg", "zh"}},
}

// All target xml:lang values, used to build the SPARQL filter.
func allTargetLangs() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range nameTargets {
		for _, l := range t.LangTags {
			if !seen[l] {
				seen[l] = true
				out = append(out, l)
			}
		}
	}
	return out
}

// langTarget maps each xml:lang to (field, priority). Earlier-listed tags
// in nameTargets[i].LangTags have lower (better) priority numbers.
type langBinding struct {
	Field    string
	Priority int
}

func langBindings() map[string]langBinding {
	m := map[string]langBinding{}
	for _, t := range nameTargets {
		for i, l := range t.LangTags {
			// First-defined target wins if multiple targets list the same lang.
			if _, dup := m[l]; dup {
				continue
			}
			m[l] = langBinding{Field: t.Field, Priority: i}
		}
	}
	return m
}

func (wikidataNames) Run(opts Options) error {
	codes, err := corpus.ListCodes(opts.Dir)
	if err != nil {
		return err
	}
	sort.Strings(codes)

	if opts.Only != "" {
		want := map[string]bool{}
		for _, c := range strings.Split(opts.Only, ",") {
			if c = strings.TrimSpace(c); c != "" {
				want[c] = true
			}
		}
		filtered := codes[:0]
		for _, c := range codes {
			if want[c] {
				filtered = append(filtered, c)
			}
		}
		codes = filtered
		fmt.Printf("scope: --only restricted to %d code(s)\n", len(codes))
	} else {
		fmt.Printf("scope: full corpus, %d codes\n", len(codes))
	}
	if len(codes) == 0 {
		return nil
	}

	mapping, err := fetchNameMapping(codes, opts.Force)
	if err != nil {
		return err
	}

	updates := map[string]map[string]string{} // iso → {field: best label}
	for iso, byField := range mapping {
		updates[iso] = byField
	}
	fmt.Printf("wikidata: resolved labels for %d ISO(s)\n", len(updates))

	updated, unchanged, missing := 0, 0, 0
	perField := map[string]int{}
	for _, iso := range codes {
		byField, ok := updates[iso]
		if !ok || len(byField) == 0 {
			missing++
			continue
		}
		path := filepath.Join(opts.Dir, iso+".md")
		ops := make([]corpus.ScalarOp, 0, len(byField))
		for field, value := range byField {
			ops = append(ops, corpus.ScalarOp{Key: field, Value: value, Mode: corpus.Overwrite})
		}
		sort.Slice(ops, func(i, j int) bool { return ops[i].Key < ops[j].Key })
		r, err := corpus.SetScalars(path, ops, opts.DryRun)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if r.Changed {
			updated++
		} else {
			unchanged++
		}
		for _, k := range r.Inserted {
			perField[k]++
		}
		for _, k := range r.Replaced {
			perField[k]++
		}
	}

	fmt.Printf("updated: %d  unchanged: %d  no-label: %d\n", updated, unchanged, missing)
	for _, t := range nameTargets {
		fmt.Printf("  %s set: %d\n", t.Field, perField[t.Field])
	}
	if opts.DryRun {
		fmt.Println("(dry run — no files written)")
	}
	return nil
}

// fetchNameMapping returns a per-ISO map of frontmatter-field → best-priority
// label string. Cached at a single file keyed by (codes hash, year-month).
func fetchNameMapping(codes []string, force bool) (map[string]map[string]string, error) {
	hash := codesHash(codes)
	cacheFile := filepath.Join(".cache", fmt.Sprintf("wikidata-names-%s-%s.json", hash, yearMonth(time.Now().UTC())))
	if !force {
		if data, err := os.ReadFile(cacheFile); err == nil {
			var out map[string]map[string]string
			if json.Unmarshal(data, &out) == nil {
				fmt.Printf("  mapping: %s (cached)\n", cacheFile)
				return out, nil
			}
		}
	}

	out, err := runNameSPARQLChunks(codes)
	if err != nil {
		return nil, err
	}
	if data, err := json.Marshal(out); err == nil {
		_ = os.MkdirAll(".cache", 0o755)
		if err := os.WriteFile(cacheFile, data, 0o644); err == nil {
			fmt.Printf("  mapping: %s (wrote %d)\n", cacheFile, len(out))
		}
	}
	return out, nil
}

func runNameSPARQLChunks(codes []string) (map[string]map[string]string, error) {
	bindings := langBindings()
	langList := allTargetLangs()
	// best[iso][field] = current best (lowest-priority-number) label
	best := map[string]map[string]string{}
	bestPri := map[string]map[string]int{}

	for i := 0; i < len(codes); i += sparqlChunkSize {
		end := i + sparqlChunkSize
		if end > len(codes) {
			end = len(codes)
		}
		chunk := codes[i:end]
		fmt.Printf("  SPARQL chunk %d-%d (%d codes)...\n", i, end, len(chunk))

		isoValues := make([]string, len(chunk))
		for j, c := range chunk {
			isoValues[j] = `"` + c + `"`
		}
		langValues := make([]string, len(langList))
		for j, l := range langList {
			langValues[j] = `"` + l + `"`
		}
		query := fmt.Sprintf(`SELECT ?iso ?label WHERE {
  VALUES ?iso { %s }
  ?lang wdt:P220 ?iso .
  ?lang rdfs:label ?label .
  FILTER(LANG(?label) IN (%s))
}`, strings.Join(isoValues, " "), strings.Join(langValues, ", "))

		q := url.Values{}
		q.Set("query", query)
		q.Set("format", "json")
		body, err := sparqlGET(wikidataSPARQL + "?" + q.Encode())
		if err != nil {
			return nil, fmt.Errorf("chunk %d-%d: %w", i, end, err)
		}

		var resp sparqlResp
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("parse chunk %d-%d: %w", i, end, err)
		}
		for _, b := range resp.Results.Bindings {
			iso := b["iso"].Value
			label := b["label"].Value
			lang := b["label"].XMLLang
			if iso == "" || label == "" || lang == "" {
				continue
			}
			lang = strings.ToLower(lang)
			bind, ok := bindings[lang]
			if !ok {
				continue
			}
			fb, fok := best[iso]
			if !fok {
				fb = map[string]string{}
				best[iso] = fb
				bestPri[iso] = map[string]int{}
			}
			pri := bestPri[iso]
			if cur, exists := pri[bind.Field]; exists && bind.Priority >= cur {
				continue
			}
			fb[bind.Field] = label
			pri[bind.Field] = bind.Priority
		}
		time.Sleep(250 * time.Millisecond)
	}
	return best, nil
}

// wikidata_names populates the `translations[]` array on each language file
// from Wikidata's `rdfs:label` in target languages.
//
// Each translation item shape: {translation_iso, name, auto?}. Curated
// (Wikidata, CLDR) entries omit `auto`. Future LLM/MT-sourced entries set
// `auto: true`.
//
// Target languages, with priority order for each Wikidata xml:lang tag:
//
//	zho  ← zh-hans > zh-cn > zh-sg > zh
//	jpn  ← ja
//	hin  ← hi
//	kor  ← ko
//	ara  ← ar
//
// Adding more languages: append to `translationTargets`. The SPARQL filter
// and per-row binding logic pick the rest up automatically.
//
// Pipeline:
//
//	1. One chunked Wikidata SPARQL query gathers all candidate rdfs:label
//	   bindings whose xml:lang is in any target's priority list. Cached
//	   monthly as .cache/wikidata-translations-<hash>-<YYYY-MM>.json.
//	2. For each ISO with at least one match, the highest-priority label per
//	   target wins. Each becomes one translation item.
//	3. corpus.MergeFile dedups against existing translations (key:
//	   translation_iso) and appends new entries. Existing items keep their
//	   curated data — incoming values fill only empty/missing fields.
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

// translationTarget maps a target translation_iso (ISO 639-3) to the
// Wikidata xml:lang tags (in priority order, earliest wins) that should
// populate it.
type translationTarget struct {
	Iso  string   // ISO 639-3 of the target language, used as translation_iso
	Tags []string // xml:lang values, highest priority first
}

var translationTargets = []translationTarget{
	{Iso: "zho", Tags: []string{"zh-hans", "zh-cn", "zh-sg", "zh"}},
	{Iso: "jpn", Tags: []string{"ja"}},
	{Iso: "hin", Tags: []string{"hi"}},
	{Iso: "kor", Tags: []string{"ko"}},
	{Iso: "ara", Tags: []string{"ar"}},
}

// translationItemOrder is the canonical key order inside each translations[]
// item. Used by corpus.MergeFile when rendering.
var translationItemOrder = []string{"translation_iso", "name", "auto"}

func allWikidataLangTags() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range translationTargets {
		for _, l := range t.Tags {
			if !seen[l] {
				seen[l] = true
				out = append(out, l)
			}
		}
	}
	return out
}

type tagBinding struct {
	Iso      string // translation_iso (target)
	Priority int    // lower wins
}

func wikidataTagBindings() map[string]tagBinding {
	m := map[string]tagBinding{}
	for _, t := range translationTargets {
		for i, l := range t.Tags {
			if _, dup := m[l]; dup {
				continue
			}
			m[l] = tagBinding{Iso: t.Iso, Priority: i}
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

	mapping, err := fetchTranslationMapping(codes, opts.Force)
	if err != nil {
		return err
	}
	fmt.Printf("wikidata: resolved labels for %d ISO(s)\n", len(mapping))

	updated, unchanged, skipped := 0, 0, 0
	perTarget := map[string]int{}
	for _, iso := range codes {
		byTarget, ok := mapping[iso]
		if !ok || len(byTarget) == 0 {
			skipped++
			continue
		}
		path := filepath.Join(opts.Dir, iso+".md")
		items := make([]*corpus.Item, 0, len(byTarget))
		// Deterministic order by translation_iso for readable diffs.
		targetIsos := make([]string, 0, len(byTarget))
		for t := range byTarget {
			targetIsos = append(targetIsos, t)
		}
		sort.Strings(targetIsos)
		for _, t := range targetIsos {
			it := corpus.NewItem()
			it.Set("translation_iso", t)
			it.Set("name", byTarget[t])
			items = append(items, it)
			perTarget[t]++
		}
		r, err := corpus.MergeFile(path, "translations", items, corpus.MergeOptions{
			DedupKey:   "translation_iso",
			FieldOrder: translationItemOrder,
			SortByKey:  true,
			DryRun:     opts.DryRun,
		})
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if r.Changed {
			updated++
		} else {
			unchanged++
		}
	}

	fmt.Printf("updated: %d  unchanged: %d  no-label: %d\n", updated, unchanged, skipped)
	for _, t := range translationTargets {
		fmt.Printf("  %s: %d\n", t.Iso, perTarget[t.Iso])
	}
	if opts.DryRun {
		fmt.Println("(dry run — no files written)")
	}
	return nil
}

// fetchTranslationMapping returns, per ISO, a map of target translation_iso
// → best-priority label.
func fetchTranslationMapping(codes []string, force bool) (map[string]map[string]string, error) {
	hash := codesHash(codes)
	cacheFile := filepath.Join(".cache", fmt.Sprintf("wikidata-translations-%s-%s.json", hash, yearMonth(time.Now().UTC())))
	if !force {
		if data, err := os.ReadFile(cacheFile); err == nil {
			var out map[string]map[string]string
			if json.Unmarshal(data, &out) == nil {
				fmt.Printf("  mapping: %s (cached)\n", cacheFile)
				return out, nil
			}
		}
	}
	out, err := runTranslationSPARQLChunks(codes)
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

func runTranslationSPARQLChunks(codes []string) (map[string]map[string]string, error) {
	bindings := wikidataTagBindings()
	langs := allWikidataLangTags()
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
		langValues := make([]string, len(langs))
		for j, l := range langs {
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
			lang := strings.ToLower(b["label"].XMLLang)
			if iso == "" || label == "" || lang == "" {
				continue
			}
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
			if cur, exists := pri[bind.Iso]; exists && bind.Priority >= cur {
				continue
			}
			fb[bind.Iso] = label
			pri[bind.Iso] = bind.Priority
		}
		time.Sleep(250 * time.Millisecond)
	}
	return best, nil
}

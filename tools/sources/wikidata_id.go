// wikidata_id sets each language file's `wikidata_id` — the Wikidata entity
// ID (QID, e.g. Q27811) whose P220 (ISO 639-3 code) claim matches the file's
// ISO. Stored once, it turns every future Wikidata-based enrichment into a
// direct entity lookup instead of a SPARQL re-match.
//
// One chunked SPARQL query covers the corpus; the iso → QID mapping is
// cached monthly as .cache/wikidata-ids-<hash>-<YYYY-MM>.json. A few ISO
// codes are claimed by more than one entity (Wikidata duplicates); the
// numerically lowest QID wins so re-runs stay deterministic.
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
	"strconv"
	"strings"
	"time"

	"languages/tools/corpus"
)

func init() { Register(wikidataID{}) }

type wikidataID struct{}

func (wikidataID) Name() string { return "wikidata_id" }

const wikidataEntityPrefix = "http://www.wikidata.org/entity/"

func (wikidataID) Run(opts Options) error {
	codes, err := corpus.ListCodes(opts.Dir)
	if err != nil {
		return err
	}
	sort.Strings(codes)
	codes = onlyFilter(codes, opts.Only)
	if len(codes) == 0 {
		return nil
	}

	mapping, err := fetchWikidataIDs(codes, opts.Force)
	if err != nil {
		return err
	}
	fmt.Printf("wikidata: QIDs for %d ISO(s)\n", len(mapping))

	updated, unchanged, missing := 0, 0, 0
	for _, iso := range codes {
		qid, ok := mapping[iso]
		if !ok {
			missing++
			continue
		}
		path := filepath.Join(opts.Dir, iso+".md")
		r, err := corpus.SetScalars(path, []corpus.ScalarOp{
			{Key: "wikidata_id", Value: qid, Mode: corpus.Overwrite},
		}, opts.DryRun)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if r.Changed {
			updated++
		} else {
			unchanged++
		}
	}

	fmt.Printf("updated: %d  unchanged: %d  no-entity: %d\n", updated, unchanged, missing)
	if opts.DryRun {
		fmt.Println("(dry run — no files written)")
	}
	return nil
}

// fetchWikidataIDs returns the iso → QID mapping, from the monthly cache
// when possible.
func fetchWikidataIDs(codes []string, force bool) (map[string]string, error) {
	hash := codesHash(codes)
	cacheFile := filepath.Join(".cache", fmt.Sprintf("wikidata-ids-%s-%s.json", hash, yearMonth(time.Now().UTC())))
	if !force {
		if data, err := os.ReadFile(cacheFile); err == nil {
			var out map[string]string
			if json.Unmarshal(data, &out) == nil {
				fmt.Printf("  mapping: %s (cached)\n", cacheFile)
				return out, nil
			}
		}
	}
	out, err := runWikidataIDChunks(codes)
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

func runWikidataIDChunks(codes []string) (map[string]string, error) {
	best := map[string]int{} // iso → numeric QID, lowest wins
	for i := 0; i < len(codes); i += sparqlChunkSize {
		end := i + sparqlChunkSize
		if end > len(codes) {
			end = len(codes)
		}
		chunk := codes[i:end]
		fmt.Printf("  SPARQL chunk %d-%d (%d codes)...\n", i, end, len(chunk))

		values := make([]string, len(chunk))
		for j, c := range chunk {
			values[j] = `"` + c + `"`
		}
		query := fmt.Sprintf(`SELECT ?iso ?lang WHERE {
  VALUES ?iso { %s }
  ?lang wdt:P220 ?iso .
}`, strings.Join(values, " "))

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
			n, ok := parseQID(b["lang"].Value)
			if iso == "" || !ok {
				continue
			}
			if cur, exists := best[iso]; !exists || n < cur {
				best[iso] = n
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	out := make(map[string]string, len(best))
	for iso, n := range best {
		out[iso] = "Q" + strconv.Itoa(n)
	}
	return out, nil
}

// parseQID extracts the numeric part of a Wikidata entity URI
// (http://www.wikidata.org/entity/Q27811 → 27811).
func parseQID(uri string) (int, bool) {
	s := strings.TrimPrefix(uri, wikidataEntityPrefix)
	if !strings.HasPrefix(s, "Q") {
		return 0, false
	}
	n, err := strconv.Atoi(s[1:])
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

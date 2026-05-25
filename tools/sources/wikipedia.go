// wikipedia enriches each language file with English Wikipedia data:
//
//   - wikipedia_url   (frontmatter, always set)
//   - autonym         (frontmatter, IfMissing — from Wikidata P1705 or {{Infobox language}} nativename)
//   - population      (frontmatter, IfMissing — from Wikidata P1098 or {{Infobox language}} speakers)
//   - body            (markdown body, IfEmpty — REST API summary extract)
//
// Pipeline:
//
//	1. One Wikidata SPARQL query (chunked across all ISOs) → mapping of
//	   {iso → article_url, nativeLabel?, speakers?}. Cached monthly.
//	2. For each ISO with a mapping AND empty body / missing autonym /
//	   missing population, fetch the per-article assets:
//	     - REST summary (extract → body)
//	     - MediaWiki action API (wikitext → infobox)
//	   Cached per article, monthly.
//	3. Apply scalar updates via corpus.SetScalars and body via corpus.SetBody.
//
// Pass --only iso[,iso,...] (via -only on the dispatcher) to restrict.
package sources

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"languages/tools/corpus"
)

func init() { Register(wikipedia{}) }

type wikipedia struct{}

func (wikipedia) Name() string { return "wikipedia" }

const (
	wikipediaUA           = "languages-tools/1.0 (https://github.com/dbs/languages; jon@dbs.org)"
	wikidataSPARQL        = "https://query.wikidata.org/sparql"
	wikipediaSummaryAPI   = "https://en.wikipedia.org/api/rest_v1/page/summary/"
	wikipediaActionAPI    = "https://en.wikipedia.org/w/api.php"
	sparqlChunkSize       = 500
	perArticleSleep       = 100 * time.Millisecond
)

type wdEntry struct {
	Article     string
	NativeLabel string
	Speakers    int64
}

type sparqlResp struct {
	Results struct {
		Bindings []map[string]struct {
			Value string `json:"value"`
		} `json:"bindings"`
	} `json:"results"`
}

type restSummary struct {
	Title   string `json:"title"`
	Extract string `json:"extract"`
}

type actionParseResp struct {
	Parse struct {
		Wikitext struct {
			Text string `json:"*"`
		} `json:"wikitext"`
	} `json:"parse"`
	Error *struct {
		Info string `json:"info"`
	} `json:"error,omitempty"`
}

func (wikipedia) Run(opts Options) error {
	codes, err := corpus.ListCodes(opts.Dir)
	if err != nil {
		return err
	}
	sort.Strings(codes)
	if opts.Only != "" {
		want := map[string]bool{}
		for _, c := range strings.Split(opts.Only, ",") {
			c = strings.TrimSpace(c)
			if c != "" {
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

	mapping, err := fetchWikidataMapping(codes, opts.Force)
	if err != nil {
		return err
	}
	fmt.Printf("wikidata mapping: %d ISOs resolved\n", len(mapping))

	updated, unchanged, mapped := 0, 0, 0
	bodyAdds, autonymAdds, popAdds, urlAdds := 0, 0, 0, 0

	for _, iso := range codes {
		path := filepath.Join(opts.Dir, iso+".md")
		wd, has := mapping[iso]
		if !has || wd.Article == "" {
			continue
		}
		mapped++

		// Decide which per-article fetches we need based on the file's current state.
		needBody, needAutonym, needPopulation, err := decideNeeds(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}

		// Always set wikipedia_url (it's source-of-truth provenance).
		ops := []corpus.ScalarOp{
			{Key: "wikipedia_url", Value: wd.Article, Mode: corpus.Overwrite},
		}

		var autonym string
		var population int64

		// Cheap wins from Wikidata before hitting Wikipedia.
		if needAutonym && wd.NativeLabel != "" {
			autonym = wd.NativeLabel
		}
		if needPopulation && wd.Speakers > 0 {
			population = wd.Speakers
		}

		var summary string

		// Hit Wikipedia only when we need something it can supply.
		fetchSummary := needBody
		fetchWikitext := (needAutonym && autonym == "") || (needPopulation && population == 0)

		if fetchSummary || fetchWikitext {
			title := titleFromArticleURL(wd.Article)
			if fetchSummary {
				if s, err := fetchPageSummary(title, opts.Force); err != nil {
					return err
				} else if s != "" {
					summary = s
				}
			}
			if fetchWikitext {
				wt, err := fetchPageWikitext(title, opts.Force)
				if err != nil {
					return err
				}
				if wt != "" {
					fields := parseLanguageInfobox(wt)
					if needAutonym && autonym == "" {
						if v := pickFirstLine(extractField(fields, "nativename", "native_name", "altname")); v != "" {
							autonym = v
						}
					}
					if needPopulation && population == 0 {
						if v := parsePopulation(extractField(fields, "speakers", "speakers2")); v > 0 {
							population = v
						}
					}
				}
			}
			time.Sleep(perArticleSleep)
		}

		if autonym != "" {
			ops = append(ops, corpus.ScalarOp{Key: "autonym", Value: autonym, Mode: corpus.IfMissing})
		}
		if population > 0 {
			ops = append(ops, corpus.ScalarOp{Key: "population", Value: population, Mode: corpus.IfMissing})
		}

		r, err := corpus.SetScalars(path, ops, opts.DryRun)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}

		var bodyChanged bool
		if needBody && summary != "" {
			bodyChanged, err = corpus.SetBody(path, summary, corpus.BodyIfEmpty, opts.DryRun)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		}

		if r.Changed || bodyChanged {
			updated++
		} else {
			unchanged++
		}
		for _, k := range append(append([]string(nil), r.Inserted...), r.Replaced...) {
			switch k {
			case "wikipedia_url":
				urlAdds++
			case "autonym":
				autonymAdds++
			case "population":
				popAdds++
			}
		}
		if bodyChanged {
			bodyAdds++
		}
	}

	fmt.Printf("mapped (wikidata had article): %d\n", mapped)
	fmt.Printf("updated: %d  unchanged: %d\n", updated, unchanged)
	fmt.Printf("  wikipedia_url set: %d\n", urlAdds)
	fmt.Printf("  autonym filled:    %d\n", autonymAdds)
	fmt.Printf("  population filled: %d\n", popAdds)
	fmt.Printf("  body written:      %d\n", bodyAdds)
	if opts.DryRun {
		fmt.Println("(dry run — no files written)")
	}
	return nil
}

// decideNeeds inspects path's current state to decide which Wikipedia
// resources are worth fetching for this ISO.
func decideNeeds(path string) (needBody, needAutonym, needPop bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, false, false, err
	}
	block, body, err := corpus.Split(data)
	if err != nil {
		return false, false, false, err
	}
	needBody = strings.TrimSpace(body) == ""
	entries, err := corpus.ReadEntries(block)
	if err != nil {
		return false, false, false, err
	}
	hasNonNull := func(key string) bool {
		for _, e := range entries {
			if e.Key == key {
				v := corpus.Unquote(strings.TrimSpace(e.Value))
				return v != "" && v != "null" && v != "~"
			}
		}
		return false
	}
	needAutonym = !hasNonNull("autonym")
	needPop = !hasNonNull("population")
	return
}

// --- Wikidata SPARQL ------------------------------------------------------

func fetchWikidataMapping(codes []string, force bool) (map[string]wdEntry, error) {
	hash := codesHash(codes)
	cacheFile := filepath.Join(".cache", fmt.Sprintf("wikipedia-mapping-%s-%s.json", hash, yearMonth(time.Now().UTC())))
	if !force {
		if data, err := os.ReadFile(cacheFile); err == nil {
			var out map[string]wdEntry
			if json.Unmarshal(data, &out) == nil {
				fmt.Printf("  mapping: %s (cached)\n", cacheFile)
				return out, nil
			}
		}
	}
	out, err := runSPARQLChunks(codes)
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

// codesHash returns a short stable fingerprint of the input codes so we can
// detect when the input set changes and avoid stale caches.
func codesHash(codes []string) string {
	h := uint64(1469598103934665603)
	for _, c := range codes {
		for i := 0; i < len(c); i++ {
			h ^= uint64(c[i])
			h *= 1099511628211
		}
		h ^= '|'
		h *= 1099511628211
	}
	return strconv.FormatUint(h, 16)
}

func yearMonth(t time.Time) string {
	return fmt.Sprintf("%04d-%02d", t.Year(), int(t.Month()))
}

func runSPARQLChunks(codes []string) (map[string]wdEntry, error) {
	out := map[string]wdEntry{}
	for i := 0; i < len(codes); i += sparqlChunkSize {
		end := i + sparqlChunkSize
		if end > len(codes) {
			end = len(codes)
		}
		chunk := codes[i:end]
		fmt.Printf("  SPARQL chunk %d-%d (%d codes)...\n", i, end, len(chunk))
		entries, err := runSPARQL(chunk)
		if err != nil {
			return nil, fmt.Errorf("sparql chunk %d-%d: %w", i, end, err)
		}
		for iso, e := range entries {
			out[iso] = mergeWDEntry(out[iso], e)
		}
		time.Sleep(250 * time.Millisecond)
	}
	return out, nil
}

func mergeWDEntry(existing, incoming wdEntry) wdEntry {
	if existing.Article == "" {
		existing.Article = incoming.Article
	}
	if existing.NativeLabel == "" {
		existing.NativeLabel = incoming.NativeLabel
	}
	if incoming.Speakers > existing.Speakers {
		existing.Speakers = incoming.Speakers
	}
	return existing
}

func runSPARQL(codes []string) (map[string]wdEntry, error) {
	values := make([]string, len(codes))
	for i, c := range codes {
		values[i] = `"` + c + `"`
	}
	query := fmt.Sprintf(`SELECT ?iso ?nativeLabel ?speakers ?article WHERE {
  VALUES ?iso { %s }
  ?lang wdt:P220 ?iso .
  OPTIONAL { ?lang wdt:P1705 ?nativeLabel }
  OPTIONAL { ?lang wdt:P1098 ?speakers }
  OPTIONAL { ?article schema:about ?lang ; schema:isPartOf <https://en.wikipedia.org/> . }
}`, strings.Join(values, " "))

	q := url.Values{}
	q.Set("query", query)
	q.Set("format", "json")
	body, err := sparqlGET(wikidataSPARQL + "?" + q.Encode())
	if err != nil {
		return nil, err
	}
	var resp sparqlResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse sparql: %w", err)
	}

	out := map[string]wdEntry{}
	for _, b := range resp.Results.Bindings {
		iso := b["iso"].Value
		if iso == "" {
			continue
		}
		entry := out[iso]
		if a := b["article"].Value; a != "" && entry.Article == "" {
			entry.Article = a
		}
		if n := b["nativeLabel"].Value; n != "" && entry.NativeLabel == "" {
			entry.NativeLabel = n
		}
		if s := b["speakers"].Value; s != "" {
			if n, err := strconv.ParseFloat(s, 64); err == nil {
				if int64(n) > entry.Speakers {
					entry.Speakers = int64(n)
				}
			}
		}
		out[iso] = entry
	}
	return out, nil
}

// --- Per-article HTTP -----------------------------------------------------

func fetchPageSummary(title string, force bool) (string, error) {
	res, err := corpus.FetchCached(corpus.FetchOptions{
		Name:     safeTitle(title),
		URL:      wikipediaSummaryAPI + url.PathEscape(title),
		Force:    force,
		Ext:      "summary.json",
		CacheDir: ".cache/wikipedia",
		Headers:  map[string]string{"User-Agent": wikipediaUA, "Accept": "application/json"},
	})
	if err != nil {
		return "", nil // tolerate missing pages — many ISOs lack one
	}
	var s restSummary
	if err := json.Unmarshal(res.Body, &s); err != nil {
		return "", nil
	}
	return strings.TrimSpace(s.Extract), nil
}

func fetchPageWikitext(title string, force bool) (string, error) {
	q := url.Values{}
	q.Set("action", "parse")
	q.Set("page", title)
	q.Set("format", "json")
	q.Set("prop", "wikitext")
	q.Set("redirects", "1")
	res, err := corpus.FetchCached(corpus.FetchOptions{
		Name:     safeTitle(title),
		URL:      wikipediaActionAPI + "?" + q.Encode(),
		Force:    force,
		Ext:      "wikitext.json",
		CacheDir: ".cache/wikipedia",
		Headers:  map[string]string{"User-Agent": wikipediaUA, "Accept": "application/json"},
	})
	if err != nil {
		return "", nil
	}
	var r actionParseResp
	if err := json.Unmarshal(res.Body, &r); err != nil {
		return "", nil
	}
	if r.Error != nil {
		return "", nil
	}
	return r.Parse.Wikitext.Text, nil
}

var reSafeName = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

func safeTitle(title string) string {
	return strings.Trim(reSafeName.ReplaceAllString(title, "_"), "_")
}

func titleFromArticleURL(article string) string {
	const prefix = "https://en.wikipedia.org/wiki/"
	if !strings.HasPrefix(article, prefix) {
		return ""
	}
	t := strings.TrimPrefix(article, prefix)
	if decoded, err := url.PathUnescape(t); err == nil {
		t = decoded
	}
	return strings.ReplaceAll(t, "_", " ")
}

// --- Wikitext infobox parsing --------------------------------------------

// parseLanguageInfobox finds {{Infobox language}} (and /genetic variant) in
// wikitext, and returns a map of its top-level fields.
func parseLanguageInfobox(wikitext string) map[string]string {
	re := regexp.MustCompile(`(?i)\{\{\s*Infobox language(?:/genetic)?\b`)
	for _, loc := range re.FindAllStringIndex(wikitext, -1) {
		start := loc[1]
		depth := 1
		end := -1
		for i := start; i < len(wikitext)-1; i++ {
			c, n := wikitext[i], wikitext[i+1]
			if c == '{' && n == '{' {
				depth++
				i++
			} else if c == '}' && n == '}' {
				depth--
				if depth == 0 {
					end = i
					break
				}
				i++
			}
		}
		if end == -1 {
			continue
		}
		return splitInfoboxFields(wikitext[start:end])
	}
	return nil
}

// splitInfoboxFields walks the body of an infobox once and splits it into
// top-level key=value pairs, respecting nested templates and links.
func splitInfoboxFields(body string) map[string]string {
	fields := map[string]string{}
	depth, bracket := 0, 0
	var key string
	var current strings.Builder
	flush := func() {
		if key != "" {
			fields[strings.TrimSpace(key)] = strings.TrimSpace(current.String())
		}
		key = ""
		current.Reset()
	}
	for i := 0; i < len(body); i++ {
		c := body[i]
		n := byte(0)
		if i+1 < len(body) {
			n = body[i+1]
		}
		switch {
		case c == '{' && n == '{':
			depth++
			current.WriteByte(c)
			current.WriteByte(n)
			i++
		case c == '}' && n == '}':
			depth--
			current.WriteByte(c)
			current.WriteByte(n)
			i++
		case c == '[' && n == '[':
			bracket++
			current.WriteByte(c)
			current.WriteByte(n)
			i++
		case c == ']' && n == ']':
			bracket--
			current.WriteByte(c)
			current.WriteByte(n)
			i++
		case c == '|' && depth == 0 && bracket == 0:
			flush()
		case c == '=' && depth == 0 && bracket == 0 && key == "":
			key = current.String()
			current.Reset()
		default:
			current.WriteByte(c)
		}
	}
	flush()
	return fields
}

func extractField(fields map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := fields[k]; ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

var (
	reRefTag     = regexp.MustCompile(`(?s)<ref[^>]*>.*?</ref>`)
	reRefSelf    = regexp.MustCompile(`(?i)<ref[^>]*/>`)
	reHTMLCmt    = regexp.MustCompile(`(?s)<!--.*?-->`)
	reLangTpl    = regexp.MustCompile(`(?i)\{\{lang\s*\|\s*[^|}]+\|\s*([^}]+)\}\}`)
	reNoBold     = regexp.MustCompile(`(?i)\{\{nobold\s*\|\s*([^}]+)\}\}`)
	reNoWrap     = regexp.MustCompile(`(?i)\{\{nowrap\s*\|\s*([^}]+)\}\}`)
	reAnyTpl     = regexp.MustCompile(`\{\{[^{}]+\}\}`)
	reWikiLink   = regexp.MustCompile(`\[\[(?:[^|\]]+\|)?([^\]]+)\]\]`)
	reAnyHTMLTag = regexp.MustCompile(`</?[^>]+>`)
	reBoldQuotes = regexp.MustCompile(`'''?`)
	reWS         = regexp.MustCompile(`\s+`)
	reBR         = regexp.MustCompile(`(?i)<br\s*/?>`)
)

func cleanWikitext(s string) string {
	if s == "" {
		return ""
	}
	s = reRefTag.ReplaceAllString(s, "")
	s = reRefSelf.ReplaceAllString(s, "")
	s = reHTMLCmt.ReplaceAllString(s, "")
	s = reLangTpl.ReplaceAllString(s, "$1")
	s = reNoBold.ReplaceAllString(s, "$1")
	s = reNoWrap.ReplaceAllString(s, "$1")
	for {
		next := reAnyTpl.ReplaceAllString(s, "")
		if next == s {
			break
		}
		s = next
	}
	s = reWikiLink.ReplaceAllString(s, "$1")
	s = reAnyHTMLTag.ReplaceAllString(s, "")
	s = reBoldQuotes.ReplaceAllString(s, "")
	s = reWS.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func pickFirstLine(s string) string {
	if s == "" {
		return ""
	}
	s = reBR.ReplaceAllString(s, "\n")
	for _, part := range strings.Split(s, "\n") {
		if c := cleanWikitext(part); c != "" {
			return c
		}
	}
	return ""
}

var (
	reMillion = regexp.MustCompile(`(?i)([\d.]+)\s*million`)
	reThousand = regexp.MustCompile(`(?i)([\d.]+)\s*thousand`)
	reNumber  = regexp.MustCompile(`(\d{1,3}(?:,\d{3})+|\d{2,})`)
)

func parsePopulation(s string) int64 {
	c := cleanWikitext(s)
	if c == "" {
		return 0
	}
	if m := reMillion.FindStringSubmatch(c); len(m) > 0 {
		if f, err := strconv.ParseFloat(m[1], 64); err == nil {
			return int64(f * 1_000_000)
		}
	}
	if m := reThousand.FindStringSubmatch(c); len(m) > 0 {
		if f, err := strconv.ParseFloat(m[1], 64); err == nil {
			return int64(f * 1_000)
		}
	}
	if m := reNumber.FindStringSubmatch(c); len(m) > 0 {
		n, err := strconv.Atoi(strings.ReplaceAll(m[1], ",", ""))
		if err == nil {
			return int64(n)
		}
	}
	return 0
}

var sparqlClient = &http.Client{Timeout: 90 * time.Second}

// sparqlGET fetches a SPARQL endpoint. The per-chunk responses are not
// cached at the HTTP layer — caching happens in fetchWikidataMapping after
// the chunks have been merged.
func sparqlGET(u string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", wikipediaUA)
	req.Header.Set("Accept", "application/sparql-results+json")
	resp, err := sparqlClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GET sparql → %d: %s", resp.StatusCode, strings.TrimSpace(string(preview)))
	}
	return io.ReadAll(resp.Body)
}

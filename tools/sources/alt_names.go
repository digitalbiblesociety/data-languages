// alt_names builds the deduped `alt_names` array for each language from two
// upstream catalogs:
//
//   - Glottolog `names.csv` (provider-tagged: multitree, lexvo, elcat,
//     aiatsis, wals, glottolog, moseley & asher, ruhlen). Joined by
//     `glottocode` — files that don't have a glottocode get only the SIL set.
//   - SIL's ISO 639-3 Name Index (Print_Name + Inverted_Name). Joined by
//     ISO 639-3.
//
// Names are deduped case-insensitively (preserving first-seen casing) and
// sorted alphabetically. Each language's current `name` and `autonym` are
// excluded so alt_names contains only *other* names.
//
// Sources:
//
//	https://github.com/glottolog/glottolog-cldf
//	https://iso639-3.sil.org/code_tables/download_tables
package sources

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"languages/tools/corpus"
)

func init() { Register(altNames{}) }

type altNames struct{}

func (altNames) Name() string { return "alt_names" }

const (
	glottologNamesURL = "https://raw.githubusercontent.com/glottolog/glottolog-cldf/master/cldf/names.csv"
	silNameIndexURL   = "https://iso639-3.sil.org/sites/iso639-3/files/downloads/iso-639-3_Name_Index.tab"
)

func (altNames) Run(opts Options) error {
	byGlot, err := fetchGlottologNames(opts.Force)
	if err != nil {
		return err
	}
	bySIL, err := fetchSILNames(opts.Force)
	if err != nil {
		return err
	}
	fmt.Printf("glottolog: names for %d glottocodes\n", len(byGlot))
	fmt.Printf("sil:       names for %d ISO codes\n", len(bySIL))

	codes, err := corpus.ListCodes(opts.Dir)
	if err != nil {
		return err
	}
	sort.Strings(codes)

	updated, unchanged, empty := 0, 0, 0
	withGlot, withSIL, withBoth, withNone := 0, 0, 0, 0
	for _, iso := range codes {
		path := filepath.Join(opts.Dir, iso+".md")
		entries, err := corpus.ReadFile(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}

		var glotcode, name, autonym string
		for _, e := range entries {
			v := corpus.Unquote(strings.TrimSpace(e.Value))
			switch e.Key {
			case "glottocode":
				glotcode = v
			case "name":
				name = v
			case "autonym":
				autonym = v
			}
		}

		var pool []string
		hasGlot, hasSIL := false, false
		if glotcode != "" {
			if names, ok := byGlot[glotcode]; ok {
				pool = append(pool, names...)
				hasGlot = true
			}
		}
		if names, ok := bySIL[iso]; ok {
			pool = append(pool, names...)
			hasSIL = true
		}
		switch {
		case hasGlot && hasSIL:
			withBoth++
		case hasGlot:
			withGlot++
		case hasSIL:
			withSIL++
		default:
			withNone++
		}

		deduped := dedupAltNames(pool, name, autonym)
		if len(deduped) == 0 {
			empty++
			continue
		}

		r, err := corpus.SetScalars(path, []corpus.ScalarOp{
			{Key: "alt_names", Value: deduped, Mode: corpus.Overwrite},
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

	fmt.Printf("coverage: both=%d  glot=%d  sil=%d  none=%d\n", withBoth, withGlot, withSIL, withNone)
	fmt.Printf("updated: %d  unchanged: %d  empty-skip: %d\n", updated, unchanged, empty)
	if opts.DryRun {
		fmt.Println("(dry run — no files written)")
	}
	return nil
}

// dedupAltNames lower-cases for dedup but preserves the first-seen casing.
// Names whose case-folded form matches `name` or `autonym` are dropped, as
// are the "<name> language" / "<name> languages" article-title forms.
func dedupAltNames(in []string, name, autonym string) []string {
	exclude := map[string]bool{}
	for _, base := range []string{name, autonym} {
		k := altNameKey(base)
		if k == "" {
			continue
		}
		exclude[k] = true
		exclude[k+" language"] = true
		exclude[k+" languages"] = true
	}
	seen := map[string]string{} // key → first-seen original
	var keys []string
	for _, raw := range in {
		s := strings.TrimSpace(raw)
		if s == "" || strings.EqualFold(s, "not specified") {
			continue
		}
		k := altNameKey(s)
		if k == "" || exclude[k] {
			continue
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = s
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return seen[keys[i]] < seen[keys[j]] })
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, seen[k])
	}
	return out
}

func altNameKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	// Collapse internal whitespace so "English  language" and "English language" dedup.
	return strings.Join(strings.Fields(s), " ")
}

// fetchGlottologNames downloads glottolog-cldf/names.csv and returns a map
// of glottocode → list of unique names (in input order).
func fetchGlottologNames(force bool) (map[string][]string, error) {
	res, err := corpus.FetchCached(corpus.FetchOptions{
		Name:  "glottolog-names",
		URL:   glottologNamesURL,
		Force: force,
		Ext:   "csv",
	})
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	seen := map[string]map[string]bool{} // glot → name-key set
	r := csv.NewReader(bytes.NewReader(res.Body))
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read glottolog header: %w", err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[h] = i
	}
	for _, k := range []string{"Language_ID", "Name"} {
		if _, ok := col[k]; !ok {
			return nil, fmt.Errorf("glottolog names.csv missing column %q", k)
		}
	}
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		g := row[col["Language_ID"]]
		n := row[col["Name"]]
		if g == "" || n == "" {
			continue
		}
		key := altNameKey(n)
		if key == "" {
			continue
		}
		s, ok := seen[g]
		if !ok {
			s = map[string]bool{}
			seen[g] = s
		}
		if s[key] {
			continue
		}
		s[key] = true
		out[g] = append(out[g], n)
	}
	return out, nil
}

// fetchSILNames downloads iso-639-3_Name_Index.tab and returns a map of
// ISO 639-3 → list of unique names (Print_Name first, then Inverted_Name).
func fetchSILNames(force bool) (map[string][]string, error) {
	res, err := corpus.FetchCached(corpus.FetchOptions{
		Name:  "sil-name-index",
		URL:   silNameIndexURL,
		Force: force,
		Ext:   "tab",
	})
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	seen := map[string]map[string]bool{}
	lines := strings.Split(string(res.Body), "\n")
	if len(lines) < 2 {
		return nil, fmt.Errorf("sil tab: %d lines", len(lines))
	}
	for i, ln := range lines {
		if i == 0 || ln == "" {
			continue
		}
		f := strings.Split(ln, "\t")
		if len(f) < 3 {
			continue
		}
		iso, print, inverted := f[0], f[1], f[2]
		add := func(n string) {
			if n == "" {
				return
			}
			key := altNameKey(n)
			if key == "" {
				return
			}
			s, ok := seen[iso]
			if !ok {
				s = map[string]bool{}
				seen[iso] = s
			}
			if s[key] {
				return
			}
			s[key] = true
			out[iso] = append(out[iso], n)
		}
		add(print)
		add(inverted)
	}
	return out, nil
}

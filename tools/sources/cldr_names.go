// cldr_names fills missing `name_zh` entries from Unicode CLDR's zh locale
// data (zh/languages.json). Variant keys ("en-GB", "ars-alt-menu", etc.)
// are skipped — only base ISO 639-1/3 codes are considered, so we never
// assign a regional display name (e.g. "British English") to the canonical
// ISO code.
//
// Mode is IfMissing: CLDR fills only where `name_zh` is absent. Files
// already populated by `wikidata_names` are left untouched.
//
// Source: https://github.com/unicode-org/cldr-json
package sources

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"languages/tools/corpus"
)

func init() { Register(cldrNames{}) }

type cldrNames struct{}

func (cldrNames) Name() string { return "cldr_names" }

const cldrZhLanguagesURL = "https://raw.githubusercontent.com/unicode-org/cldr-json/main/cldr-json/cldr-localenames-full/main/zh/languages.json"

type cldrZhDoc struct {
	Main map[string]struct {
		LocaleDisplayNames struct {
			Languages map[string]string `json:"languages"`
		} `json:"localeDisplayNames"`
	} `json:"main"`
}

func (cldrNames) Run(opts Options) error {
	res, err := corpus.FetchCached(corpus.FetchOptions{
		Name:  "cldr-zh-languages",
		URL:   cldrZhLanguagesURL,
		Force: opts.Force,
		Ext:   "json",
	})
	if err != nil {
		return err
	}
	fmt.Printf("source: %s", res.Source)
	if res.Fresh {
		fmt.Println(" (fetched)")
	} else {
		fmt.Println(" (cached)")
	}

	var doc cldrZhDoc
	if err := json.Unmarshal(res.Body, &doc); err != nil {
		return fmt.Errorf("parse cldr zh: %w", err)
	}
	var raw map[string]string
	for _, v := range doc.Main {
		raw = v.LocaleDisplayNames.Languages
		break
	}
	if raw == nil {
		return fmt.Errorf("no languages map in cldr zh data")
	}
	fmt.Printf("CLDR zh entries: %d\n", len(raw))

	// Filter to base codes; map to ISO 639-3 via the alias table in cldr.go.
	resolved := map[string]string{}
	for key, name := range raw {
		if strings.Contains(key, "-") {
			continue
		}
		iso3 := normalizeISO(key)
		if iso3 == "" || name == "" {
			continue
		}
		resolved[iso3] = name
	}
	fmt.Printf("resolved to ISO 639-3: %d\n", len(resolved))

	isos := make([]string, 0, len(resolved))
	for iso := range resolved {
		isos = append(isos, iso)
	}
	sort.Strings(isos)

	updated, unchanged, missing := 0, 0, 0
	for _, iso := range isos {
		path := filepath.Join(opts.Dir, iso+".md")
		if _, err := corpus.ReadFile(path); err != nil {
			missing++
			continue
		}
		r, err := corpus.SetScalars(path, []corpus.ScalarOp{
			{Key: "name_zh", Value: resolved[iso], Mode: corpus.IfMissing},
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

	fmt.Printf("filled (was empty): %d\nunchanged (already had value): %d\nmissing iso (no .md file): %d\n", updated, unchanged, missing)
	if opts.DryRun {
		fmt.Println("(dry run — no files written)")
	}
	return nil
}

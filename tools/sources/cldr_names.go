// cldr_names fills the `translations[]` array on each language file from
// Unicode CLDR locale data. One CLDR file per target language (zh, ja, hi,
// ko, ar, es, fr, de, pt) provides a {language_code → display_name_in_target} map.
//
// Variant keys ("en-GB", "ars-alt-menu", etc.) are skipped — only base
// ISO 639-1/3 codes are used.
//
// Merge semantics: each call appends new translation_iso entries; existing
// entries (e.g. those already filled by `wikidata_names`) keep their data.
// CLDR fills gaps; it does not overwrite Wikidata.
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

// cldrLocale maps a target language (used as translation_iso in our schema)
// to the CLDR locale folder name that holds its localised language names.
type cldrLocale struct {
	Iso    string // ISO 639-3 of the target, used as translation_iso
	Folder string // CLDR folder under cldr-localenames-full/main/
}

var cldrLocales = []cldrLocale{
	{Iso: "zho", Folder: "zh"},
	{Iso: "jpn", Folder: "ja"},
	{Iso: "hin", Folder: "hi"},
	{Iso: "kor", Folder: "ko"},
	{Iso: "ara", Folder: "ar"},
	{Iso: "spa", Folder: "es"},
	{Iso: "fra", Folder: "fr"},
	{Iso: "deu", Folder: "de"},
	{Iso: "por", Folder: "pt"},
}

const cldrLocaleURLTmpl = "https://raw.githubusercontent.com/unicode-org/cldr-json/main/cldr-json/cldr-localenames-full/main/%s/languages.json"

type cldrLocaleDoc struct {
	Main map[string]struct {
		LocaleDisplayNames struct {
			Languages map[string]string `json:"languages"`
		} `json:"localeDisplayNames"`
	} `json:"main"`
}

func (cldrNames) Run(opts Options) error {
	// Per-locale: build {iso639_3_of_language: name_in_target}.
	byTarget := map[string]map[string]string{} // translation_iso → {iso → name}
	for _, l := range cldrLocales {
		m, err := fetchCLDRLocale(l, opts.Force)
		if err != nil {
			return fmt.Errorf("%s: %w", l.Folder, err)
		}
		byTarget[l.Iso] = m
	}

	// Build {iso_of_language: {translation_iso: name}} from those.
	byLang := map[string]map[string]string{}
	for tiso, m := range byTarget {
		for lang, name := range m {
			by, ok := byLang[lang]
			if !ok {
				by = map[string]string{}
				byLang[lang] = by
			}
			by[tiso] = name
		}
	}

	langs := make([]string, 0, len(byLang))
	for l := range byLang {
		langs = append(langs, l)
	}
	sort.Strings(langs)

	updated, unchanged, missing := 0, 0, 0
	for _, lang := range langs {
		path := filepath.Join(opts.Dir, lang+".md")
		if _, err := corpus.ReadFile(path); err != nil {
			missing++
			continue
		}
		byT := byLang[lang]
		targetIsos := make([]string, 0, len(byT))
		for t := range byT {
			targetIsos = append(targetIsos, t)
		}
		sort.Strings(targetIsos)
		items := make([]*corpus.Item, 0, len(targetIsos))
		for _, t := range targetIsos {
			it := corpus.NewItem()
			it.Set("translation_iso", t)
			it.Set("name", byT[t])
			items = append(items, it)
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

	fmt.Printf("updated: %d  unchanged: %d  missing iso (no .md file): %d\n", updated, unchanged, missing)
	for _, l := range cldrLocales {
		fmt.Printf("  %s (CLDR folder %q): %d resolved\n", l.Iso, l.Folder, len(byTarget[l.Iso]))
	}
	if opts.DryRun {
		fmt.Println("(dry run — no files written)")
	}
	return nil
}

// fetchCLDRLocale grabs one CLDR locale's languages.json and returns a map
// of {iso639_3_of_named_language → name_in_this_locale}.
func fetchCLDRLocale(l cldrLocale, force bool) (map[string]string, error) {
	res, err := corpus.FetchCached(corpus.FetchOptions{
		Name:  "cldr-localenames-" + l.Folder,
		URL:   fmt.Sprintf(cldrLocaleURLTmpl, l.Folder),
		Force: force,
		Ext:   "json",
	})
	if err != nil {
		return nil, err
	}
	var doc cldrLocaleDoc
	if err := json.Unmarshal(res.Body, &doc); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	var raw map[string]string
	for _, v := range doc.Main {
		raw = v.LocaleDisplayNames.Languages
		break
	}
	if raw == nil {
		return nil, fmt.Errorf("no languages map in CLDR data")
	}
	out := map[string]string{}
	for key, name := range raw {
		if strings.Contains(key, "-") {
			continue
		}
		iso3 := normalizeISO(key)
		if iso3 == "" || name == "" {
			continue
		}
		out[iso3] = name
	}
	return out, nil
}

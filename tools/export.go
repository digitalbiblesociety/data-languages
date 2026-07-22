// export.go — machine-readable build artifact (-export): languages.json with
// the typed frontmatter of every languages/<iso>.md. Markdown bodies are left
// out to keep the artifact data-only. With -check, verifies the artifact on
// disk is in sync with the corpus.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"languages/tools/corpus"
)

type exportTranslation struct {
	TranslationISO string `json:"translation_iso"`
	Name           string `json:"name"`
	Auto           bool   `json:"auto,omitempty"`
}

type exportDialect struct {
	RolvCode    string `json:"rolv_code"`
	LanguageTag string `json:"language_tag,omitempty"`
	Name        string `json:"name"`
	CountryID   string `json:"country_id,omitempty"`
	Location    string `json:"location,omitempty"`
}

// exportEntry mirrors the canonical schema key order.
type exportEntry struct {
	Iso                     string              `json:"iso"`
	Iso6391                 string              `json:"iso639_1,omitempty"`
	MacrolanguageID         string              `json:"macrolanguage_id,omitempty"`
	Name                    string              `json:"name"`
	Autonym                 string              `json:"autonym,omitempty"`
	AltNames                []string            `json:"alt_names,omitempty"`
	Population              *int                `json:"population,omitempty"`
	CountryID               string              `json:"country_id,omitempty"`
	CountryName             string              `json:"country_name,omitempty"`
	StatusID                string              `json:"status_id,omitempty"`
	Scope                   string              `json:"scope,omitempty"`
	LanguageType            string              `json:"language_type,omitempty"`
	IsoLWC                  string              `json:"iso_lwc,omitempty"`
	Latitude                *float64            `json:"latitude,omitempty"`
	Longitude               *float64            `json:"longitude,omitempty"`
	Scripts                 []string            `json:"scripts,omitempty"`
	Glottocode              string              `json:"glottocode,omitempty"`
	GlottologFamilyID       string              `json:"glottolog_family_id,omitempty"`
	GlottologFamilyName     string              `json:"glottolog_family_name,omitempty"`
	GlottologClassification string              `json:"glottolog_classification,omitempty"`
	WikidataID              string              `json:"wikidata_id,omitempty"`
	WikipediaURL            string              `json:"wikipedia_url,omitempty"`
	Translations            []exportTranslation `json:"translations,omitempty"`
	RolvDialects            []exportDialect     `json:"rolv_dialects,omitempty"`
}

// fm is one parsed file: scalar entries plus the raw block for array fields.
type fm struct {
	entries []corpus.Entry
	block   string
}

func parseFM(path string) (fm, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return fm{}, err
	}
	block, _, err := corpus.Split(data)
	if err != nil {
		return fm{}, fmt.Errorf("%s: %w", path, err)
	}
	entries, err := corpus.ReadEntries(block)
	if err != nil {
		return fm{}, fmt.Errorf("%s: %w", path, err)
	}
	return fm{entries: entries, block: block}, nil
}

func (f fm) scalar(key string) string {
	for _, e := range f.entries {
		if e.Key == key {
			v := corpus.Unquote(e.Value)
			if v == "null" || v == "~" {
				return ""
			}
			return v
		}
	}
	return ""
}

func (f fm) intPtr(key string) *int {
	v := f.scalar(key)
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return nil
	}
	return &n
}

func (f fm) floatPtr(key string) *float64 {
	v := f.scalar(key)
	if v == "" {
		return nil
	}
	x, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return nil
	}
	return &x
}

func (f fm) inline(key string) []string {
	return corpus.InlineList(f.scalar(key))
}

func itemString(it *corpus.Item, key string) string {
	v, _ := it.Get(key)
	s, _ := v.(string)
	return s
}

func (f fm) translations() []exportTranslation {
	items, start, _ := corpus.ReadArrayField(f.block, "translations")
	if start == -1 {
		return nil
	}
	var out []exportTranslation
	for _, it := range items {
		auto, _ := it.Get("auto")
		b, _ := auto.(bool)
		out = append(out, exportTranslation{
			TranslationISO: itemString(it, "translation_iso"),
			Name:           itemString(it, "name"),
			Auto:           b,
		})
	}
	return out
}

func (f fm) rolvDialects() []exportDialect {
	items, start, _ := corpus.ReadArrayField(f.block, "rolv_dialects")
	if start == -1 {
		return nil
	}
	var out []exportDialect
	for _, it := range items {
		out = append(out, exportDialect{
			RolvCode:    itemString(it, "rolv_code"),
			LanguageTag: itemString(it, "language_tag"),
			Name:        itemString(it, "name"),
			CountryID:   itemString(it, "country_id"),
			Location:    itemString(it, "location"),
		})
	}
	return out
}

func buildExport(dir string) ([]exportEntry, error) {
	codes, err := corpus.ListCodes(dir)
	if err != nil {
		return nil, err
	}
	var entries []exportEntry
	for _, c := range codes {
		f, err := parseFM(filepath.Join(dir, c+".md"))
		if err != nil {
			return nil, err
		}
		entries = append(entries, exportEntry{
			Iso:                     f.scalar("iso"),
			Iso6391:                 f.scalar("iso639_1"),
			MacrolanguageID:         f.scalar("macrolanguage_id"),
			Name:                    f.scalar("name"),
			Autonym:                 f.scalar("autonym"),
			AltNames:                f.inline("alt_names"),
			Population:              f.intPtr("population"),
			CountryID:               f.scalar("country_id"),
			CountryName:             f.scalar("country_name"),
			StatusID:                f.scalar("status_id"),
			Scope:                   f.scalar("scope"),
			LanguageType:            f.scalar("language_type"),
			IsoLWC:                  f.scalar("iso_lwc"),
			Latitude:                f.floatPtr("latitude"),
			Longitude:               f.floatPtr("longitude"),
			Scripts:                 f.inline("scripts"),
			Glottocode:              f.scalar("glottocode"),
			GlottologFamilyID:       f.scalar("glottolog_family_id"),
			GlottologFamilyName:     f.scalar("glottolog_family_name"),
			GlottologClassification: f.scalar("glottolog_classification"),
			WikidataID:              f.scalar("wikidata_id"),
			WikipediaURL:            f.scalar("wikipedia_url"),
			Translations:            f.translations(),
			RolvDialects:            f.rolvDialects(),
		})
	}
	return entries, nil
}

func renderJSON(entries []exportEntry) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(entries); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

const exportPath = "languages.json"

func runExport(dir string, check bool) int {
	entries, err := buildExport(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "export: %v\n", err)
		return 2
	}
	out, err := renderJSON(entries)
	if err != nil {
		fmt.Fprintf(os.Stderr, "export: %v\n", err)
		return 2
	}

	if check {
		have, err := os.ReadFile(exportPath)
		if err != nil || !bytes.Equal(have, out) {
			fmt.Fprintf(os.Stderr, "%s is out of sync with %s/ — run: go run ./tools -export\n", exportPath, dir)
			return 1
		}
		fmt.Println("export artifact in sync")
		return 0
	}

	if err := os.WriteFile(exportPath, out, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", exportPath, err)
		return 2
	}
	fmt.Printf("wrote %s (%d entries)\n", exportPath, len(entries))
	return 0
}

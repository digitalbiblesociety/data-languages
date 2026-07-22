// translatefill drives LLM-assisted backfill of the translations[] field for
// one target language (it began life as the Spanish-only "spanishfill").
//
// It has two modes:
//
//	export: scan languages/<iso>.md, and for every file that lacks a
//	        translation_iso=<lang> entry, emit a JSON array of
//	        {iso, name, autonym, alt_names}. This is the work-list handed to
//	        the translation agents.
//
//	import: read a JSON object {iso: translated_name} produced by the agents
//	        and merge each as a translations[] item {translation_iso: <lang>,
//	        name, auto: true}. Uses corpus.MergeFile so canonical YAML order,
//	        dedup, and sorting all match the other translation sources.
//	        Existing curated entries are never overwritten.
//
// Usage from the repo root:
//
//	go run ./tools/translatefill -lang kor -mode export > /tmp/kor-todo.json
//	go run ./tools/translatefill -lang kor -mode import -in /tmp/kor-names.json [-dry-run]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"languages/tools/corpus"
)

var (
	itemOrder = []string{"translation_iso", "name", "auto"}
	reLang    = regexp.MustCompile(`^[a-z]{3}$`)
)

type todo struct {
	Iso      string   `json:"iso"`
	Name     string   `json:"name"`
	Autonym  string   `json:"autonym,omitempty"`
	AltNames []string `json:"alt_names,omitempty"`
}

func main() {
	var (
		dir    = flag.String("dir", "languages", "directory holding <iso>.md files")
		lang   = flag.String("lang", "", "target language (ISO 639-3, e.g. kor)")
		mode   = flag.String("mode", "", "export | import")
		in     = flag.String("in", "", "import: path to JSON {iso: translated_name}")
		dryRun = flag.Bool("dry-run", false, "import: preview without writing")
	)
	flag.Parse()

	if !reLang.MatchString(*lang) {
		fmt.Fprintln(os.Stderr, "pass -lang <iso 639-3 code>, e.g. -lang kor")
		os.Exit(2)
	}

	switch *mode {
	case "export":
		if err := runExport(*dir, *lang); err != nil {
			fmt.Fprintln(os.Stderr, "export:", err)
			os.Exit(1)
		}
	case "import":
		if err := runImport(*dir, *lang, *in, *dryRun); err != nil {
			fmt.Fprintln(os.Stderr, "import:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "pass -mode export or -mode import")
		os.Exit(2)
	}
}

// hasTarget reports whether the file's translations[] already contains lang.
func hasTarget(block, lang string) bool {
	items, start, _ := corpus.ReadArrayField(block, "translations")
	if start == -1 {
		return false
	}
	for _, it := range items {
		if v, ok := it.Get("translation_iso"); ok {
			if s, _ := v.(string); s == lang {
				return true
			}
		}
	}
	return false
}

func topScalar(entries []corpus.Entry, key string) string {
	for _, e := range entries {
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

func runExport(dir, lang string) error {
	paths, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return err
	}
	sort.Strings(paths)
	var list []todo
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		block, _, err := corpus.Split(data)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if hasTarget(block, lang) {
			continue
		}
		entries, err := corpus.ReadEntries(block)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		iso := strings.TrimSuffix(filepath.Base(path), ".md")
		list = append(list, todo{
			Iso:      iso,
			Name:     topScalar(entries, "name"),
			Autonym:  topScalar(entries, "autonym"),
			AltNames: corpus.InlineList(topScalar(entries, "alt_names")),
		})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(list)
}

func runImport(dir, lang, in string, dryRun bool) error {
	if in == "" {
		return fmt.Errorf("-in is required")
	}
	data, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	var names map[string]string
	if err := json.Unmarshal(data, &names); err != nil {
		return fmt.Errorf("parse %s: %w", in, err)
	}

	isos := make([]string, 0, len(names))
	for iso := range names {
		isos = append(isos, iso)
	}
	sort.Strings(isos)

	updated, unchanged, skipped := 0, 0, 0
	for _, iso := range isos {
		name := strings.TrimSpace(names[iso])
		if name == "" {
			skipped++
			continue
		}
		path := filepath.Join(dir, iso+".md")
		if _, err := os.Stat(path); err != nil {
			skipped++
			continue
		}
		it := corpus.NewItem()
		it.Set("translation_iso", lang)
		it.Set("name", name)
		it.Set("auto", true)
		r, err := corpus.MergeFile(path, "translations", []*corpus.Item{it}, corpus.MergeOptions{
			DedupKey:   "translation_iso",
			FieldOrder: itemOrder,
			SortByKey:  true,
			DryRun:     dryRun,
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
	fmt.Printf("import: updated %d  unchanged %d  skipped %d  (of %d names)\n", updated, unchanged, skipped, len(names))
	if dryRun {
		fmt.Println("(dry run — no files written)")
	}
	return nil
}

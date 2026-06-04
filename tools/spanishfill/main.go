// spanishfill is a one-off helper for the Spanish (spa) translation backfill.
//
// It has two modes:
//
//	export: scan languages/<iso>.md, and for every file that lacks a
//	        translation_iso=spa entry, emit a JSON array of
//	        {iso, name, autonym, alt_names}. This is the work-list handed to
//	        the translation agents.
//
//	import: read a JSON object {iso: spanish_name} produced by the agents and
//	        merge each as a translations[] item {translation_iso: spa, name,
//	        auto: true}. Uses corpus.MergeFile so canonical YAML order, dedup,
//	        and sorting all match the other translation sources. Existing
//	        curated spa entries are never overwritten.
//
// Usage from the repo root:
//
//	go run ./tools/spanishfill -mode export > /tmp/spa-todo.json
//	go run ./tools/spanishfill -mode import -in /tmp/spa-names.json [-dry-run]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"languages/tools/corpus"
)

const targetIso = "spa"

var itemOrder = []string{"translation_iso", "name", "auto"}

type todo struct {
	Iso      string   `json:"iso"`
	Name     string   `json:"name"`
	Autonym  string   `json:"autonym,omitempty"`
	AltNames []string `json:"alt_names,omitempty"`
}

func main() {
	var (
		dir    = flag.String("dir", "languages", "directory holding <iso>.md files")
		mode   = flag.String("mode", "", "export | import")
		in     = flag.String("in", "", "import: path to JSON {iso: spanish_name}")
		dryRun = flag.Bool("dry-run", false, "import: preview without writing")
	)
	flag.Parse()

	switch *mode {
	case "export":
		if err := runExport(*dir); err != nil {
			fmt.Fprintln(os.Stderr, "export:", err)
			os.Exit(1)
		}
	case "import":
		if err := runImport(*dir, *in, *dryRun); err != nil {
			fmt.Fprintln(os.Stderr, "import:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "pass -mode export or -mode import")
		os.Exit(2)
	}
}

// hasTarget reports whether the file's translations[] already contains targetIso.
func hasTarget(block string) bool {
	items, start, _ := corpus.ReadArrayField(block, "translations")
	if start == -1 {
		return false
	}
	for _, it := range items {
		if v, ok := it.Get("translation_iso"); ok {
			if s, _ := v.(string); s == targetIso {
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

// altNames pulls the inline alt_names array if present (e.g. `alt_names: [A, B]`).
func altNames(entries []corpus.Entry) []string {
	for _, e := range entries {
		if e.Key != "alt_names" {
			continue
		}
		v := strings.TrimSpace(e.Value)
		if !strings.HasPrefix(v, "[") || !strings.HasSuffix(v, "]") {
			return nil
		}
		v = strings.TrimSuffix(strings.TrimPrefix(v, "["), "]")
		var out []string
		for _, part := range strings.Split(v, ",") {
			p := corpus.Unquote(strings.TrimSpace(part))
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return nil
}

func runExport(dir string) error {
	entriesGlob, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return err
	}
	sort.Strings(entriesGlob)
	var list []todo
	for _, path := range entriesGlob {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		block, _, err := corpus.Split(data)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if hasTarget(block) {
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
			AltNames: altNames(entries),
		})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(list)
}

func runImport(dir, in string, dryRun bool) error {
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
		it.Set("translation_iso", targetIso)
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

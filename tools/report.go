// report.go — corpus coverage dashboard (-report): how many files carry each
// frontmatter key, translation coverage per target language with auto (i.e.
// machine-translated, unreviewed) counts, and markdown-body presence. This is
// the progress view for curation work; it changes nothing on disk.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"languages/tools/corpus"
)

type translationStat struct {
	total int
	auto  int
}

func runReport(dir string) int {
	codes, err := corpus.ListCodes(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "report: %v\n", err)
		return 2
	}

	keyCount := map[string]int{}
	bodies := 0
	perTarget := map[string]*translationStat{}

	for _, c := range codes {
		data, err := os.ReadFile(filepath.Join(dir, c+".md"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "report: %v\n", err)
			return 2
		}
		block, body, err := corpus.Split(data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "report: %s: %v\n", c, err)
			return 2
		}
		if strings.TrimSpace(body) != "" {
			bodies++
		}
		entries, err := corpus.ReadEntries(block)
		if err != nil {
			fmt.Fprintf(os.Stderr, "report: %s: %v\n", c, err)
			return 2
		}
		for _, e := range entries {
			v := corpus.Unquote(e.Value)
			if !e.IsSeq && (v == "" || v == "null" || v == "~" || v == "[]") {
				continue
			}
			keyCount[e.Key]++
		}
		items, start, _ := corpus.ReadArrayField(block, "translations")
		if start == -1 {
			continue
		}
		for _, it := range items {
			v, _ := it.Get("translation_iso")
			iso, _ := v.(string)
			if iso == "" {
				continue
			}
			st := perTarget[iso]
			if st == nil {
				st = &translationStat{}
				perTarget[iso] = st
			}
			st.total++
			if a, _ := it.Get("auto"); a == true {
				st.auto++
			}
		}
	}

	n := len(codes)
	pct := func(c int) string { return fmt.Sprintf("%3d%%", (100*c)/n) }

	fmt.Printf("%d files\n\nfield coverage\n", n)
	for _, k := range corpus.CanonicalOrder {
		fmt.Printf("  %-26s %5d  %s\n", k, keyCount[k], pct(keyCount[k]))
	}
	fmt.Printf("  %-26s %5d  %s\n", "(markdown body)", bodies, pct(bodies))

	targets := make([]string, 0, len(perTarget))
	for iso := range perTarget {
		targets = append(targets, iso)
	}
	sort.Strings(targets)
	fmt.Printf("\ntranslation coverage (auto = machine-translated, unreviewed)\n")
	for _, iso := range targets {
		st := perTarget[iso]
		fmt.Printf("  %s  %5d  %s   auto %5d\n", iso, st.total, pct(st.total), st.auto)
	}
	return 0
}

// iso639 enriches each language file with the registrar's own metadata from
// the SIL ISO 639-3 code tables:
//
//   - iso639_1         (ISO 639-1 two-letter code, when one exists)
//   - macrolanguage_id (parent macrolanguage, for individual member codes)
//   - scope            (individual | macrolanguage | special)
//   - language_type    (living | extinct | ancient | historical | constructed | special)
//
// All four are authoritative registrar values, so re-runs overwrite.
// Retired macrolanguage memberships (I_Status = R) are ignored.
//
// Sources (tab-separated, cached monthly):
//
//	https://iso639-3.sil.org/sites/iso639-3/files/downloads/iso-639-3.tab
//	https://iso639-3.sil.org/sites/iso639-3/files/downloads/iso-639-3-macrolanguages.tab
//
// Honors --only iso[,iso,...] to scope a test run.
package sources

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"languages/tools/corpus"
)

func init() { Register(iso639{}) }

type iso639 struct{}

func (iso639) Name() string { return "iso639" }

const (
	silCodeTableURL      = "https://iso639-3.sil.org/sites/iso639-3/files/downloads/iso-639-3.tab"
	silMacrolanguagesURL = "https://iso639-3.sil.org/sites/iso639-3/files/downloads/iso-639-3-macrolanguages.tab"
)

// silScopes / silTypes map the table's single-letter columns to the schema's
// spelled-out vocabulary.
var (
	silScopes = map[string]string{"I": "individual", "M": "macrolanguage", "S": "special"}
	silTypes  = map[string]string{"A": "ancient", "C": "constructed", "E": "extinct", "H": "historical", "L": "living", "S": "special"}
)

type silCodeRow struct {
	Part1 string // ISO 639-1 code, "" when none exists
	Scope string // spelled-out scope, "" on unknown letter
	Type  string // spelled-out language type, "" on unknown letter
}

func (iso639) Run(opts Options) error {
	table, err := fetchSILCodeTable(opts.Force)
	if err != nil {
		return err
	}
	macro, err := fetchSILMacrolanguages(opts.Force)
	if err != nil {
		return err
	}
	fmt.Printf("sil: %d code rows, %d active macrolanguage members\n", len(table), len(macro))

	codes, err := corpus.ListCodes(opts.Dir)
	if err != nil {
		return err
	}
	sort.Strings(codes)
	codes = onlyFilter(codes, opts.Only)

	updated, unchanged, missing := 0, 0, 0
	var missingISOs []string
	withPart1, withMacro := 0, 0
	for _, iso := range codes {
		row, ok := table[iso]
		if !ok {
			missing++
			missingISOs = append(missingISOs, iso)
			continue
		}
		var ops []corpus.ScalarOp
		if row.Scope != "" {
			ops = append(ops, corpus.ScalarOp{Key: "scope", Value: row.Scope, Mode: corpus.Overwrite})
		}
		if row.Type != "" {
			ops = append(ops, corpus.ScalarOp{Key: "language_type", Value: row.Type, Mode: corpus.Overwrite})
		}
		if row.Part1 != "" {
			ops = append(ops, corpus.ScalarOp{Key: "iso639_1", Value: row.Part1, Mode: corpus.Overwrite})
			withPart1++
		}
		if m := macro[iso]; m != "" {
			ops = append(ops, corpus.ScalarOp{Key: "macrolanguage_id", Value: m, Mode: corpus.Overwrite})
			withMacro++
		}
		path := filepath.Join(opts.Dir, iso+".md")
		r, err := corpus.SetScalars(path, ops, opts.DryRun)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if r.Changed {
			updated++
		} else {
			unchanged++
		}
	}

	fmt.Printf("coverage: iso639_1=%d  macrolanguage_id=%d\n", withPart1, withMacro)
	fmt.Printf("updated: %d\nunchanged: %d\nmissing iso (not in code table): %d", updated, unchanged, missing)
	if n := len(missingISOs); n > 0 {
		show := missingISOs
		if n > 10 {
			show = missingISOs[:10]
		}
		fmt.Printf(" — %v", show)
		if n > 10 {
			fmt.Printf(" … (and %d more)", n-10)
		}
	}
	fmt.Println()
	if opts.DryRun {
		fmt.Println("(dry run — no files written)")
	}
	return nil
}

// fetchSILCodeTable downloads iso-639-3.tab and returns a map of
// ISO 639-3 → {Part1, Scope, Language_Type}.
func fetchSILCodeTable(force bool) (map[string]silCodeRow, error) {
	res, err := corpus.FetchCached(corpus.FetchOptions{
		Name:  "sil-code-table",
		URL:   silCodeTableURL,
		Force: force,
		Ext:   "tab",
	})
	if err != nil {
		return nil, err
	}
	rows, err := parseTab(string(res.Body), "Id", "Part1", "Scope", "Language_Type")
	if err != nil {
		return nil, fmt.Errorf("iso-639-3.tab: %w", err)
	}
	out := make(map[string]silCodeRow, len(rows))
	for _, r := range rows {
		id := r["Id"]
		if id == "" {
			continue
		}
		out[id] = silCodeRow{
			Part1: r["Part1"],
			Scope: silScopes[r["Scope"]],
			Type:  silTypes[r["Language_Type"]],
		}
	}
	return out, nil
}

// fetchSILMacrolanguages downloads iso-639-3-macrolanguages.tab and returns
// a map of member ISO 639-3 → macrolanguage ISO 639-3, active rows only.
func fetchSILMacrolanguages(force bool) (map[string]string, error) {
	res, err := corpus.FetchCached(corpus.FetchOptions{
		Name:  "sil-macrolanguages",
		URL:   silMacrolanguagesURL,
		Force: force,
		Ext:   "tab",
	})
	if err != nil {
		return nil, err
	}
	rows, err := parseTab(string(res.Body), "M_Id", "I_Id", "I_Status")
	if err != nil {
		return nil, fmt.Errorf("iso-639-3-macrolanguages.tab: %w", err)
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		if r["I_Status"] != "A" {
			continue
		}
		if m, i := r["M_Id"], r["I_Id"]; m != "" && i != "" {
			out[i] = m
		}
	}
	return out, nil
}

// parseTab parses a tab-separated table with a header row into one map per
// data row, keyed by the requested column names. A missing column is an
// error; short rows yield "" for their absent columns.
func parseTab(body string, columns ...string) ([]map[string]string, error) {
	body = strings.TrimPrefix(body, "\ufeff")
	lines := strings.Split(body, "\n")
	if len(lines) < 2 {
		return nil, fmt.Errorf("%d lines", len(lines))
	}
	header := strings.Split(strings.TrimRight(lines[0], "\r"), "\t")
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.TrimSpace(h)] = i
	}
	for _, c := range columns {
		if _, ok := idx[c]; !ok {
			return nil, fmt.Errorf("missing column %q in header %v", c, header)
		}
	}
	var out []map[string]string
	for _, ln := range lines[1:] {
		ln = strings.TrimRight(ln, "\r")
		if ln == "" {
			continue
		}
		f := strings.Split(ln, "\t")
		row := make(map[string]string, len(columns))
		for _, c := range columns {
			if i := idx[c]; i < len(f) {
				row[c] = strings.TrimSpace(f[i])
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// onlyFilter narrows codes to the comma-separated subset in only; "" keeps
// everything. Shared by sources that honor --only.
func onlyFilter(codes []string, only string) []string {
	if strings.TrimSpace(only) == "" {
		return codes
	}
	want := map[string]bool{}
	for _, c := range strings.Split(only, ",") {
		if c = strings.TrimSpace(c); c != "" {
			want[c] = true
		}
	}
	out := codes[:0]
	for _, c := range codes {
		if want[c] {
			out = append(out, c)
		}
	}
	fmt.Printf("scope: --only restricted to %d code(s)\n", len(out))
	return out
}

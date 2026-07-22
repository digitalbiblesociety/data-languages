package corpus

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const corpusDir = "../../languages"

// TestRoundTrip proves Split→reassemble is byte-identical for every file in
// the corpus. SetScalars and MergeFile write files back exactly this way, so
// this guarantees an untouched region of a file can never be reformatted by
// the tools.
func TestRoundTrip(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(corpusDir, "*.md"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("glob corpus: %v (%d files)", err, len(paths))
	}
	for _, p := range paths {
		orig, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		block, body, err := Split(orig)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		got := "---\n" + strings.TrimRight(block, "\n") + "\n---\n" + body
		if got != string(orig) {
			t.Errorf("%s: round-trip differs", p)
		}
	}
}

// TestCanonicalOrderMatchesSchema pins CanonicalOrder (and requiredFields) to
// schema.json so the hand-written validator cannot drift from the published
// schema — adding a key to one place without the other fails here.
func TestCanonicalOrderMatchesSchema(t *testing.T) {
	data, err := os.ReadFile("../../schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Required []string        `json:"required"`
		Props    json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}

	// Walk the raw properties object with a token decoder to keep key order.
	dec := json.NewDecoder(bytes.NewReader(schema.Props))
	if _, err := dec.Token(); err != nil { // opening {
		t.Fatal(err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}

	if got, want := strings.Join(CanonicalOrder, ","), strings.Join(keys, ","); got != want {
		t.Errorf("CanonicalOrder does not match schema.json properties order\n validator: %s\n schema:    %s", got, want)
	}
	if got, want := strings.Join(requiredFields, ","), strings.Join(schema.Required, ","); got != want {
		t.Errorf("requiredFields does not match schema.json required\n validator: %s\n schema:    %s", got, want)
	}
}

// TestMergeFileBehaviour exercises the writeback path on a synthetic file:
// canonical insertion of an absent field, dedup by key, sorting, and that an
// identical second merge is a no-op.
func TestMergeFileBehaviour(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zzz.md")
	orig := "---\niso: zzz\nname: Test\nrolv_dialects:\n  - rolv_code: X1\n    name: Testish\n---\n\nBody text.\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	mk := func(iso, name string) *Item {
		it := NewItem()
		it.Set("translation_iso", iso)
		it.Set("name", name)
		it.Set("auto", true)
		return it
	}
	opts := MergeOptions{
		DedupKey:   "translation_iso",
		FieldOrder: []string{"translation_iso", "name", "auto"},
		SortByKey:  true,
	}

	res, err := MergeFile(path, "translations", []*Item{mk("spa", "Testés"), mk("fra", "testois")}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || res.Added != 2 {
		t.Fatalf("first merge: got %+v, want Changed with Added=2", res)
	}

	got, _ := os.ReadFile(path)
	want := "---\niso: zzz\nname: Test\ntranslations:\n  - translation_iso: fra\n    name: testois\n    auto: true\n  - translation_iso: spa\n    name: Testés\n    auto: true\nrolv_dialects:\n  - rolv_code: X1\n    name: Testish\n---\n\nBody text.\n"
	if string(got) != want {
		t.Fatalf("merged file:\n%s\nwant:\n%s", got, want)
	}

	// Same items again: dedup makes it a no-op, file untouched.
	res, err = MergeFile(path, "translations", []*Item{mk("spa", "OTHER")}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Fatalf("second merge: got %+v, want unchanged (existing entries are never overwritten)", res)
	}
}

// TestReferentialChecks proves the Index-backed rules catch dangling and
// malformed references that per-file validation cannot see.
func TestReferentialChecks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zzz.md")
	content := "---\niso: zzz\nmacrolanguage_id: qqq\nname: Test\ncountry_id: XA\ntranslations:\n" +
		"  - translation_iso: spa\n    name: B\n" +
		"  - translation_iso: fra\n    name: A\n" +
		"  - translation_iso: fra\n    name: A\n" +
		"  - translation_iso: xxx\n    name: C\n---\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := &Index{Scope: map[string]string{"zzz": "individual", "qqq": "individual", "spa": "individual", "fra": "individual"}}

	errs := strings.Join(ValidateFile(path, idx), "\n")
	for _, want := range []string{
		`"qqq" is not a macrolanguage`,
		`"XA" is not an assigned ISO 3166-1`,
		`not sorted by translation_iso`,
		`duplicate translation_iso "fra"`,
		`translation_iso "xxx" has no languages/xxx.md file`,
	} {
		if !strings.Contains(errs, want) {
			t.Errorf("missing expected error %q in:\n%s", want, errs)
		}
	}

	// nil index: referential checks are skipped, shape checks still run.
	errs = strings.Join(ValidateFile(path, nil), "\n")
	if strings.Contains(errs, "macrolanguage") || strings.Contains(errs, "sorted") {
		t.Errorf("nil index should skip referential checks, got:\n%s", errs)
	}
}

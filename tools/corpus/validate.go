package corpus

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// CanonicalOrder mirrors the `properties` order in ../schema.json. Files
// emit and validate keys in this order; out-of-order keys are a validation
// error so writers stay consistent.
var CanonicalOrder = []string{
	"iso",
	"iso639_1",
	"macrolanguage_id",
	"name",
	"autonym",
	"alt_names",
	"population",
	"country_id",
	"country_name",
	"location",
	"area",
	"status_id",
	"scope",
	"language_type",
	"latitude",
	"longitude",
	"language_map_img",
	"scripts",
	"glottocode",
	"glottolog_family_id",
	"glottolog_family_name",
	"glottolog_classification",
	"wikidata_id",
	"wikipedia_url",
	"translations",
	"rolv_dialects",
}

var (
	requiredFields = []string{"iso", "name"}

	allowedKeys = func() map[string]int {
		m := map[string]int{}
		for i, k := range CanonicalOrder {
			m[k] = i
		}
		return m
	}()

	statusValues       = []string{"0", "1", "2", "3", "4", "5", "6a", "6b", "7", "8a", "8b", "9", "10"}
	scopeValues        = []string{"individual", "macrolanguage", "special"}
	languageTypeValues = []string{"living", "extinct", "ancient", "historical", "constructed", "special"}

	reISO        = regexp.MustCompile(`^[a-z]{3}$`)
	reISO6391    = regexp.MustCompile(`^[a-z]{2}$`)
	reWikidataID = regexp.MustCompile(`^Q[1-9][0-9]*$`)
	reCountryID  = regexp.MustCompile(`^[A-Z]{2}$`)
	reGlottocode = regexp.MustCompile(`^[a-z0-9]{4}\d{4}$`)
	reScriptCode = regexp.MustCompile(`^[A-Z][a-z]{3}$`)
	// matches a YAML inline string array: `[A]`, `[A, B]`, `[]`
	reInlineArray = regexp.MustCompile(`^\[\s*(?:[^,\[\]\s]+(?:\s*,\s*[^,\[\]\s]+)*)?\s*\]$`)

	// arraySchemas describes how to validate each top-level sequence field.
	arraySchemas = map[string]arraySchema{
		"rolv_dialects": {
			required: []string{"rolv_code", "name"},
			allowed:  setOf("rolv_code", "language_tag", "name", "country_id", "location"),
		},
		"translations": {
			required: []string{"translation_iso", "name"},
			allowed:  setOf("translation_iso", "name", "auto"),
		},
	}
)

type arraySchema struct {
	required []string
	allowed  map[string]bool
}

func setOf(xs ...string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func oneOf(v string, vals []string) bool {
	for _, s := range vals {
		if v == s {
			return true
		}
	}
	return false
}

// ValidateFile returns the human-readable issues found in path's frontmatter.
// An empty slice means the file is conformant.
func ValidateFile(path string) []string {
	entries, err := ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("frontmatter: %v", err)}
	}

	var errs []string
	seen := map[string]bool{}
	lastIdx := -1
	for _, e := range entries {
		if seen[e.Key] {
			errs = append(errs, fmt.Sprintf("line %d: duplicate key %q", e.Line, e.Key))
			continue
		}
		seen[e.Key] = true

		idx, ok := allowedKeys[e.Key]
		if !ok {
			errs = append(errs, fmt.Sprintf("line %d: unknown key %q (not in schema)", e.Line, e.Key))
			continue
		}
		if idx < lastIdx {
			errs = append(errs, fmt.Sprintf("line %d: key %q out of canonical order", e.Line, e.Key))
		}
		lastIdx = idx

		if msg := validateValue(e); msg != "" {
			errs = append(errs, fmt.Sprintf("line %d: %s: %s", e.Line, e.Key, msg))
		}
	}

	for _, req := range requiredFields {
		if !seen[req] {
			errs = append(errs, fmt.Sprintf("missing required key %q", req))
		}
	}

	for field, schema := range arraySchemas {
		if !seen[field] {
			continue
		}
		errs = append(errs, validateArrayField(path, field, schema)...)
	}
	return errs
}

func validateValue(e Entry) string {
	v := Unquote(e.Value)

	if _, ok := arraySchemas[e.Key]; ok {
		// Inline "field: []" is fine. Block form is validated separately
		// so we can report per-item issues.
		if e.Value != "" && e.Value != "[]" {
			return "must be a YAML sequence (or `[]`)"
		}
		return ""
	}

	if e.Value == "" {
		return "missing value"
	}

	// Treat explicit null / ~ as absent. Required fields will still trip the
	// "missing required key" check above; optional fields just pass.
	if v == "null" || v == "~" {
		return ""
	}

	switch e.Key {
	case "iso":
		if !reISO.MatchString(v) {
			return fmt.Sprintf("value %q must be three lowercase letters (ISO 639-3)", v)
		}
	case "iso639_1":
		if !reISO6391.MatchString(v) {
			return fmt.Sprintf("value %q must be two lowercase letters (ISO 639-1)", v)
		}
	case "macrolanguage_id":
		if !reISO.MatchString(v) {
			return fmt.Sprintf("value %q must be three lowercase letters (ISO 639-3)", v)
		}
	case "scope":
		if !oneOf(v, scopeValues) {
			return fmt.Sprintf("value %q not in %v", v, scopeValues)
		}
	case "language_type":
		if !oneOf(v, languageTypeValues) {
			return fmt.Sprintf("value %q not in %v", v, languageTypeValues)
		}
	case "wikidata_id":
		if !reWikidataID.MatchString(v) {
			return fmt.Sprintf("value %q must be a Wikidata QID (e.g. 'Q27811')", v)
		}
	case "name":
		if v == "" {
			return "must be non-empty"
		}
	case "country_id":
		if !reCountryID.MatchString(v) {
			return fmt.Sprintf("value %q must be two uppercase letters (ISO 3166-1 alpha-2)", v)
		}
	case "population":
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return fmt.Sprintf("value %q must be a non-negative integer", v)
		}
	case "latitude":
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < -90 || f > 90 {
			return fmt.Sprintf("value %q must be a number between -90 and 90", v)
		}
	case "longitude":
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < -180 || f > 180 {
			return fmt.Sprintf("value %q must be a number between -180 and 180", v)
		}
	case "glottocode", "glottolog_family_id":
		if !reGlottocode.MatchString(v) {
			return fmt.Sprintf("value %q must match Glottolog ID form (e.g. 'aari1239')", v)
		}
	case "scripts":
		if !reInlineArray.MatchString(e.Value) {
			return "must be a YAML inline array (e.g. [Latn, Cyrl])"
		}
		inner := strings.TrimSpace(strings.Trim(e.Value, "[]"))
		if inner != "" {
			for _, part := range strings.Split(inner, ",") {
				p := strings.TrimSpace(part)
				if !reScriptCode.MatchString(p) {
					return fmt.Sprintf("script code %q must match ISO 15924 (e.g. 'Latn')", p)
				}
			}
		}
	case "alt_names":
		// Permissive shape check only — element strings may contain spaces,
		// parens, commas (when quoted), and non-Latin characters. Element-level
		// rules belong in schema.json, not here.
		t := strings.TrimSpace(e.Value)
		if !strings.HasPrefix(t, "[") || !strings.HasSuffix(t, "]") {
			return "must be a YAML inline array (e.g. [\"Alt Name 1\", \"Alt Name 2\"])"
		}
	case "wikipedia_url":
		if !strings.HasPrefix(v, "https://en.wikipedia.org/wiki/") {
			return fmt.Sprintf("value %q must be an English Wikipedia article URL", v)
		}
	case "status_id":
		for _, s := range statusValues {
			if v == s {
				return ""
			}
		}
		return fmt.Sprintf("value %q not in EGIDS scale %v", v, statusValues)
	}
	return ""
}

// validateArrayField checks each item under a sequence field has the required
// fields and no unknown fields.
func validateArrayField(path, field string, schema arraySchema) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("%s: read: %v", field, err)}
	}
	block, _, err := Split(data)
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", field, err)}
	}
	items, start, _ := ReadArrayField(block, field)
	if start == -1 {
		return nil
	}
	var errs []string
	for i, it := range items {
		for _, req := range schema.required {
			v, has := it.Get(req)
			if !has {
				errs = append(errs, fmt.Sprintf("%s[%d]: missing field %q", field, i, req))
				continue
			}
			if s, ok := v.(string); ok && s == "" {
				errs = append(errs, fmt.Sprintf("%s[%d]: %q is empty", field, i, req))
			}
		}
		for _, k := range it.Keys() {
			if !schema.allowed[k] {
				errs = append(errs, fmt.Sprintf("%s[%d]: unknown field %q", field, i, k))
			}
		}
	}
	return errs
}

// RunValidate walks dir, validates every .md file, and returns a process
// exit code (0 on success, 1 on validation errors, 2 on I/O failure).
func RunValidate(dir string) int {
	paths, err := listMarkdown(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", dir, err)
		return 2
	}
	ok, bad := 0, 0
	for _, p := range paths {
		errs := ValidateFile(p)
		if len(errs) == 0 {
			ok++
			continue
		}
		bad++
		fmt.Println(p)
		for _, e := range errs {
			fmt.Printf("  - %s\n", e)
		}
	}
	fmt.Printf("\n%d ok, %d with errors (of %d total)\n", ok, bad, len(paths))
	if bad > 0 {
		return 1
	}
	return 0
}

func listMarkdown(dir string) ([]string, error) {
	codes, err := ListCodes(dir)
	if err != nil {
		return nil, err
	}
	sort.Strings(codes)
	out := make([]string, len(codes))
	for i, c := range codes {
		out[i] = filepath.Join(dir, c+".md")
	}
	return out, nil
}


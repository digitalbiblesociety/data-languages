package corpus

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Item is one element of a YAML sequence — an ordered map of key→value pairs.
// Insertion order is preserved for stable output.
type Item struct {
	keys   []string
	values map[string]any
}

// NewItem makes an empty item.
func NewItem() *Item { return &Item{values: map[string]any{}} }

// Set adds or replaces a field, preserving insertion order.
func (it *Item) Set(key string, value any) {
	if _, ok := it.values[key]; !ok {
		it.keys = append(it.keys, key)
	}
	it.values[key] = value
}

// Get returns the value for key (or nil, false) if not present.
func (it *Item) Get(key string) (any, bool) {
	v, ok := it.values[key]
	return v, ok
}

// Keys returns the keys in insertion order.
func (it *Item) Keys() []string { return append([]string(nil), it.keys...) }

// Empty reports whether the item has no fields.
func (it *Item) Empty() bool { return len(it.keys) == 0 }

var (
	reSeqHeader = regexp.MustCompile(`^([A-Za-z_][\w]*)\s*:\s*(.*)$`)
	reChildKV   = regexp.MustCompile(`^([A-Za-z_][\w]*)\s*:\s*(.*)$`)
)

// ReadArrayField extracts a top-level sequence field from a frontmatter block.
//
//	items   parsed items in document order
//	start   0-based index of the line introducing the field, or -1 if absent
//	end     0-based index of the last line belonging to the field
func ReadArrayField(block, field string) (items []*Item, start, end int) {
	lines := strings.Split(block, "\n")
	headerRe := regexp.MustCompile(`^` + regexp.QuoteMeta(field) + `\s*:`)
	start, end = -1, -1
	for i, ln := range lines {
		if headerRe.MatchString(ln) {
			start = i
			break
		}
	}
	if start == -1 {
		return nil, -1, -1
	}
	// Inline form ("field: []") — no children to read.
	if m := reSeqHeader.FindStringSubmatch(lines[start]); m != nil && strings.TrimSpace(m[2]) != "" {
		return nil, start, start
	}
	end = start
	var cur *Item
	for i := start + 1; i < len(lines); i++ {
		ln := lines[i]
		if ln == "" {
			end = i
			continue
		}
		// Any non-space at column 0 ends the sequence.
		if !strings.HasPrefix(ln, " ") && !strings.HasPrefix(ln, "\t") {
			break
		}
		trimmed := strings.TrimLeft(ln, " \t")
		switch {
		case strings.HasPrefix(trimmed, "- "):
			if cur != nil && !cur.Empty() {
				items = append(items, cur)
			}
			cur = NewItem()
			if m := reChildKV.FindStringSubmatch(strings.TrimPrefix(trimmed, "- ")); m != nil {
				cur.Set(m[1], parseScalar(m[2]))
			}
		default:
			if cur != nil {
				if m := reChildKV.FindStringSubmatch(trimmed); m != nil {
					cur.Set(m[1], parseScalar(m[2]))
				}
			}
		}
		end = i
	}
	if cur != nil && !cur.Empty() {
		items = append(items, cur)
	}
	return items, start, end
}

// RenderArrayField turns items into the canonical "field:\n  - k: v\n    k2: v2"
// shape. If fieldOrder is non-nil, those keys are emitted first (in order),
// then any remaining keys in insertion order. An empty list renders as
// "field: []".
func RenderArrayField(field string, items []*Item, fieldOrder []string) string {
	if len(items) == 0 {
		return field + ": []"
	}
	var b strings.Builder
	b.WriteString(field)
	b.WriteString(":")
	for _, it := range items {
		keys := orderedKeys(it, fieldOrder)
		first := true
		for _, k := range keys {
			v, ok := it.Get(k)
			if !ok || v == nil {
				continue
			}
			if s, isStr := v.(string); isStr && s == "" {
				continue
			}
			prefix := "    "
			if first {
				prefix = "  - "
			}
			first = false
			b.WriteString("\n")
			b.WriteString(prefix)
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(YAMLString(v))
		}
		if first {
			b.WriteString("\n  - {}")
		}
	}
	return b.String()
}

func orderedKeys(it *Item, order []string) []string {
	if len(order) == 0 {
		return it.Keys()
	}
	seen := map[string]bool{}
	var out []string
	for _, k := range order {
		if _, ok := it.Get(k); ok {
			out = append(out, k)
			seen[k] = true
		}
	}
	for _, k := range it.Keys() {
		if !seen[k] {
			out = append(out, k)
		}
	}
	return out
}

// MergeByKey adds incoming items to existing, keyed by dedupKey. New items are
// appended. Existing items have empty/missing fields filled from incoming;
// non-empty fields are preserved (incoming never overwrites curated data).
func MergeByKey(existing, incoming []*Item, dedupKey string) (out []*Item, changed bool) {
	out = append(out, existing...)
	idx := map[string]int{}
	for i, it := range out {
		if k, ok := stringValue(it, dedupKey); ok {
			idx[k] = i
		}
	}
	for _, in := range incoming {
		k, ok := stringValue(in, dedupKey)
		if !ok {
			continue
		}
		if i, hit := idx[k]; hit {
			touched := false
			for _, field := range in.Keys() {
				newVal, _ := in.Get(field)
				if isEmpty(newVal) {
					continue
				}
				existingVal, has := out[i].Get(field)
				if !has || isEmpty(existingVal) {
					out[i].Set(field, newVal)
					touched = true
				}
			}
			if touched {
				changed = true
			}
		} else {
			out = append(out, in)
			idx[k] = len(out) - 1
			changed = true
		}
	}
	return out, changed
}

func stringValue(it *Item, key string) (string, bool) {
	v, ok := it.Get(key)
	if !ok {
		return "", false
	}
	s, isStr := v.(string)
	if !isStr {
		return fmt.Sprint(v), true
	}
	return s, true
}

func isEmpty(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && s == ""
}

func parseScalar(s string) any {
	s = strings.TrimSpace(s)
	switch s {
	case "", "null", "~":
		return ""
	case "true":
		return true
	case "false":
		return false
	}
	if (strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`)) ||
		(strings.HasPrefix(s, `'`) && strings.HasSuffix(s, `'`)) {
		// Treat single-quoted as if it were double-quoted; the corpus does
		// not currently use escape sequences that would diverge.
		unq, err := strconv.Unquote(`"` + strings.Trim(s, `"'`) + `"`)
		if err == nil {
			return unq
		}
		return strings.Trim(s, `"'`)
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// MergeOptions controls how a writeback merge behaves.
type MergeOptions struct {
	DedupKey   string   // required: which field identifies an item
	FieldOrder []string // optional: canonical field order for emission
	DryRun     bool     // if true, do not write the file
}

// MergeResult reports what happened during MergeFile.
type MergeResult struct {
	Changed bool
	Added   int // count of newly-appended items
}

// MergeFile merges incoming items into the named array field of path's
// frontmatter and writes the file back (unless DryRun).
func MergeFile(path, field string, incoming []*Item, opts MergeOptions) (MergeResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return MergeResult{}, err
	}
	block, body, err := Split(data)
	if err != nil {
		return MergeResult{}, err
	}
	existing, start, end := ReadArrayField(block, field)
	merged, changed := MergeByKey(existing, incoming, opts.DedupKey)
	if !changed {
		return MergeResult{}, nil
	}
	rendered := RenderArrayField(field, merged, opts.FieldOrder)
	lines := strings.Split(block, "\n")
	var newBlock string
	if start == -1 {
		// Append the field to the end of the block.
		newBlock = strings.Join(append(lines, strings.Split(rendered, "\n")...), "\n")
	} else {
		head := lines[:start]
		tail := lines[end+1:]
		newLines := append([]string{}, head...)
		newLines = append(newLines, strings.Split(rendered, "\n")...)
		newLines = append(newLines, tail...)
		newBlock = strings.Join(newLines, "\n")
	}
	res := MergeResult{Changed: true, Added: len(merged) - len(existing)}
	if opts.DryRun {
		return res, nil
	}
	out := "---\n" + strings.TrimRight(newBlock, "\n") + "\n---\n" + body
	return res, os.WriteFile(path, []byte(out), 0o644)
}

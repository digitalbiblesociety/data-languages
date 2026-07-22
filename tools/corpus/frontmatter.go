// Package corpus holds helpers for reading, writing, and validating the
// per-language markdown files under languages/<iso>.md.
//
// The intentional subset of YAML used in these files is:
//   - flat scalars at the top level (iso, name, …)
//   - sequence values written as "- key: value" blocks indented two spaces
//
// Nothing here is a general-purpose YAML parser. It exists because the corpus
// shape is narrow and predictable, and we want the tools to stay stdlib-only.
package corpus

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Entry is one top-level frontmatter key as it appears on disk.
type Entry struct {
	Line  int    // 1-based line number in the file
	Key   string // key text, trimmed
	Value string // raw text after ":" trimmed; "" for sequences
	IsSeq bool   // true if at least one "- " child line follows
}

var reFrontmatter = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n?`)

// Split returns the frontmatter block (between the "---" lines) and the body
// (everything after the closing "---"). Returns an error if no block is found.
func Split(data []byte) (block, body string, err error) {
	m := reFrontmatter.FindSubmatchIndex(data)
	if m == nil {
		return "", "", fmt.Errorf("no frontmatter block found")
	}
	return string(data[m[2]:m[3]]), string(data[m[1]:]), nil
}

// ReadEntries scans top-level keys of a frontmatter block in order.
// Indented child lines belong to the most-recently-seen key and are not
// returned as their own entries.
func ReadEntries(block string) ([]Entry, error) {
	var entries []Entry
	var cur *Entry
	for i, raw := range strings.Split(block, "\n") {
		line := i + 2 // +1 for 0→1 base, +1 for opening "---" line
		if raw == "" {
			continue
		}
		if strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t") {
			if cur != nil {
				cur.IsSeq = cur.IsSeq || strings.HasPrefix(strings.TrimSpace(raw), "- ")
			}
			continue
		}
		colon := strings.Index(raw, ":")
		if colon < 0 {
			return nil, fmt.Errorf("line %d: missing ':'", line)
		}
		k := strings.TrimSpace(raw[:colon])
		v := strings.TrimSpace(raw[colon+1:])
		entries = append(entries, Entry{Line: line, Key: k, Value: v})
		cur = &entries[len(entries)-1]
	}
	return entries, nil
}

// ReadFile loads a file and returns its frontmatter entries.
func ReadFile(path string) ([]Entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _, err := Split(data)
	if err != nil {
		return nil, err
	}
	return ReadEntries(block)
}

// InlineList parses a YAML inline array ("[a, b]") into its elements,
// honouring quotes so quoted elements may contain commas. Returns nil when v
// is not bracketed.
func InlineList(v string) []string {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "[") || !strings.HasSuffix(v, "]") {
		return nil
	}
	inner := v[1 : len(v)-1]
	var out []string
	var cur strings.Builder
	quote := byte(0)
	flush := func() {
		if s := Unquote(strings.TrimSpace(cur.String())); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
			cur.WriteByte(c)
		case c == '"' || c == '\'':
			quote = c
			cur.WriteByte(c)
		case c == ',':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

// Unquote strips matched outer quotes from a scalar value.
func Unquote(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

var (
	reNumber   = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	reReserved = regexp.MustCompile(`^(true|false|null|yes|no|on|off)$`)
)

// YAMLString emits v as a YAML scalar, quoting only when necessary so the file
// stays close to what a human would write. A []string renders inline as
// `[a, b, c]` (or `[]` when empty).
func YAMLString(v any) string {
	if b, ok := v.(bool); ok {
		return strconv.FormatBool(b)
	}
	switch n := v.(type) {
	case int:
		return strconv.Itoa(n)
	case int64:
		return strconv.FormatInt(n, 10)
	case float64:
		return strconv.FormatFloat(n, 'g', -1, 64)
	case []string:
		if len(n) == 0 {
			return "[]"
		}
		parts := make([]string, len(n))
		for i, x := range n {
			parts[i] = YAMLString(x)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	s := fmt.Sprint(v)
	if s == "" {
		return `""`
	}
	if needsQuote(s) {
		return strconv.Quote(s)
	}
	return s
}

func needsQuote(s string) bool {
	if strings.ContainsAny(s, ":#?&*!|>'\"%@`{}[],\n") {
		return true
	}
	if s != strings.TrimSpace(s) {
		return true
	}
	low := strings.ToLower(s)
	if reNumber.MatchString(s) || reReserved.MatchString(low) {
		return true
	}
	switch s[0] {
	case '-':
		return true
	}
	return false
}

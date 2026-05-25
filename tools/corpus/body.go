package corpus

import (
	"fmt"
	"os"
	"strings"
)

// BodyMode controls how SetBody behaves when the file already has body text
// after the closing frontmatter fence.
type BodyMode int

const (
	// BodyIfEmpty writes content only when the existing body is empty
	// (zero bytes or whitespace only).
	BodyIfEmpty BodyMode = iota
	// BodyOverwrite always replaces the existing body with content.
	BodyOverwrite
)

// SetBody writes content as the markdown body of path. The frontmatter block
// is preserved verbatim. Content is normalized to end with exactly one
// trailing newline; an empty content removes any existing body.
//
// Returns (changed=true) when the file would be (or was) modified.
func SetBody(path, content string, mode BodyMode, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	block, body, err := Split(data)
	if err != nil {
		return false, fmt.Errorf("frontmatter: %w", err)
	}

	if mode == BodyIfEmpty && strings.TrimSpace(body) != "" {
		return false, nil
	}

	desired := strings.TrimRight(content, "\n")
	if desired != "" {
		desired += "\n"
	}
	if normalize(body) == desired {
		return false, nil
	}
	if dryRun {
		return true, nil
	}
	out := "---\n" + strings.TrimRight(block, "\n") + "\n---\n" + desired
	return true, os.WriteFile(path, []byte(out), 0o644)
}

// normalize trims trailing whitespace and adds a single trailing newline if
// the result is non-empty.
func normalize(s string) string {
	t := strings.TrimRight(s, " \t\r\n")
	if t == "" {
		return ""
	}
	return t + "\n"
}

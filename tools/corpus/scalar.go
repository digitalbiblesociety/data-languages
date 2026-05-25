package corpus

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// SetMode chooses what SetScalars does when a key already has a value.
type SetMode int

const (
	// Overwrite the existing value unconditionally.
	Overwrite SetMode = iota
	// IfMissing skips the update if the existing value is anything other than
	// empty / null / ~.
	IfMissing
)

// ScalarOp is one field update.
type ScalarOp struct {
	Key   string
	Value any
	Mode  SetMode
}

// SetResult reports what SetScalars did.
type SetResult struct {
	Changed   bool
	Inserted  []string // keys that were not previously present
	Replaced  []string // keys whose values changed
	Unchanged []string // keys whose updates were skipped (IfMissing) or were no-ops
}

// SetScalars applies a batch of scalar-key updates to path's frontmatter while
// preserving the canonical key order defined by CanonicalOrder. Array-valued
// fields (language_links, rolv_dialects, …) are untouched — only the
// scalar lines they live alongside change. Re-runs that produce identical
// values are no-ops.
//
// Each ScalarOp must reference a key in CanonicalOrder; unknown keys are an
// error so typos at the call site fail loudly.
func SetScalars(path string, ops []ScalarOp, dryRun bool) (SetResult, error) {
	res := SetResult{}
	if len(ops) == 0 {
		return res, nil
	}
	canonPos := map[string]int{}
	for i, k := range CanonicalOrder {
		canonPos[k] = i
	}
	for _, op := range ops {
		if _, ok := canonPos[op.Key]; !ok {
			return res, fmt.Errorf("SetScalars: key %q is not in CanonicalOrder", op.Key)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return res, err
	}
	block, body, err := Split(data)
	if err != nil {
		return res, err
	}
	lines := strings.Split(block, "\n")

	// Find each top-level key's first line in `lines` (0-based).
	keyLine := map[string]int{}
	for i, ln := range lines {
		if ln == "" {
			continue
		}
		if strings.HasPrefix(ln, " ") || strings.HasPrefix(ln, "\t") {
			continue
		}
		colon := strings.Index(ln, ":")
		if colon < 0 {
			continue
		}
		k := strings.TrimSpace(ln[:colon])
		if _, dup := keyLine[k]; !dup {
			keyLine[k] = i
		}
	}

	type insertion struct {
		at       int
		canon    int // canonical position, used as tiebreaker when `at` ties
		rendered string
	}
	var insertions []insertion
	newLines := append([]string(nil), lines...)

	for _, op := range ops {
		rendered := op.Key + ": " + YAMLString(op.Value)
		if i, ok := keyLine[op.Key]; ok {
			current := newLines[i]
			if op.Mode == IfMissing {
				colon := strings.Index(current, ":")
				v := ""
				if colon > 0 {
					v = Unquote(strings.TrimSpace(current[colon+1:]))
				}
				if v != "" && v != "null" && v != "~" {
					res.Unchanged = append(res.Unchanged, op.Key)
					continue
				}
			}
			if current == rendered {
				res.Unchanged = append(res.Unchanged, op.Key)
				continue
			}
			newLines[i] = rendered
			res.Replaced = append(res.Replaced, op.Key)
			res.Changed = true
		} else {
			// Pick the lowest-line index of any existing key whose canonical
			// position is greater than ours. Insert before it.
			myPos := canonPos[op.Key]
			at := len(newLines) // default: append before closing
			for k, lineIdx := range keyLine {
				if kp, ok := canonPos[k]; ok && kp > myPos && lineIdx < at {
					at = lineIdx
				}
			}
			insertions = append(insertions, insertion{at: at, canon: myPos, rendered: rendered})
			res.Inserted = append(res.Inserted, op.Key)
			res.Changed = true
		}
	}

	if !res.Changed {
		return res, nil
	}

	// Apply insertions back-to-front so earlier ones don't shift later targets.
	// At a tie, insert canonically-later keys first so they end up at higher
	// indices once the earlier ones are pushed down.
	sort.Slice(insertions, func(i, j int) bool {
		if insertions[i].at != insertions[j].at {
			return insertions[i].at > insertions[j].at
		}
		return insertions[i].canon > insertions[j].canon
	})
	for _, ins := range insertions {
		newLines = append(newLines[:ins.at], append([]string{ins.rendered}, newLines[ins.at:]...)...)
	}

	if dryRun {
		return res, nil
	}
	newBlock := strings.TrimRight(strings.Join(newLines, "\n"), "\n")
	out := "---\n" + newBlock + "\n---\n" + body
	return res, os.WriteFile(path, []byte(out), 0o644)
}

package corpus

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveLanguageFile returns the path to <dir>/<iso>.md if it exists. ISO
// codes with a region suffix ("cmn-Hant") fall back to the base code.
func ResolveLanguageFile(dir, iso string) string {
	iso = strings.ToLower(strings.TrimSpace(iso))
	if iso == "" {
		return ""
	}
	primary := filepath.Join(dir, iso+".md")
	if _, err := os.Stat(primary); err == nil {
		return primary
	}
	if base, _, ok := strings.Cut(iso, "-"); ok && base != "" {
		alt := filepath.Join(dir, base+".md")
		if _, err := os.Stat(alt); err == nil {
			return alt
		}
	}
	return ""
}

// ListCodes returns the ISO codes (sans .md suffix) found in dir, sorted.
func ListCodes(dir string) ([]string, error) {
	dents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var codes []string
	for _, d := range dents {
		n := d.Name()
		if !strings.HasSuffix(n, ".md") {
			continue
		}
		codes = append(codes, strings.TrimSuffix(n, ".md"))
	}
	return codes, nil
}

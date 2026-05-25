package corpus

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const userAgent = "languages-tools (+local)"

// FetchOptions configures a cached HTTP GET.
type FetchOptions struct {
	Name     string            // cache key (one file per source per month)
	URL      string            // URL to GET
	Headers  map[string]string // extra request headers (override defaults)
	Force    bool              // if true, bypass on-disk cache
	Ext      string            // cache-file extension, defaults to "html"
	CacheDir string            // cache directory; defaults to ./.cache next to the binary
}

// FetchResult is the outcome of a FetchCached call.
type FetchResult struct {
	Body   []byte
	Source string // cache file path
	Fresh  bool   // true if this call hit the network
}

var httpClient = &http.Client{Timeout: 60 * time.Second}

// FetchCached returns the body for url, reading from a year-month-keyed cache
// file when possible. The cache layout matches fab-svelte's updates scheme so
// updaters can be ported across repos without re-fetching every source.
func FetchCached(opts FetchOptions) (FetchResult, error) {
	if opts.Name == "" || opts.URL == "" {
		return FetchResult{}, fmt.Errorf("FetchCached: name and url are required")
	}
	ext := opts.Ext
	if ext == "" {
		ext = "html"
	}
	cacheDir := opts.CacheDir
	if cacheDir == "" {
		cacheDir = ".cache"
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return FetchResult{}, err
	}
	cacheFile := filepath.Join(cacheDir, fmt.Sprintf("%s-%s.%s", opts.Name, yearMonth(time.Now().UTC()), ext))

	if !opts.Force {
		if b, err := os.ReadFile(cacheFile); err == nil {
			return FetchResult{Body: b, Source: cacheFile, Fresh: false}, nil
		}
	}

	req, err := http.NewRequest(http.MethodGet, opts.URL, nil)
	if err != nil {
		return FetchResult{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json,*/*")
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return FetchResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return FetchResult{}, fmt.Errorf("GET %s → %d: %s", opts.URL, resp.StatusCode, strings.TrimSpace(string(preview)))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return FetchResult{}, err
	}
	if err := os.WriteFile(cacheFile, body, 0o644); err != nil {
		return FetchResult{}, err
	}
	return FetchResult{Body: body, Source: cacheFile, Fresh: true}, nil
}

func yearMonth(t time.Time) string {
	return fmt.Sprintf("%04d-%02d", t.Year(), int(t.Month()))
}

// StripTags returns the inner text of a small HTML fragment with whitespace
// collapsed. It is not a real HTML parser; it is the same level of helper
// fab-svelte's stripTags provides for scraping list pages.
func StripTags(s string) string {
	var b strings.Builder
	inTag := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '<':
			inTag = true
		case c == '>':
			inTag = false
		case !inTag:
			b.WriteByte(c)
		}
	}
	out := b.String()
	out = strings.ReplaceAll(out, "&nbsp;", " ")
	out = strings.ReplaceAll(out, "&amp;", "&")
	out = strings.ReplaceAll(out, "&quot;", `"`)
	out = strings.ReplaceAll(out, "&#039;", "'")
	out = strings.ReplaceAll(out, "&apos;", "'")
	out = strings.ReplaceAll(out, "&lt;", "<")
	out = strings.ReplaceAll(out, "&gt;", ">")
	return strings.Join(strings.Fields(out), " ")
}

// NormalizeURL upgrades protocol-relative and protocol-bare URLs to https
// and resolves root-relative paths against baseHost. Returns "" for inputs
// that cannot be made absolute.
func NormalizeURL(href, baseHost string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(href, "//"):
		return "https:" + href
	case strings.HasPrefix(href, "http://"):
		return "https://" + strings.TrimPrefix(href, "http://")
	case strings.HasPrefix(href, "https://"):
		return href
	case strings.HasPrefix(href, "/") && baseHost != "":
		return strings.TrimRight(baseHost, "/") + href
	}
	return ""
}

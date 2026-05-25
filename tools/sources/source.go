// Package sources hosts external-data updaters. Each updater is one file
// (e.g. sources/storyrunners.go) that calls Register from its init().
//
// An updater fetches a remote index, parses entries down to a list of
// {iso, link} pairs, and merges them into each language's language_links
// frontmatter array with corpus.MergeFile.
//
// See sources/README.md for the source-skeleton template.
package sources

import "sort"

// Options is the shared CLI surface forwarded to every Source.Run.
type Options struct {
	Dir    string // path to the languages directory (e.g. "languages")
	Only   string // comma-separated subset hint, source-defined meaning
	DryRun bool   // do not write files
	Force  bool   // bypass FetchCached's on-disk cache
}

// Source is one external updater.
type Source interface {
	Name() string
	Run(opts Options) error
}

var registry = map[string]Source{}

// Register adds s to the registry. Sources should call this from init().
// A duplicate name panics so collisions are caught at startup.
func Register(s Source) {
	if _, dup := registry[s.Name()]; dup {
		panic("sources: duplicate registration of " + s.Name())
	}
	registry[s.Name()] = s
}

// All returns every registered source, sorted by name.
func All() []Source {
	out := make([]Source, 0, len(registry))
	for _, s := range registry {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// ByName looks up a registered source.
func ByName(name string) (Source, bool) {
	s, ok := registry[name]
	return s, ok
}

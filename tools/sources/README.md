# tools/sources/

One file per external data source. Each file:

1. Defines a `Source` (a value with `Name()` and `Run(opts)`).
2. Registers itself in `init()`.

The dispatcher (`tools/update.go`) picks them up automatically.

## Patterns

Sources fall into two shapes depending on what they enrich:

- **Scalar enrichment** — fill or set top-level scalar fields (`glottocode`,
  `wikipedia_url`, `scripts`, …). Use `corpus.SetScalars`. See
  `glottolog.go`, `wikipedia.go`, `cldr.go`.
- **Array enrichment** — append items to a top-level sequence field
  (`rolv_dialects`). Use `corpus.MergeFile`. See `rolv.go`.

You can mix both in one source — `wikipedia.go` does scalars *and* body via
`corpus.SetBody`.

## Skeleton — scalar source

```go
// tools/sources/example.go
package sources

import (
	"fmt"

	"languages/tools/corpus"
)

func init() { Register(example{}) }

type example struct{}

func (example) Name() string { return "example" }

func (example) Run(opts Options) error {
	res, err := corpus.FetchCached(corpus.FetchOptions{
		Name:  "example",
		URL:   "https://example.org/data.json",
		Force: opts.Force,
		Ext:   "json",
	})
	if err != nil {
		return err
	}
	fmt.Printf("source: %s (fresh=%v)\n", res.Source, res.Fresh)

	rows := parseExample(res.Body) // returns []{iso, code, label}

	updated, unchanged, missing := 0, 0, 0
	for _, r := range rows {
		path := corpus.ResolveLanguageFile(opts.Dir, r.iso)
		if path == "" {
			missing++
			continue
		}
		result, err := corpus.SetScalars(path, []corpus.ScalarOp{
			{Key: "some_id", Value: r.code, Mode: corpus.Overwrite},
			{Key: "population", Value: r.population, Mode: corpus.IfMissing},
		}, opts.DryRun)
		if err != nil {
			return err
		}
		if result.Changed {
			updated++
		} else {
			unchanged++
		}
	}

	fmt.Printf("updated %d, unchanged %d, missing %d\n", updated, unchanged, missing)
	return nil
}
```

## Skeleton — array source

```go
// tools/sources/example_dialects.go
package sources

import (
	"fmt"

	"languages/tools/corpus"
)

func (exampleDialects) Run(opts Options) error {
	// … fetch & parse …

	fieldOrder := []string{"code", "name", "country_id"}
	for iso, entries := range byISO {
		path := corpus.ResolveLanguageFile(opts.Dir, iso)
		if path == "" {
			continue
		}
		items := make([]*corpus.Item, 0, len(entries))
		for _, e := range entries {
			it := corpus.NewItem()
			it.Set("code", e.code)
			it.Set("name", e.name)
			it.Set("country_id", e.country)
			items = append(items, it)
		}
		_, err := corpus.MergeFile(path, "example_dialects", items,
			corpus.MergeOptions{
				DedupKey:   "code",
				FieldOrder: fieldOrder,
				DryRun:     opts.DryRun,
			})
		if err != nil {
			return err
		}
	}
	return nil
}
```

If you add a new top-level field — scalar or array — also:

1. Append it to the `propertyNames` enum and `properties` block of `schema.json`.
2. Add it to `corpus.CanonicalOrder` in `tools/corpus/validate.go`.
3. For arrays: register the per-item shape in `arraySchemas` in the same file.
4. For scalars with non-trivial shape: add a `case` to `validateValue`.

## Conventions

- **Cache key** matches the file stem (`example.go` → `corpus.FetchOptions.Name="example"`). The cache lives in `.cache/` and rotates monthly.
- **Dedup key** for arrays is whatever identifies an item uniquely (`rolv_code` for ROLV, `url` for link-shaped data).
- **Source-of-truth fields** use `corpus.Overwrite` — re-runs reflect upstream.
- **Gap-fill fields** use `corpus.IfMissing` — never overwrite curated data.
- **Honor `opts.DryRun`, `opts.Force`, `opts.Only`** — forwarded verbatim by the dispatcher.

## Running

```sh
go run ./tools -list                          # show registered sources
go run ./tools -update <name>                 # run one
go run ./tools -update all                    # run every registered source
go run ./tools -update all -dry-run -force    # preview a forced refresh
```

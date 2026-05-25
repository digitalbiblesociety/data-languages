// update is the CLI entrypoint for the languages tooling.
//
// Usage from the repo root:
//
//	go run ./tools -validate                  # validate every file under languages/
//	go run ./tools -list                      # list registered sources
//	go run ./tools -update <name>             # run a single source
//	go run ./tools -update all                # run all sources
//	go run ./tools -update all -dry-run       # forward -dry-run to each source
//	go run ./tools -update all -force         # bypass each source's HTTP cache
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"languages/tools/corpus"
	"languages/tools/sources"
)

const defaultDir = "languages"

func main() {
	var (
		dir      = flag.String("dir", defaultDir, "directory holding <iso>.md files")
		validate = flag.Bool("validate", false, "validate frontmatter against the schema")
		list     = flag.Bool("list", false, "list registered sources and exit")
		update   = flag.String("update", "", "run a source by name, or 'all' for every registered source")
		only     = flag.String("only", "", "forwarded to sources: comma-separated subset")
		dryRun   = flag.Bool("dry-run", false, "preview without writing")
		force    = flag.Bool("force", false, "forwarded to sources: bypass HTTP cache")
		bail     = flag.Bool("bail", false, "stop after the first failing source")
	)
	flag.Parse()

	switch {
	case *list:
		listSources()
	case *validate:
		os.Exit(corpus.RunValidate(*dir))
	case *update != "":
		os.Exit(runUpdate(*dir, *update, *only, *dryRun, *force, *bail))
	default:
		fmt.Fprintln(os.Stderr, "nothing to do — pass -validate, -list, or -update <name>")
		flag.Usage()
		os.Exit(2)
	}
}

func listSources() {
	all := sources.All()
	if len(all) == 0 {
		fmt.Println("no sources registered — add one under tools/sources/ (see sources/README.md)")
		return
	}
	fmt.Printf("%d source(s):\n", len(all))
	for _, s := range all {
		fmt.Printf("  - %s\n", s.Name())
	}
}

func runUpdate(dir, name, only string, dryRun, force, bail bool) int {
	all := sources.All()
	if len(all) == 0 {
		fmt.Println("no sources registered yet — add one under tools/sources/")
		return 0
	}

	var run []sources.Source
	switch name {
	case "all":
		run = all
	default:
		if s, ok := sources.ByName(name); ok {
			run = []sources.Source{s}
		} else {
			fmt.Fprintf(os.Stderr, "unknown source %q\n", name)
			fmt.Fprintf(os.Stderr, "available: %s\n", strings.Join(sourceNames(all), ", "))
			return 2
		}
	}

	opts := sources.Options{Dir: dir, Only: only, DryRun: dryRun, Force: force}
	failed := 0
	for _, s := range run {
		fmt.Printf("\n── %s ──\n", s.Name())
		if err := s.Run(opts); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", s.Name(), err)
			failed++
			if bail {
				break
			}
		}
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func sourceNames(ss []sources.Source) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Name()
	}
	return out
}

// rolv enriches each language file with entries from GRN's Registry of
// Language Varieties (ROLV).
//
// Source:    https://hisregistries.org/rolv/
// Endpoint:  GraphQL at https://gql.globalrecordings.net/graphql
// Cadence:   monthly cache via corpus.FetchCached
//
// One ISO 639-3 code can have many ROLV varieties (max 81 in current data —
// "nst" / Tase Naga). Each variety becomes one item in `rolv_dialects`.
// Re-runs are idempotent: rolv_code is the dedup key.
package sources

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"

	"languages/tools/corpus"
)

func init() { Register(rolv{}) }

type rolv struct{}

func (rolv) Name() string { return "rolv" }

const (
	rolvHost  = "https://gql.globalrecordings.net/graphql"
	rolvQuery = `{ROLVCodes{LanguageCode,LanguageName,ROLVCode,LanguageTag,VarietyName,CountryCode,LocationName}}`
)

type rolvRow struct {
	LanguageCode string `json:"LanguageCode"`
	LanguageName string `json:"LanguageName"`
	ROLVCode     int    `json:"ROLVCode"`
	LanguageTag  string `json:"LanguageTag"`
	VarietyName  string `json:"VarietyName"`
	CountryCode  string `json:"CountryCode"`
	LocationName string `json:"LocationName"`
}

type rolvResponse struct {
	Data struct {
		ROLVCodes []rolvRow `json:"ROLVCodes"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors,omitempty"`
}

func (rolv) Run(opts Options) error {
	res, err := corpus.FetchCached(corpus.FetchOptions{
		Name:  "rolv",
		URL:   rolvHost + "?query=" + url.QueryEscape(rolvQuery),
		Force: opts.Force,
		Ext:   "json",
	})
	if err != nil {
		return err
	}
	fmt.Printf("source: %s", res.Source)
	if res.Fresh {
		fmt.Print(" (fetched)")
	} else {
		fmt.Print(" (cached)")
	}
	fmt.Println()

	var doc rolvResponse
	if err := json.Unmarshal(res.Body, &doc); err != nil {
		return fmt.Errorf("parse json: %w", err)
	}
	if len(doc.Errors) > 0 {
		return fmt.Errorf("graphql: %s", doc.Errors[0].Message)
	}
	rows := doc.Data.ROLVCodes
	fmt.Printf("rows: %d\n", len(rows))

	byISO := map[string][]rolvRow{}
	for _, r := range rows {
		if r.LanguageCode == "" || r.ROLVCode == 0 {
			continue
		}
		byISO[r.LanguageCode] = append(byISO[r.LanguageCode], r)
	}

	isos := make([]string, 0, len(byISO))
	for iso := range byISO {
		isos = append(isos, iso)
	}
	sort.Strings(isos)

	fieldOrder := []string{"rolv_code", "language_tag", "name", "country_id", "location"}

	updated, unchanged, missing := 0, 0, 0
	var missingISOs []string
	for _, iso := range isos {
		path := corpus.ResolveLanguageFile(opts.Dir, iso)
		if path == "" {
			missing++
			missingISOs = append(missingISOs, iso)
			continue
		}
		group := byISO[iso]
		sort.Slice(group, func(i, j int) bool { return group[i].ROLVCode < group[j].ROLVCode })

		items := make([]*corpus.Item, 0, len(group))
		for _, r := range group {
			it := corpus.NewItem()
			it.Set("rolv_code", r.ROLVCode)
			if r.LanguageTag != "" {
				it.Set("language_tag", r.LanguageTag)
			}
			it.Set("name", r.VarietyName)
			if r.CountryCode != "" {
				it.Set("country_id", r.CountryCode)
			}
			if r.LocationName != "" {
				it.Set("location", r.LocationName)
			}
			items = append(items, it)
		}

		result, err := corpus.MergeFile(path, "rolv_dialects", items, corpus.MergeOptions{
			DedupKey:   "rolv_code",
			FieldOrder: fieldOrder,
			DryRun:     opts.DryRun,
		})
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if result.Changed {
			updated++
		} else {
			unchanged++
		}
	}

	fmt.Printf("updated: %d\nunchanged: %d\nmissing iso (no .md file): %d", updated, unchanged, missing)
	if n := len(missingISOs); n > 0 {
		show := missingISOs
		if n > 10 {
			show = missingISOs[:10]
		}
		fmt.Printf(" — %v", show)
		if n > 10 {
			fmt.Printf(" … (and %d more)", n-10)
		}
	}
	fmt.Println()
	if opts.DryRun {
		fmt.Println("(dry run — no files written)")
	}
	return nil
}

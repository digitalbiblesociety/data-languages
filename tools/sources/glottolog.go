// glottolog enriches each language file with Glottolog 5.x metadata:
//
//   - glottocode               (Glottolog languoid ID)
//   - glottolog_family_id      (top-level family ID)
//   - glottolog_family_name    (human-readable family name)
//   - glottolog_classification (top-down genealogical path)
//
// When latitude / longitude / country_id are absent or null on the local
// file, Glottolog's values are filled in. Curated values are never
// overwritten.
//
// Source:    https://glottolog.org/meta/downloads
// Endpoint:  zipped CSV at cdstar.eva.mpg.de
// Cadence:   monthly cache via corpus.FetchCached
//
// Glottolog publishes ~27k languoids (families + languages + dialects). We
// key on iso639P3code; ~8.2k rows carry an ISO code that maps to our corpus.
package sources

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"languages/tools/corpus"
)

func init() { Register(glottolog{}) }

type glottolog struct{}

func (glottolog) Name() string { return "glottolog" }

const glottologURL = "https://cdstar.eva.mpg.de//bitstreams/EAEA0-608B-9919-A962-0/glottolog_languoid.csv.zip"

// row mirrors one languoid in the Glottolog CSV. Only the fields we use are
// captured; CSV column order is fixed via the header lookup below.
type row struct {
	ID             string
	FamilyID       string
	ParentID       string
	Name           string
	Bookkeeping    bool
	Level          string // language | dialect | family
	Latitude       string
	Longitude      string
	ISO            string
	CountryIDs     string // comma-separated ISO 3166-1 alpha-2
}

func (glottolog) Run(opts Options) error {
	res, err := corpus.FetchCached(corpus.FetchOptions{
		Name:  "glottolog",
		URL:   glottologURL,
		Force: opts.Force,
		Ext:   "zip",
	})
	if err != nil {
		return err
	}
	fmt.Printf("source: %s", res.Source)
	if res.Fresh {
		fmt.Println(" (fetched)")
	} else {
		fmt.Println(" (cached)")
	}

	rows, err := parseGlottologZip(res.Body)
	if err != nil {
		return err
	}
	fmt.Printf("rows: %d total\n", len(rows))

	byID := make(map[string]*row, len(rows))
	for i := range rows {
		byID[rows[i].ID] = &rows[i]
	}

	updated, unchanged, missing := 0, 0, 0
	var missingISOs []string
	languageRows := 0
	for i := range rows {
		r := &rows[i]
		if r.ISO == "" || r.Bookkeeping {
			continue
		}
		// Only "language"-level rows have a 1:1 relationship with ISO 639-3.
		// Some dialects and a handful of families also carry an ISO code,
		// usually for legacy reasons — skip them so we don't enrich one ISO
		// file with multiple Glottolog identities.
		if r.Level != "language" {
			continue
		}
		languageRows++

		path := corpus.ResolveLanguageFile(opts.Dir, r.ISO)
		if path == "" {
			missing++
			missingISOs = append(missingISOs, r.ISO)
			continue
		}

		familyName := ""
		classification := ""
		if r.FamilyID != "" {
			if fam, ok := byID[r.FamilyID]; ok {
				familyName = fam.Name
			}
			classification = classificationPath(r, byID)
		}

		ops := []corpus.ScalarOp{
			{Key: "glottocode", Value: r.ID, Mode: corpus.Overwrite},
		}
		if r.FamilyID != "" {
			ops = append(ops, corpus.ScalarOp{Key: "glottolog_family_id", Value: r.FamilyID, Mode: corpus.Overwrite})
		}
		if familyName != "" {
			ops = append(ops, corpus.ScalarOp{Key: "glottolog_family_name", Value: familyName, Mode: corpus.Overwrite})
		}
		if classification != "" {
			ops = append(ops, corpus.ScalarOp{Key: "glottolog_classification", Value: classification, Mode: corpus.Overwrite})
		}
		if lat, ok := parseFloat(r.Latitude); ok {
			ops = append(ops, corpus.ScalarOp{Key: "latitude", Value: lat, Mode: corpus.IfMissing})
		}
		if lon, ok := parseFloat(r.Longitude); ok {
			ops = append(ops, corpus.ScalarOp{Key: "longitude", Value: lon, Mode: corpus.IfMissing})
		}
		if cid := firstCountry(r.CountryIDs); cid != "" {
			ops = append(ops, corpus.ScalarOp{Key: "country_id", Value: cid, Mode: corpus.IfMissing})
		}

		result, err := corpus.SetScalars(path, ops, opts.DryRun)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if result.Changed {
			updated++
		} else {
			unchanged++
		}
	}

	fmt.Printf("language-level rows with ISO: %d\n", languageRows)
	fmt.Printf("updated: %d\nunchanged: %d\nmissing iso (no .md file): %d", updated, unchanged, missing)
	if n := len(missingISOs); n > 0 {
		sort.Strings(missingISOs)
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

// classificationPath walks parent_id from the languoid up to the family and
// returns the family→leaf path joined with " > ". The leaf itself is
// included.
func classificationPath(leaf *row, byID map[string]*row) string {
	var chain []string
	seen := map[string]bool{}
	cur := leaf
	for cur != nil && !seen[cur.ID] {
		seen[cur.ID] = true
		chain = append(chain, cur.Name)
		if cur.ParentID == "" || cur.ParentID == cur.ID {
			break
		}
		cur = byID[cur.ParentID]
	}
	// chain is leaf → family; reverse.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return strings.Join(chain, " > ")
}

func firstCountry(s string) string {
	if s == "" {
		return ""
	}
	parts := strings.Split(s, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if len(p) == 2 {
			return strings.ToUpper(p)
		}
	}
	return ""
}

func parseFloat(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func parseGlottologZip(body []byte) ([]row, error) {
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	var csvFile *zip.File
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, ".csv") {
			csvFile = f
			break
		}
	}
	if csvFile == nil {
		return nil, fmt.Errorf("no .csv inside zip")
	}
	rc, err := csvFile.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}

	reader := csv.NewReader(bytes.NewReader(data))
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[h] = i
	}
	required := []string{"id", "family_id", "parent_id", "name", "bookkeeping", "level", "latitude", "longitude", "iso639P3code", "country_ids"}
	for _, r := range required {
		if _, ok := col[r]; !ok {
			return nil, fmt.Errorf("missing column %q in CSV header (got %v)", r, header)
		}
	}

	var out []row
	for {
		rec, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		out = append(out, row{
			ID:          rec[col["id"]],
			FamilyID:    rec[col["family_id"]],
			ParentID:    rec[col["parent_id"]],
			Name:        rec[col["name"]],
			Bookkeeping: strings.EqualFold(rec[col["bookkeeping"]], "true"),
			Level:       rec[col["level"]],
			Latitude:    rec[col["latitude"]],
			Longitude:   rec[col["longitude"]],
			ISO:         rec[col["iso639P3code"]],
			CountryIDs:  rec[col["country_ids"]],
		})
	}
	return out, nil
}

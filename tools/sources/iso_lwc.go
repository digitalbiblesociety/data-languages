// iso_lwc sets each living language's `iso_lwc` field: the ISO 639-3 code of a
// practical language of wider communication (LWC / lingua franca) a speaker of
// that language is most likely to fall back to. It is a Go port of the
// standalone tools/iso6393_to_lwc_package/build_lwc_mapping.py, reworked to fit
// this repo.
//
// Method (per living language):
//
//  1. Resolve a likely territory, in priority order:
//     a. the corpus's own curated `country_id` (authoritative — covers ~99.9%
//     of living languages here, so the Python's heavy name-inference
//     machinery is almost entirely moot);
//     b. a curated language→core-territory table (for the handful lacking a
//     country_id, e.g. Norwegian);
//     c. Unicode CLDR likely-subtags;
//     d. an explicit country adjective in the English name (curated table).
//  2. Pick the territory's primary LWC, in priority order:
//     a. a curated country→LWC override table (sociolinguistically important
//     cases, e.g. DZ→arb, CH→deu, SG→eng);
//     b. the top-ranked CLDR territoryInfo language for that territory, ranked
//     by official status then population percentage.
//
// The result is Overwritten on re-runs (it is derived, not curated). Non-living
// code elements (ancient/constructed/extinct/historical/special) get no LWC.
//
// This is NOT an official ISO relationship. LWC choice is sociolinguistic and
// varies by community, domain, generation, and education; treat it as a default
// fallback only.
//
// Sources (cached monthly):
//
//	https://raw.githubusercontent.com/unicode-org/cldr-json/.../territoryInfo.json
//	https://raw.githubusercontent.com/unicode-org/cldr-json/.../likelySubtags.json
//
// Honors --only iso[,iso,...] to scope a test run.
package sources

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"languages/tools/corpus"
)

func init() { Register(isoLWC{}) }

type isoLWC struct{}

func (isoLWC) Name() string { return "iso_lwc" }

const (
	cldrTerritoryInfoURL = "https://raw.githubusercontent.com/unicode-org/cldr-json/main/cldr-json/cldr-core/supplemental/territoryInfo.json"
	cldrLikelySubtagsURL = "https://raw.githubusercontent.com/unicode-org/cldr-json/main/cldr-json/cldr-core/supplemental/likelySubtags.json"
)

func (isoLWC) Run(opts Options) error {
	terr, terrSrc, err := fetchTerritoryInfo(opts.Force)
	if err != nil {
		return err
	}
	likely, likelySrc, err := fetchLikelySubtags(opts.Force)
	if err != nil {
		return err
	}
	fmt.Printf("territoryInfo: %s\nlikelySubtags: %s\n", terrSrc, likelySrc)

	all, err := corpus.ListCodes(opts.Dir)
	if err != nil {
		return err
	}
	sort.Strings(all)
	validISO := make(map[string]bool, len(all))
	for _, c := range all {
		validISO[c] = true
	}

	// Precompute each territory's CLDR-derived primary language once.
	territoryPrimary := computeTerritoryPrimary(terr, validISO)

	codes := onlyFilter(append([]string(nil), all...), opts.Only)

	var (
		updated, unchanged, skipped, unmapped int
		selfMaps                              int
		methodCount                           = map[string]int{}
		unmappedList                          []string
	)
	for _, iso := range codes {
		path := filepath.Join(opts.Dir, iso+".md")
		m, err := readLangMeta(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if m.langType != "living" {
			skipped++
			continue
		}
		region, method := resolveTerritory(iso, m, likely)
		lwc := ""
		if region != "" {
			lwc = lwcForTerritory(region, territoryPrimary, validISO)
		}
		if lwc == "" {
			unmapped++
			unmappedList = append(unmappedList, iso)
			continue
		}
		methodCount[method]++
		if lwc == iso {
			selfMaps++
		}
		r, err := corpus.SetScalars(path, []corpus.ScalarOp{
			{Key: "iso_lwc", Value: lwc, Mode: corpus.Overwrite},
		}, opts.DryRun)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if r.Changed {
			updated++
		} else {
			unchanged++
		}
	}

	fmt.Printf("updated: %d\nunchanged: %d\nskipped (non-living): %d\nunmapped living: %d\n",
		updated, unchanged, skipped, unmapped)
	fmt.Printf("self-maps (language is itself the territory LWC): %d\n", selfMaps)
	fmt.Println("territory method breakdown:")
	for _, k := range []string{"country_id", "core_territory", "likely_subtag", "name_adjective"} {
		if methodCount[k] > 0 {
			fmt.Printf("  %-14s %d\n", k, methodCount[k])
		}
	}
	if n := len(unmappedList); n > 0 {
		show := unmappedList
		if n > 20 {
			show = unmappedList[:20]
		}
		fmt.Printf("unmapped living isos: %v", show)
		if n > 20 {
			fmt.Printf(" … (and %d more)", n-20)
		}
		fmt.Println()
	}
	if opts.DryRun {
		fmt.Println("(dry run — no files written)")
	}
	return nil
}

// langMeta is the subset of a language file this source reads.
type langMeta struct {
	countryID string
	name      string
	langType  string
	iso6391   string
}

func readLangMeta(path string) (langMeta, error) {
	entries, err := corpus.ReadFile(path)
	if err != nil {
		return langMeta{}, err
	}
	var m langMeta
	for _, e := range entries {
		v := corpus.Unquote(e.Value)
		if v == "null" || v == "~" {
			v = ""
		}
		switch e.Key {
		case "country_id":
			m.countryID = v
		case "name":
			m.name = v
		case "language_type":
			m.langType = v
		case "iso639_1":
			m.iso6391 = v
		}
	}
	return m, nil
}

// resolveTerritory returns a likely ISO 3166-1 alpha-2 territory for a language
// and the method that produced it. Returns ("", "") when nothing resolves.
func resolveTerritory(iso string, m langMeta, likely map[string]string) (region, method string) {
	if reCountryCode.MatchString(m.countryID) {
		return m.countryID, "country_id"
	}
	if t, ok := coreTerritory[iso]; ok {
		return t, "core_territory"
	}
	for _, key := range []string{m.iso6391, iso} {
		if key == "" {
			continue
		}
		if tag, ok := likely[key]; ok {
			if r := parseRegion(tag); r != "" {
				return r, "likely_subtag"
			}
		}
	}
	if r := inferTerritoryFromName(m.name); r != "" {
		return r, "name_adjective"
	}
	return "", ""
}

// lwcForTerritory picks the primary LWC for a territory: the curated override
// when present and valid, otherwise the CLDR-derived primary.
func lwcForTerritory(region string, territoryPrimary map[string]string, validISO map[string]bool) string {
	if p, ok := overridePrimary[region]; ok && validISO[p] {
		return p
	}
	return territoryPrimary[region]
}

// computeTerritoryPrimary maps each territory to its top language, ranking by
// official status then population percentage. National (official / de-facto
// official) languages win over merely-regional ones; ties break alphabetically
// for determinism.
func computeTerritoryPrimary(terr map[string]cldrTerritory, validISO map[string]bool) map[string]string {
	out := make(map[string]string, len(terr))
	for region, info := range terr {
		type ranked struct {
			rank int
			pct  float64
			code string
		}
		var all, national []ranked
		for tag, lp := range info.LanguagePopulation {
			code := iso3ForTag(tag)
			if code == "" || !validISO[code] {
				continue
			}
			pct, _ := strconv.ParseFloat(lp.PopulationPercent, 64)
			r := ranked{statusRank(lp.OfficialStatus), pct, code}
			all = append(all, r)
			if r.rank >= 3 {
				national = append(national, r)
			}
		}
		pool := national
		if len(pool) == 0 {
			pool = all
		}
		if len(pool) == 0 {
			continue
		}
		sort.Slice(pool, func(i, j int) bool {
			a, b := pool[i], pool[j]
			if a.rank != b.rank {
				return a.rank > b.rank
			}
			if a.pct != b.pct {
				return a.pct > b.pct
			}
			return a.code < b.code
		})
		out[region] = pool[0].code
	}
	return out
}

func statusRank(s string) int {
	switch s {
	case "official":
		return 4
	case "de_facto_official":
		return 3
	case "official_regional":
		return 2
	case "official_minority":
		return 1
	default:
		return 0
	}
}

// iso3ForTag normalizes a CLDR language tag (2-letter, 3-letter, or a
// macrolanguage that should resolve to its standard variety) to ISO 639-3.
// Script/region suffixes ("sd_Deva", "iu-Latn") are stripped. Returns "" for
// tags it cannot resolve.
func iso3ForTag(tag string) string {
	if tag == "" {
		return ""
	}
	base := tag
	if i := strings.IndexAny(base, "-_"); i >= 0 {
		base = base[:i]
	}
	if v, ok := cldrToISO3[base]; ok {
		return v
	}
	switch len(base) {
	case 3:
		return base
	case 2:
		if v, ok := iso639_1to3[base]; ok {
			return v
		}
	}
	return ""
}

var (
	reCountryCode = regexp.MustCompile(`^[A-Z]{2}$`)
	reTagSplit    = regexp.MustCompile(`[-_]`)
	reNonAlnum    = regexp.MustCompile(`[^a-z0-9 ]+`)
	reWhitespace  = regexp.MustCompile(`\s+`)
)

// parseRegion returns the last 2-uppercase-letter subtag of a language tag
// (the region), skipping the primary language subtag. "" when none.
func parseRegion(tag string) string {
	parts := reTagSplit.Split(tag, -1)
	for i := len(parts) - 1; i >= 1; i-- {
		if reCountryCode.MatchString(parts[i]) {
			return parts[i]
		}
	}
	return ""
}

// adjectivePhrases holds normalized country-adjective labels, longest first, so
// "papua new guinea" is tried before "guinea".
type adjPhrase struct {
	phrase string
	region string
}

var adjectivePhrases []adjPhrase

func init() {
	for label, region := range adjectiveTerritory {
		if p := normPhrase(label); p != "" {
			adjectivePhrases = append(adjectivePhrases, adjPhrase{p, region})
		}
	}
	sort.Slice(adjectivePhrases, func(i, j int) bool {
		if len(adjectivePhrases[i].phrase) != len(adjectivePhrases[j].phrase) {
			return len(adjectivePhrases[i].phrase) > len(adjectivePhrases[j].phrase)
		}
		return adjectivePhrases[i].phrase < adjectivePhrases[j].phrase
	})
}

func normPhrase(s string) string {
	s = strings.ToLower(s)
	s = reNonAlnum.ReplaceAllString(s, " ")
	s = reWhitespace.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// inferTerritoryFromName returns a territory when the English name contains a
// known country adjective (e.g. "Norwegian Sign Language" → NO). This is a
// last-resort, low-confidence signal; here it fires only for the few living
// languages that lack a curated country_id and are absent from CLDR.
func inferTerritoryFromName(name string) string {
	n := normPhrase(name)
	if n == "" {
		return ""
	}
	padded := " " + n + " "
	for _, p := range adjectivePhrases {
		if strings.Contains(padded, " "+p.phrase+" ") {
			return p.region
		}
	}
	return ""
}

// --- CLDR fetch + parse ---------------------------------------------------

type cldrTerritoryInfoDoc struct {
	Supplemental struct {
		TerritoryInfo map[string]cldrTerritory `json:"territoryInfo"`
	} `json:"supplemental"`
}

type cldrTerritory struct {
	LanguagePopulation map[string]cldrLangPop `json:"languagePopulation"`
}

type cldrLangPop struct {
	OfficialStatus    string `json:"_officialStatus"`
	PopulationPercent string `json:"_populationPercent"`
}

func fetchTerritoryInfo(force bool) (map[string]cldrTerritory, string, error) {
	res, err := corpus.FetchCached(corpus.FetchOptions{
		Name: "cldr-territoryinfo", URL: cldrTerritoryInfoURL, Force: force, Ext: "json",
	})
	if err != nil {
		return nil, "", err
	}
	var doc cldrTerritoryInfoDoc
	if err := json.Unmarshal(res.Body, &doc); err != nil {
		return nil, "", fmt.Errorf("parse territoryInfo: %w", err)
	}
	return doc.Supplemental.TerritoryInfo, sourceLabel(res), nil
}

type cldrLikelySubtagsDoc struct {
	Supplemental struct {
		LikelySubtags map[string]string `json:"likelySubtags"`
	} `json:"supplemental"`
}

func fetchLikelySubtags(force bool) (map[string]string, string, error) {
	res, err := corpus.FetchCached(corpus.FetchOptions{
		Name: "cldr-likelysubtags", URL: cldrLikelySubtagsURL, Force: force, Ext: "json",
	})
	if err != nil {
		return nil, "", err
	}
	var doc cldrLikelySubtagsDoc
	if err := json.Unmarshal(res.Body, &doc); err != nil {
		return nil, "", fmt.Errorf("parse likelySubtags: %w", err)
	}
	return doc.Supplemental.LikelySubtags, sourceLabel(res), nil
}

func sourceLabel(res corpus.FetchResult) string {
	if res.Fresh {
		return res.Source + " (fetched)"
	}
	return res.Source + " (cached)"
}

// --- Curated tables (ported from build_lwc_mapping.py) --------------------

// cldrToISO3 maps broad CLDR language tags to the ISO 639-3 standard variety,
// so a macrolanguage tag resolves to the practical written/spoken standard.
var cldrToISO3 = map[string]string{
	"ar": "arb", // Standard Arabic
	"zh": "cmn", // Mandarin / Standard Chinese
	"fa": "pes", // Iranian Persian
	"ms": "zsm", // Standard Malay
	"sw": "swh", // Swahili (individual language)
	"sq": "sqi", // Albanian (Tosk-based standard)
	"ps": "pus", // Pashto
	"ku": "kur", // Kurdish
	"no": "nob",
	"nb": "nob",
	"nn": "nno",
	"tl": "tgl",
}

// overridePrimary maps a territory to its curated primary LWC (ISO 639-3).
// These select the practical national/regional language of wider communication
// and are not claims that every speaker uses it.
var overridePrimary = map[string]string{
	// Africa
	"DZ": "arb", "AO": "por", "BJ": "fra", "BW": "eng", "BF": "fra", "BI": "run",
	"CV": "por", "CM": "fra", "CF": "sag", "TD": "fra", "KM": "arb", "CG": "fra",
	"CD": "fra", "CI": "fra", "DJ": "fra", "EG": "arb", "GQ": "spa", "ER": "tir",
	"SZ": "eng", "ET": "amh", "GA": "fra", "GM": "eng", "GH": "eng", "GN": "fra",
	"GW": "por", "KE": "swh", "LS": "sot", "LR": "eng", "LY": "arb", "MG": "mlg",
	"MW": "nya", "ML": "bam", "MR": "arb", "MU": "mfe", "MA": "arb", "MZ": "por",
	"NA": "eng", "NE": "fra", "NG": "eng", "RW": "kin", "ST": "por", "SN": "fra",
	"SC": "crs", "SL": "kri", "SO": "som", "ZA": "eng", "SS": "eng", "SD": "arb",
	"TZ": "swh", "TG": "fra", "TN": "arb", "UG": "eng", "EH": "arb", "ZM": "eng",
	"ZW": "eng",
	// Americas and Caribbean
	"AR": "spa", "BO": "spa", "BR": "por", "BZ": "eng", "CA": "eng", "CL": "spa",
	"CO": "spa", "CR": "spa", "CU": "spa", "DO": "spa", "EC": "spa", "SV": "spa",
	"GF": "fra", "GT": "spa", "GY": "eng", "HT": "hat", "HN": "spa", "MX": "spa",
	"NI": "spa", "PA": "spa", "PY": "spa", "PE": "spa", "SR": "nld", "US": "eng",
	"UY": "spa", "VE": "spa", "PR": "spa", "AW": "pap", "CW": "pap", "BQ": "pap",
	"SX": "eng", "JM": "eng", "TT": "eng", "BB": "eng", "BS": "eng", "GD": "eng",
	"LC": "eng", "VC": "eng", "AG": "eng", "DM": "eng", "KN": "eng", "GL": "kal",
	// Asia and Middle East
	"AF": "prs", "AM": "hye", "AZ": "aze", "BH": "arb", "BD": "ben", "BT": "dzo",
	"BN": "zsm", "KH": "khm", "CN": "cmn", "CY": "ell", "GE": "kat", "HK": "yue",
	"IN": "hin", "ID": "ind", "IR": "pes", "IQ": "arb", "IL": "heb", "JP": "jpn",
	"JO": "arb", "KZ": "kaz", "KP": "kor", "KR": "kor", "KW": "arb", "KG": "kir",
	"LA": "lao", "LB": "arb", "MO": "yue", "MY": "zsm", "MV": "div", "MN": "mon",
	"MM": "mya", "NP": "nep", "OM": "arb", "PK": "urd", "PS": "arb", "PH": "fil",
	"QA": "arb", "SA": "arb", "SG": "eng", "LK": "sin", "SY": "arb", "TW": "cmn",
	"TJ": "tgk", "TH": "tha", "TL": "tet", "TR": "tur", "TM": "tuk", "AE": "arb",
	"UZ": "uzn", "VN": "vie", "YE": "arb",
	// Europe
	"AL": "sqi", "AD": "cat", "AT": "deu", "BE": "nld", "BA": "bos", "BG": "bul",
	"BY": "bel", "CH": "deu", "CZ": "ces", "DE": "deu", "DK": "dan", "EE": "est",
	"ES": "spa", "FI": "fin", "FR": "fra", "GB": "eng", "GR": "ell", "HR": "hrv",
	"HU": "hun", "IE": "eng", "IS": "isl", "IT": "ita", "LI": "deu", "LT": "lit",
	"LU": "ltz", "LV": "lav", "MC": "fra", "MD": "ron", "ME": "cnr", "MK": "mkd",
	"MT": "mlt", "NL": "nld", "NO": "nob", "PL": "pol", "PT": "por", "RO": "ron",
	"RS": "srp", "RU": "rus", "SE": "swe", "SI": "slv", "SK": "slk", "SM": "ita",
	"UA": "ukr", "VA": "ita", "XK": "sqi",
	// Oceania
	"AU": "eng", "FJ": "fij", "FM": "eng", "KI": "gil", "MH": "mah", "NR": "nau",
	"NZ": "eng", "PG": "tpi", "PW": "pau", "SB": "pis", "TO": "ton", "TV": "tvl",
	"VU": "bis", "WS": "smo", "NC": "fra", "PF": "fra", "WF": "fra", "GU": "eng",
	"MP": "eng", "AS": "smo", "CK": "rar", "NU": "niu", "TK": "tkl",
}

// coreTerritory maps a language (ISO 639-3) to its core territory, used only
// when the corpus carries no country_id for that language.
var coreTerritory = map[string]string{
	"eng": "US", "fra": "FR", "spa": "ES", "por": "PT", "rus": "RU", "arb": "EG",
	"cmn": "CN", "hin": "IN", "urd": "PK", "ben": "BD", "swh": "TZ", "ind": "ID",
	"zsm": "MY", "hau": "NG", "pes": "IR", "prs": "AF", "tur": "TR", "jpn": "JP",
	"kor": "KR", "deu": "DE", "nld": "NL", "ita": "IT", "pol": "PL", "ukr": "UA",
	"ron": "RO", "tha": "TH", "vie": "VN", "fil": "PH", "amh": "ET", "gaz": "ET",
	"som": "SO", "mlg": "MG", "sag": "CF", "tpi": "PG", "bis": "VU", "pis": "SB",
	"kri": "SL", "hat": "HT", "mfe": "MU", "bam": "ML", "dyu": "BF", "lin": "CD",
	"wol": "SN", "tet": "TL", "srn": "SR", "pap": "AW", "kin": "RW", "kaz": "KZ",
	"uzn": "UZ", "tgk": "TJ", "kir": "KG", "mon": "MN", "mya": "MM", "nep": "NP",
	"sin": "LK", "tam": "IN", "heb": "IL", "ell": "GR", "sqi": "AL", "bos": "BA",
	"hrv": "HR", "srp": "RS", "cat": "ES", "eus": "ES", "glg": "ES", "ces": "CZ",
	"slk": "SK", "hun": "HU", "bul": "BG", "mkd": "MK", "slv": "SI", "swe": "SE",
	"dan": "DK", "fin": "FI", "est": "EE", "lav": "LV", "lit": "LT", "nob": "NO",
	"nno": "NO", "isl": "IS", "gle": "IE", "cym": "GB", "mlt": "MT", "ltz": "LU",
	"kat": "GE", "hye": "AM", "aze": "AZ", "bel": "BY", "afr": "ZA", "zul": "ZA",
	"nya": "MW", "bem": "ZM", "sna": "ZW", "nde": "ZW", "tsn": "BW", "sot": "LS",
	"run": "BI", "aka": "GH", "gug": "PY", "fij": "FJ", "smo": "WS", "ton": "TO",
	"kal": "GL", "dzo": "BT", "khm": "KH", "lao": "LA", "fuc": "SN", "mnk": "GM",
	"fat": "GH", "ory": "IN", "pbu": "AF", "dgo": "IN", "gom": "IN", "mhr": "RU",
	"mey": "MR", "pga": "SS", "hmo": "PG", "hif": "FJ", "cha": "GU", "tah": "PF",
	"rar": "CK", "niu": "NU", "tkl": "TK", "tvl": "TV", "mah": "MH", "nau": "NR",
	"pau": "PW", "gil": "KI", "chk": "FM", "pon": "FM", "kos": "FM", "uli": "FM",
	"ssw": "SZ", "mos": "BF", "fon": "BJ", "kea": "CV", "swb": "KM", "ewe": "TG",
	"quz": "PE", "qug": "EC", "ayr": "BO", "quh": "BO", "bzj": "BZ", "jam": "JM",
	"acf": "LC", "crs": "SC",
}

// adjectiveTerritory maps a country adjective / demonym found in a language name
// to a territory. Deterministic, curated; the fuzzy parent-name inference from
// the Python original is intentionally dropped.
var adjectiveTerritory = map[string]string{
	"Abkhaz": "GE", "Afghan": "AF", "Albanian": "AL", "Algerian": "DZ", "American": "US",
	"Argentine": "AR", "Argentinian": "AR", "Armenian": "AM", "Australian": "AU", "Auslan": "AU",
	"Austrian": "AT", "Azerbaijani": "AZ", "Bahraini": "BH", "Bangladeshi": "BD",
	"Belgian": "BE", "Belize": "BZ", "Bolivian": "BO", "Bosnian": "BA", "Brazilian": "BR",
	"British": "GB", "Bulgarian": "BG", "Cambodian": "KH", "Cameroonian": "CM", "Canadian": "CA",
	"Catalan": "ES", "Chadian": "TD", "Chilean": "CL", "Chinese": "CN", "Colombian": "CO",
	"Costa Rican": "CR", "Croatian": "HR", "Cuban": "CU", "Cypriot": "CY", "Czech": "CZ",
	"Danish": "DK", "Dominican": "DO", "Dutch": "NL", "Ecuadorian": "EC", "Egyptian": "EG",
	"Emirati": "AE", "Eritrean": "ER", "Estonian": "EE", "Ethiopian": "ET", "Fijian": "FJ",
	"Filipino": "PH", "Finnish": "FI", "French": "FR", "Georgian": "GE", "German": "DE",
	"Ghanaian": "GH", "Greek": "GR", "Guatemalan": "GT", "Haitian": "HT", "Honduran": "HN",
	"Hong Kong": "HK", "Hungarian": "HU", "Icelandic": "IS", "Indian": "IN", "Indonesian": "ID",
	"Iranian": "IR", "Iraqi": "IQ", "Irish": "IE", "Israeli": "IL", "Italian": "IT",
	"Jamaican": "JM", "Japanese": "JP", "Jordanian": "JO", "Kazakh": "KZ", "Kenyan": "KE",
	"Korean": "KR", "Kuwaiti": "KW", "Kyrgyz": "KG", "Lao": "LA", "Latvian": "LV",
	"Lebanese": "LB", "Liberian": "LR", "Libyan": "LY", "Lithuanian": "LT", "Macedonian": "MK",
	"Malagasy": "MG", "Malawian": "MW", "Malaysian": "MY", "Malian": "ML", "Maltese": "MT",
	"Mauritanian": "MR", "Mauritian": "MU", "Mexican": "MX", "Moldovan": "MD", "Mongolian": "MN",
	"Montenegrin": "ME", "Moroccan": "MA", "Mozambican": "MZ", "Namibian": "NA", "Nepalese": "NP",
	"Nepali": "NP", "New Zealand": "NZ", "Nicaraguan": "NI", "Nigerian": "NG", "Norwegian": "NO",
	"Omani": "OM", "Pakistani": "PK", "Palestinian": "PS", "Panamanian": "PA", "Papua New Guinea": "PG",
	"Paraguayan": "PY", "Peruvian": "PE", "Philippine": "PH", "Polish": "PL", "Portuguese": "PT",
	"Puerto Rican": "PR", "Qatari": "QA", "Romanian": "RO", "Russian": "RU", "Saudi": "SA",
	"Senegalese": "SN", "Serbian": "RS", "Singapore": "SG", "Slovak": "SK", "Slovenian": "SI",
	"Somali": "SO", "South African": "ZA", "Spanish": "ES", "Sri Lankan": "LK", "Sudanese": "SD",
	"Swedish": "SE", "Swiss": "CH", "Syrian": "SY", "Taiwanese": "TW", "Tanzanian": "TZ",
	"Thai": "TH", "Tibetan": "CN", "Timorese": "TL", "Tunisian": "TN", "Turkish": "TR",
	"Turkmen": "TM", "Ugandan": "UG", "Ukrainian": "UA", "Uruguayan": "UY", "Uzbek": "UZ",
	"Venezuelan": "VE", "Vietnamese": "VN", "Yemeni": "YE", "Zambian": "ZM", "Zimbabwean": "ZW",
}

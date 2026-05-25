// cldr sets each language's `scripts` array from Unicode CLDR's languageData.
//
// Source:    https://github.com/unicode-org/cldr-json
// Endpoint:  raw JSON at github.com/unicode-org/cldr-json/.../languageData.json
// Cadence:   monthly cache via corpus.FetchCached
//
// CLDR keys most major languages by ISO 639-1 (2-letter) codes and the rest
// by ISO 639-3 (3-letter). We normalize everything to ISO 639-3 using the
// SIL-published alias table (see iso639_1to3 below) and merge primary +
// "-alt-secondary" script sets into one deduped, deterministically-ordered
// array per language.
package sources

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"languages/tools/corpus"
)

func init() { Register(cldr{}) }

type cldr struct{}

func (cldr) Name() string { return "cldr" }

const cldrLanguageDataURL = "https://raw.githubusercontent.com/unicode-org/cldr-json/main/cldr-json/cldr-core/supplemental/languageData.json"

type cldrLangData struct {
	Supplemental struct {
		LanguageData map[string]struct {
			Scripts []string `json:"_scripts"`
		} `json:"languageData"`
	} `json:"supplemental"`
}

func (cldr) Run(opts Options) error {
	res, err := corpus.FetchCached(corpus.FetchOptions{
		Name:  "cldr-languagedata",
		URL:   cldrLanguageDataURL,
		Force: opts.Force,
		Ext:   "json",
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

	var doc cldrLangData
	if err := json.Unmarshal(res.Body, &doc); err != nil {
		return fmt.Errorf("parse cldr json: %w", err)
	}
	raw := doc.Supplemental.LanguageData
	fmt.Printf("CLDR entries: %d\n", len(raw))

	// Merge primary + -alt-secondary into one map keyed by ISO 639-3.
	merged := map[string][]string{}
	for key, body := range raw {
		base := key
		if i := strings.Index(base, "-alt-"); i >= 0 {
			base = base[:i]
		}
		iso3 := normalizeISO(base)
		if iso3 == "" {
			continue
		}
		merged[iso3] = append(merged[iso3], body.Scripts...)
	}

	// Dedup + sort each script list. Sort order: keep the most common first
	// (alphabetical) since CLDR doesn't reliably mark "primary" and ordering
	// would otherwise depend on map iteration.
	for iso, scripts := range merged {
		merged[iso] = dedupSorted(scripts)
	}

	// Apply.
	updated, unchanged, missing := 0, 0, 0
	var missingISOs []string
	isos := make([]string, 0, len(merged))
	for iso := range merged {
		isos = append(isos, iso)
	}
	sort.Strings(isos)

	for _, iso := range isos {
		path := filepath.Join(opts.Dir, iso+".md")
		if _, err := corpus.ReadFile(path); err != nil {
			missing++
			missingISOs = append(missingISOs, iso)
			continue
		}
		r, err := corpus.SetScalars(path, []corpus.ScalarOp{
			{Key: "scripts", Value: merged[iso], Mode: corpus.Overwrite},
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

// normalizeISO returns the ISO 639-3 code for an ISO 639-1 or 639-3 input;
// returns "" if the input is neither.
func normalizeISO(code string) string {
	switch len(code) {
	case 2:
		if v, ok := iso639_1to3[code]; ok {
			return v
		}
		return ""
	case 3:
		return code
	default:
		return ""
	}
}

func dedupSorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// iso639_1to3 maps ISO 639-1 (two-letter) codes to ISO 639-3 (three-letter).
// Source: SIL's iso-639-3.tab, column Part1 → column Id. Stable; ISO codes
// are not retired or renumbered.
var iso639_1to3 = map[string]string{
	"aa": "aar", "ab": "abk", "ae": "ave", "af": "afr", "ak": "aka",
	"am": "amh", "an": "arg", "ar": "ara", "as": "asm", "av": "ava",
	"ay": "aym", "az": "aze", "ba": "bak", "be": "bel", "bg": "bul",
	"bi": "bis", "bm": "bam", "bn": "ben", "bo": "bod", "br": "bre",
	"bs": "bos", "ca": "cat", "ce": "che", "ch": "cha", "co": "cos",
	"cr": "cre", "cs": "ces", "cu": "chu", "cv": "chv", "cy": "cym",
	"da": "dan", "de": "deu", "dv": "div", "dz": "dzo", "ee": "ewe",
	"el": "ell", "en": "eng", "eo": "epo", "es": "spa", "et": "est",
	"eu": "eus", "fa": "fas", "ff": "ful", "fi": "fin", "fj": "fij",
	"fo": "fao", "fr": "fra", "fy": "fry", "ga": "gle", "gd": "gla",
	"gl": "glg", "gn": "grn", "gu": "guj", "gv": "glv", "ha": "hau",
	"he": "heb", "hi": "hin", "ho": "hmo", "hr": "hrv", "ht": "hat",
	"hu": "hun", "hy": "hye", "hz": "her", "ia": "ina", "id": "ind",
	"ie": "ile", "ig": "ibo", "ii": "iii", "ik": "ipk", "io": "ido",
	"is": "isl", "it": "ita", "iu": "iku", "ja": "jpn", "jv": "jav",
	"ka": "kat", "kg": "kon", "ki": "kik", "kj": "kua", "kk": "kaz",
	"kl": "kal", "km": "khm", "kn": "kan", "ko": "kor", "kr": "kau",
	"ks": "kas", "ku": "kur", "kv": "kom", "kw": "cor", "ky": "kir",
	"la": "lat", "lb": "ltz", "lg": "lug", "li": "lim", "ln": "lin",
	"lo": "lao", "lt": "lit", "lu": "lub", "lv": "lav", "mg": "mlg",
	"mh": "mah", "mi": "mri", "mk": "mkd", "ml": "mal", "mn": "mon",
	"mr": "mar", "ms": "msa", "mt": "mlt", "my": "mya", "na": "nau",
	"nb": "nob", "nd": "nde", "ne": "nep", "ng": "ndo", "nl": "nld",
	"nn": "nno", "no": "nor", "nr": "nbl", "nv": "nav", "ny": "nya",
	"oc": "oci", "oj": "oji", "om": "orm", "or": "ori", "os": "oss",
	"pa": "pan", "pi": "pli", "pl": "pol", "ps": "pus", "pt": "por",
	"qu": "que", "rm": "roh", "rn": "run", "ro": "ron", "ru": "rus",
	"rw": "kin", "sa": "san", "sc": "srd", "sd": "snd", "se": "sme",
	"sg": "sag", "sh": "hbs", "si": "sin", "sk": "slk", "sl": "slv",
	"sm": "smo", "sn": "sna", "so": "som", "sq": "sqi", "sr": "srp",
	"ss": "ssw", "st": "sot", "su": "sun", "sv": "swe", "sw": "swa",
	"ta": "tam", "te": "tel", "tg": "tgk", "th": "tha", "ti": "tir",
	"tk": "tuk", "tl": "tgl", "tn": "tsn", "to": "ton", "tr": "tur",
	"ts": "tso", "tt": "tat", "tw": "twi", "ty": "tah", "ug": "uig",
	"uk": "ukr", "ur": "urd", "uz": "uzb", "ve": "ven", "vi": "vie",
	"vo": "vol", "wa": "wln", "wo": "wol", "xh": "xho", "yi": "yid",
	"yo": "yor", "za": "zha", "zh": "zho", "zu": "zul",
}

# Languages of the World

A curated dataset of the world's languages — one markdown file per ISO 639-3 code, with YAML frontmatter describing core attributes (name, autonym, country, EGIDS status, coordinates) and an array of external resource links.

## Layout

```
schema.json     JSON Schema describing the frontmatter
languages/      One <iso>.md per language; <iso> is ISO 639-3 (e.g. aar.md)
tools/          Go utilities
```

## File format

Each `languages/<iso>.md` is a markdown file with YAML frontmatter:

```yaml
---
iso: aar
iso639_1: aa
name: Afar
autonym: Qafar af
population: 2541000
country_id: ET
country_name: Ethiopia
status_id: "2"
scope: individual
language_type: living
latitude: 12.228107
longitude: 41.808293
wikidata_id: Q27811
---
```

The order of keys is canonical see the `properties` block of `schema.json`.

### Required keys

`iso`, `name` — present in every file.

### Fields

| Field            | Notes                                                                  |
| ---------------- | ---------------------------------------------------------------------- |
| `iso`            | ISO 639-3, three lowercase letters.                                    |
| `iso639_1`       | ISO 639-1 two-letter code, when one exists (e.g. `aa`). Populated by `tools/sources/iso639.go`. |
| `macrolanguage_id` | ISO 639-3 code of the parent macrolanguage, for individual member codes (e.g. `zho` on `cmn`). Populated by `tools/sources/iso639.go`. |
| `name`           | Common English name.                                                   |
| `autonym`        | Endonym (the language's name in itself), optional.                     |
| `alt_names`      | Inline array of deduplicated alternative names (English variants, transliterations, names in other languages/scripts). Excludes `name` and `autonym`. Populated by `tools/sources/alt_names.go`. |
| `population`     | Integer, approximate speakers.                                         |
| `country_id`     | ISO 3166-1 alpha-2 (uppercase).                                        |
| `country_name`   | Free text country name.                                                |
| `location`       | Free-text location where the language is spoken, sometimes with a map reference (e.g. `Nigeria, Map 6`). Migrated from DBS language records. |
| `area`           | One-line characterization of the language and where it is spoken (e.g. `Edoid language spoken in Nigeria`). Migrated from DBS language records. |
| `status_id`      | EGIDS scale — `0`–`10`, with `6a`/`6b`/`8a`/`8b`. Quoted as a string. |
| `scope`          | ISO 639-3 code scope: `individual`, `macrolanguage`, or `special`. Populated by `tools/sources/iso639.go`. |
| `language_type`  | ISO 639-3 language type: `living`, `extinct`, `ancient`, `historical`, `constructed`, or `special`. Populated by `tools/sources/iso639.go`. |
| `latitude`       | Decimal degrees, `-90`..`90`.                                          |
| `longitude`      | Decimal degrees, `-180`..`180`.                                        |
| `language_map_img` | Filename of a language-area map image, served from `https://img.dbs.org/country/languages/{filename}`. Migrated from DBS language records. |
| `scripts`        | Inline array of ISO 15924 codes (e.g. `[Latn, Cyrl]`). Cross-references the sibling `scripts/` repo. Populated by `tools/sources/cldr.go`. |
| `glottocode`                | Glottolog languoid ID (e.g. `aari1239`). Populated by `tools/sources/glottolog.go`. |
| `glottolog_family_id`       | Glottolog ID of the top-level family.                                       |
| `glottolog_family_name`     | Human-readable family name.                                                  |
| `glottolog_classification`  | Top-down genealogical path, joined with ` > `.                              |
| `wikidata_id`               | Wikidata entity ID (QID, e.g. `Q27811`), resolved via P220. Populated by `tools/sources/wikidata_id.go`. |
| `wikipedia_url`             | English Wikipedia article URL. Populated by `tools/sources/wikipedia.go`.    |
| `translations`              | Block array of `{translation_iso, name, auto?}`. Populated by `tools/sources/wikidata_names.go` (primary) and `tools/sources/cldr_names.go` (gap-fill). Currently targets `zho`, `jpn`, `hin`, `kor`, `ara`, `spa`, `fra`. The optional `auto: true` flag marks automated translations; `spa` is filled to 100% coverage — curated entries from Wikidata/CLDR, and the remaining ~5,100 by LLM (`auto: true`) via `tools/spanishfill`. |                 |
| `rolv_dialects`  | Array of `{rolv_code, language_tag, name, country_id, location}`. Populated by `tools/sources/rolv.go` from GRN's Registry of Language Varieties. |

## Tools

All commands run from the repo root.

### Validate the corpus against the schema

```sh
go run ./tools -validate
```

Checks every file under `languages/`:

- required keys present
- no unknown keys
- canonical key order
- regex patterns (`iso`, `country_id`)
- numeric ranges (`population`, `latitude`, `longitude`)
- enum vocabulary (`status_id`, `scope`, `language_type`)
- per-item fields under array fields (`rolv_dialects`)

Exits non-zero if any file has issues.

### Run external sources

External-data updaters live under `tools/sources/`. Each one fetches a remote index, parses entries, and merges them into the matching languages' frontmatter (scalars and/or arrays). See `tools/sources/README.md` for the skeleton.

```sh
go run ./tools -list                              # show registered sources
go run ./tools -update <name>                     # run one source
go run ./tools -update all                        # run all
go run ./tools -update all -dry-run               # preview, no writes
go run ./tools -update all -force                 # bypass the .cache
go run ./tools -update all -bail                  # stop on first failure
```

### Registered sources

| Name        | Fields                                                                              | Upstream                                                                                      |
| ----------- | ----------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| `alt_names` | `alt_names`                                                                          | [Glottolog `names.csv`](https://github.com/glottolog/glottolog-cldf) (~120 K name rows, multi-provider) joined on `glottocode` + [SIL ISO 639-3 Name Index](https://iso639-3.sil.org/code_tables/download_tables) joined on ISO. ~6,700 files enriched. |
| `cldr`      | `scripts`                                                                            | [Unicode CLDR](https://github.com/unicode-org/cldr-json) `languageData.json`. ~810 ISO-coded languages with script affiliations (primary + secondary, deduped). |
| `glottolog` | `glottocode`, `glottolog_family_id`, `glottolog_family_name`, `glottolog_classification`. Fills `latitude`/`longitude`/`country_id` when missing. | [Glottolog 5.x](https://glottolog.org/) languoid CSV (CC BY 4.0). ~7,500 ISO-coded languoids. |
| `iso639`    | `iso639_1`, `macrolanguage_id`, `scope`, `language_type`                            | [SIL ISO 639-3 code tables](https://iso639-3.sil.org/code_tables/download_tables) (`iso-639-3.tab` + `iso-639-3-macrolanguages.tab`). Covers the full corpus; ~184 codes carry an ISO 639-1 equivalent, ~444 are macrolanguage members. |
| `rolv`      | `rolv_dialects`                                                                     | GRN's [Registry of Language Varieties](https://hisregistries.org/rolv/) via GraphQL at `gql.globalrecordings.net`. ~12,300 varieties across ~3,300 ISO codes. |
| `cldr_names` | `translations[]` (IfMissing per `translation_iso`, fills gaps).                       | Unicode CLDR locale files for `zh`, `ja`, `hi`, `ko`, `ar`, `es`, `fr`. Adds ~330 entries on top of `wikidata_names`. Easily extended to more languages — append to `cldrLocales` in the source. |
| `wikidata_id` | `wikidata_id`                                                                      | Wikidata SPARQL P220 → entity QID. ~7,900 ISOs covered; duplicate claims resolve to the lowest QID. |
| `wikidata_names` | `translations[]` (per-language priority: zh-Hans > zh-CN > zh-SG > zh; ja; hi; ko; ar; es; fr). | Wikidata SPARQL `rdfs:label` filtered by xml:lang. ~7,160 ISOs covered. Extend by appending to `translationTargets` in the source. |
| `wikipedia` | `wikipedia_url` (always); `autonym`, `population` (when missing); markdown body (when empty). | Wikidata SPARQL (P220 → article) + English Wikipedia REST summary + MediaWiki action API for `{{Infobox language}}`. Per-article cache under `.cache/wikipedia/`. **Slow on a cold cache** (~50 minutes for the full corpus); use `-only iso1,iso2` to scope. |

Add more under `tools/sources/` — see `tools/sources/README.md` for the template.

## Adding or editing languages by hand

1. Create `languages/<iso>.md` (three lowercase letters, e.g. `kor.md`).
2. Use the canonical key order from `schema.json` or an existing nearby file.
3. Run `go run ./tools -validate` before committing.

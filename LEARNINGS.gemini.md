# Learning Log

*This log records completed tasks following the PRAR workflow.*

## Task: Initial Monorepo Setup
- **Perceive**: User wanted a SolidStart + Go + Docker Compose monorepo with Redis.
- **Reason**: Planned the directory structure and docker-compose orchestration. Decided on `redis/redis-stack` to support JSON/Search.
- **Act**: Initialized `frontend` with pnpm, `backend` with go module, and created `docker-compose.yml`.
- **Refine**: Mapped ports correctly and tested the docker-compose setup. Added Postgres database per updated user request.

## Task: ETL Pipeline Planning
- **Perceive**: Extract English dictionary data from `kaikki.org` jsonl.gz files into Postgres and Redis.
- **Reason**: 2.4GB streaming approach with batch inserts is optimal for memory efficiency.
- **Act**: Implemented `cmd/etl/main.go` using `pgx` (CopyFromRows) and `go-redis` (Pipeline + JSON.SET). Updated to Go 1.26 and mapped backend Docker volumes for local sync.
- **Refine**: Fixed networking issue with `REDIS_URL` in `docker-compose.yml` (added `redis://` scheme) and fixed JSON serialization for `go-redis` pipeline by explicitly marshaling the map before insertion.

## Task: POS Consolidation Refactor
- **Perceive**: User wanted to store POS definitions and examples grouped in a single JSONB field rather than creating separate rows per POS.
- **Reason**: Investigated the `wiktextract` dataset and confirmed entries are grouped sequentially by word. Designed a state-machine parser to accumulate definitions/examples in memory and flush when the word changes.
- **Act**: Dropped the existing Postgres table, flushed Redis, updated `WordEntry` struct, and rewrote `StreamAndProcess` to consolidate the items. Verified build success.
- **Refine**: Fixed a minor unused variable compilation error during the pipeline refactor.

## Task: Dictionary Word Filter
- **Perceive**: User reported junk entries (symbols, non-Latin scripts) polluting the DB. Wanted an Oxford/Apple-style dataset but also to keep CamelCase (iPhone), acronyms (NASA), numbers with meaning (101), and loanwords with diacritics (résumé, café).
- **Reason**: Queried the DB directly to measure impact. Found ~21K of 1.36M entries (~1.5%) would be filtered. Designed a regex + allowlist approach: `passesDictionaryWordFilter()` using Unicode ranges for Latin diacritics and a curated allowlist for symbol-containing words (24/7, Q&A, R&D, etc.).
- **Act**: Added the filter function and allowlist to `parser.go`, integrated it into `StreamAndProcess` before `mergeEntry`, and added a skip counter to the final log output. Later tightened the regex to reject all digit-starting words (previously allowed), moving them to an external allowlist file (`backend/data/allowlist.txt`) loaded at init time. This makes the allowlist editable without recompiling Go code.
- **Refine**: The allowlist file supports comments (`#`) and blank lines for organization. Populated it with ~60 legitimate number-starting words and ~17 symbol-containing words sourced from Oxford-style references.

## Task: Clean Etymology Tree Diagrams
- **Perceive**: Wiktextract data prepends a large "Etymology tree" block to the `etymology_text` field, which pollutes the prose.
- **Reason**: Instead of using fragile string-matching heuristics ("Inherited from", "Cognates"), investigated the raw JSON via `jq`. Found that Wiktextract maps the exact tree output to the `expansion` field of templates (like `{{etymon}}`) inside the `etymology_templates` array.
- **Act**: Added `EtymologyTemplates` parsing to the `WiktextractWord` struct. Rewrote `cleanEtymology()` to iterate over the parsed templates and exactly subtract the template's expanded text from the start of the `etymology_text`. Additionally, updated it to return only the first line (`\n` split) of the cleaned text to isolate the primary etymology and discard extraneous trailing sections like "Cognates" or "Further possible etymology".
- **Refine**: This structural approach achieves 100% precision in stripping the tree without risking accidental deletion of legitimate etymology prose, and taking the first paragraph successfully simplifies the etymology down to its core description.

## Task: Optimize Database for Autocomplete and Word Frequencies
- **Perceive**: Pushing 1.3M full dictionary JSON payloads to Redis for autocomplete and lookups consumes excessive memory and duplicates data unnecessarily. RediSearch natively supports a lightweight autocomplete structure (`FT.SUGADD`). Furthermore, the user requested incorporating word frequencies (using Python's `wordfreq` package) to sort autocomplete suggestions by popularity.
- **Reason**: Calling a Python script from the Go ETL pipeline 1.3M times is unacceptably slow. Instead, the best pattern is to decouple the frequency extraction. A one-off Python script can dump the `wordfreq` data for English into a static JSON file. The Go parser can then load this JSON into a map at startup (`init()`) and perform an O(1) lookup to assign a Zipf frequency score to each word as it processes the Wiktextract stream.
- **Act**: Created `scripts/export_wordfreq.py` and generated `backend/data/wordfreq.json`. Updated the `words` table in Postgres to include a `frequency REAL` column. Refactored `redis.go` to drop the `JSON.SET` logic and replace it with `FT.SUGADD dict_autocomplete <word> <score>`, using the assigned Zipf frequency as the score.
- **Refine**: The ETL pipeline remains extremely fast because it never leaves Go during processing. Redis memory footprint is drastically reduced since it now only stores the prefix autocomplete dictionary, leaving Postgres as the primary source of truth for full dictionary lookups.

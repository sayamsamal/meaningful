# Meaningful

A fast, comprehensive English dictionary application powered by data extracted from [Wiktionary](https://en.wiktionary.org/) via [wiktextract](https://github.com/tatuylonen/wiktextract). Meaningful provides definitions, synonyms, antonyms, etymology, and usage examples — styled after premium dictionaries like Oxford and Apple Dictionary.

## Architecture

```
meaningful/
├── frontend/          # SolidStart (TypeScript, Vite)
├── backend/           # Go 1.26
│   ├── cmd/api/       # HTTP API server (port 8080)
│   ├── cmd/etl/       # ETL pipeline for Wiktextract data
│   └── internal/
│       ├── database/  # PostgreSQL & Redis connection helpers
│       └── parser/    # JSONL streaming parser with data cleaning
├── docs/              # Documentation & backlog
└── docker-compose.yml # Full-stack orchestration
```

| Component            | Technology              | Purpose                                      |
|----------------------|-------------------------|----------------------------------------------|
| **Frontend**         | SolidStart v2 + Vite 7  | Web UI for searching and displaying words    |
| **Backend**          | Go 1.26                 | API server and ETL pipeline                  |
| **Primary Database** | PostgreSQL 16           | Structured dictionary storage (JSONB)        |
| **Cache/Search**     | Redis Stack             | Fast autocomplete via RediSearch + RedisJSON |
| **Orchestration**    | Docker Compose          | Local development environment                |

## Data Model

Each word is stored as a single row in PostgreSQL with all parts of speech consolidated into one JSONB `senses` field:

```json
{
  "word": "run",
  "etymologies": ["From Middle English runnen..."],
  "senses": {
    "noun": [
      {
        "definition": "An act or instance of running.",
        "examples": ["I went for a quick run."]
      }
    ],
    "verb": [
      {
        "definition": "To move at a speed faster than a walk.",
        "examples": ["He had to run to catch the bus."]
      }
    ]
  },
  "synonyms": ["sprint", "dash", "jog"],
  "antonyms": ["walk", "crawl"]
}
```

### PostgreSQL Schema

| Column        | Type           | Description                                          |
|---------------|----------------|------------------------------------------------------|
| `id`          | `SERIAL`       | Primary key                                          |
| `word`        | `VARCHAR(255)` | The dictionary word (indexed)                        |
| `frequency`   | `REAL`         | Zipf frequency score derived from `wordfreq`         |
| `etymologies` | `JSONB`        | Array of etymology strings (deduplicated)            |
| `senses`      | `JSONB`        | POS-keyed map of definitions with examples           |
| `synonyms`    | `JSONB`        | Deduplicated array of synonym strings                |
| `antonyms`    | `JSONB`        | Deduplicated array of antonym strings                |

### Redis Schema

Redis is specifically optimized for autocomplete suggestions. We use the RediSearch Autocomplete Dictionary structure (`FT.SUGADD`) under the key `dict_autocomplete`.
- Each word is inserted with its `frequency` acting as the auto-complete score, ensuring common words rank higher in typeahead suggestions.
- *Note:* Redis does not store the full JSON blobs to prevent out-of-memory issues. Full definitions are fetched via PostgreSQL.

## Prerequisites

- [Docker](https://docs.docker.com/get-docker/) (with Docker Compose)
- Python 3 (For generating the frequency dataset)

## Getting Started

### 1. Generate the Word Frequency Dictionary
Before running the Go ETL, we need to export Zipf frequencies using the Python `wordfreq` library.
```bash
cd backend/scripts
python3 -m venv venv
source venv/bin/activate
pip install wordfreq
python export_wordfreq.py
cd ../..
```
This generates `backend/data/wordfreq.json` which the Go parser loads into memory.

### 2. Start all services

```bash
docker compose up -d
```

This will start four services:

| Service    | URL                       | Description              |
|------------|---------------------------|--------------------------|
| Frontend   | http://localhost:5173      | SolidStart dev server    |
| Backend    | http://localhost:8080      | Go API server            |
| PostgreSQL | `localhost:5432`          | Database (user: `postgres`, pass: `postgres`, db: `meaningful`) |
| Redis      | `localhost:6379`          | Cache & autocomplete     |
| RedisInsight | http://localhost:8001   | Redis management UI      |

### 3. Run the ETL pipeline

The ETL pipeline is run **manually** — it does not execute on container startup. This gives you full control over when to ingest or refresh the dataset.

```bash
docker compose exec backend go run cmd/etl/main.go
```

This will:
1. Stream the compressed JSONL dataset (~2.4 GB) from [kaikki.org](https://kaikki.org/dictionary/rawdata.html)
2. Decompress and parse it on-the-fly (memory-efficient, no disk usage)
3. Filter to English entries only
4. Apply data quality filters and structural etymology cleaning
5. Assign a Zipf frequency score from the pre-generated `wordfreq.json`
6. Consolidate all parts of speech for each word into a single record
7. Batch insert into PostgreSQL (using `COPY`) and Redis (using `FT.SUGADD` pipelines)

> **Note:** The full import takes several minutes depending on your hardware and network speed.

### 4. Re-running the ETL

When a new dataset is released on kaikki.org, you can refresh the data by dropping the existing table and flushing Redis, then re-running:

```bash
docker compose exec postgres psql -U postgres -d meaningful -c "DROP TABLE IF EXISTS words;"
docker compose exec redis redis-cli FLUSHALL
docker compose exec backend go run cmd/etl/main.go
```

## Data Cleaning

The ETL pipeline enforces Oxford/Apple-style data quality. The following regex pattern is used to filter dictionary words:

```go
// validWordPattern enforces that a word:
//   - Starts with an ASCII letter (digit-starting words are handled via allowlist)
//   - Contains only ASCII letters, digits, spaces, hyphens, apostrophes,
//     and common Latin diacritics (é, ñ, ü, ß, etc.)
//   - Is between 1 and 45 characters long
var validWordPattern = regexp.MustCompile(
	`^[A-Za-z]` + // must start with a letter (number-words use the allowlist)
		`[A-Za-z0-9 '\-` + // ASCII core characters
		`\x{00C0}-\x{00FF}` + // Latin-1 Supplement (À-ÿ): é, ñ, ü, ß, etc.
		`\x{0100}-\x{017F}` + // Latin Extended-A (Ā-ſ): ō, œ, š, ž, etc.
		`]*$`) // zero or more of the above
```

- **Requires** words to match the strict regex above.
- **Maintains an external allowlist** (`backend/data/allowlist.txt`) for legitimate words starting with numbers or containing symbols (`101`, `24/7`, `Q&A`, `R&D`, etc.)
- **Structural Etymology Cleaning**: Uses Wiktionary's parsed template expansions (`etymology_templates`) to explicitly subtract visual "Etymology tree" diagrams and truncate irrelevant headings (like "Cognates"), leaving only concise, clean prose.
- **Enforces** a max word length of 45 characters

## Data Source

Dictionary data is sourced from [Wiktextract](https://github.com/tatuylonen/wiktextract), which extracts structured data from the English Wiktionary. The raw JSONL dataset is published at [kaikki.org](https://kaikki.org/dictionary/rawdata.html).

## Tech Stack Details

### Backend Dependencies

| Package                         | Version | Purpose                     |
|---------------------------------|---------|-----------------------------|
| `github.com/jackc/pgx/v5`      | v5.9.2  | PostgreSQL driver & pool    |
| `github.com/redis/go-redis/v9` | v9.19.0 | Redis client (JSON + Search)|

### Frontend Dependencies

| Package                            | Version         | Purpose                  |
|------------------------------------|-----------------|--------------------------|
| `@solidjs/start`                   | 2.0.0-alpha.2   | SolidStart framework     |
| `solid-js`                         | ^1.9.5          | Reactive UI library      |
| `vite`                             | ^7.0.0          | Build tool & dev server  |
| `tailwindcss`                      | ^4.2.4          | Utility-first CSS        |

## License

This project is for personal/educational use. Dictionary data is derived from Wiktionary and is available under the [Creative Commons Attribution-ShareAlike License](https://creativecommons.org/licenses/by-sa/4.0/).

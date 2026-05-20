# Meaningful — Claude Code Reference

This file is the primary reference for working in this codebase. Read it before making changes.

---

## Project Overview

**Meaningful** is a fast, comprehensive English dictionary web application styled after Oxford and Apple Dictionary. It provides definitions, synonyms, antonyms, etymology, and usage examples sourced from Wiktionary via [wiktextract](https://github.com/tatuylonen/wiktextract).

---

## Architecture at a Glance

```
meaningful/
├── frontend/                   # SolidStart (TypeScript, Vite 7)
│   ├── src/
│   │   ├── app.tsx             # Root layout — Router, SearchBar, Suspense
│   │   ├── app.css             # All styles (CSS variables + component classes)
│   │   ├── components/
│   │   │   ├── SearchBar.tsx   # Client-side autocomplete (debounce + keyboard nav)
│   │   │   └── WordCard.tsx    # SSR word definition display (uses `"use server"`)
│   │   ├── routes/
│   │   │   ├── index.tsx       # / → hardcoded "meaningful" Word of the Day
│   │   │   └── [word].tsx      # /{word} → dynamic definition page
│   │   └── types/schema.ts     # Auto-generated OpenAPI types (pnpm run gen-types)
│   ├── vite.config.ts          # SolidStart + Nitro v2 plugins
│   ├── Dockerfile              # node:22-alpine, pnpm, port 5173
│   └── package.json
│
├── backend/
│   ├── cmd/
│   │   ├── api/main.go         # HTTP server entrypoint (port 8080)
│   │   └── etl/main.go         # One-shot ETL pipeline entrypoint
│   ├── internal/
│   │   ├── api/handlers.go     # HTTP handlers: GetWord, GetMultipleWords, Autocomplete
│   │   ├── database/
│   │   │   ├── postgres.go     # pgxpool connection, schema, batch insert, queries
│   │   │   └── redis.go        # go-redis connection, FT.SUGADD autocomplete, word cache
│   │   └── parser/
│   │       └── parser.go       # Streaming JSONL parser with filter + POS consolidation
│   ├── data/
│   │   ├── allowlist.txt       # Words bypassing the regex filter (101, Q&A, etc.)
│   │   └── wordfreq.json       # Pre-generated Zipf frequency map (Python wordfreq)
│   ├── scripts/
│   │   └── export_wordfreq.py  # One-time Python script that generates wordfreq.json
│   ├── meaningful.yaml         # OpenAPI 3.0 spec — source of truth for API contracts
│   ├── Dockerfile              # golang:1.26-alpine, builds /backend-api binary
│   └── go.mod                  # module: meaningful-backend, go 1.26
│
├── docs/
│   └── backlog.md              # Task backlog (Pending / In Progress / Completed)
├── docker-compose.yml          # Orchestrates all 4 services
└── .env                        # Local secrets (gitignored)
```

---

## Services & Ports

| Service      | Port  | URL                          | Notes                                |
|-------------|-------|------------------------------|--------------------------------------|
| Frontend    | 5173  | http://localhost:5173        | SolidStart dev server (pnpm dev)     |
| Backend     | 8080  | http://localhost:8080        | Go HTTP API                          |
| PostgreSQL  | 5432  | localhost:5432               | user/pass/db: `postgres/postgres/meaningful` |
| Redis Stack | 6379  | localhost:6379               | Includes RediSearch module           |
| RedisInsight| 8001  | http://localhost:8001        | Redis management UI                  |

---

## Local Setup & Running

**Prerequisites:** Docker (with Compose), Python 3 (one-time step only)

### One-time: generate word frequency data
```bash
cd backend/scripts
python3 -m venv venv && source venv/bin/activate
pip install wordfreq
python export_wordfreq.py
cd ../..
```
This produces `backend/data/wordfreq.json` (~Zipf scores for ~200K English words).

### Start all services
```bash
docker compose up -d
```

### Run the ETL pipeline (required to populate the database)
```bash
docker compose exec backend go run cmd/etl/main.go
```
Downloads ~2.4 GB from kaikki.org, streams and parses it, then batch-inserts into Postgres and Redis. Takes several minutes. **Run only after `docker compose up -d`.**

### Re-run ETL (refresh dataset)
```bash
docker compose exec postgres psql -U postgres -d meaningful -c "DROP TABLE IF EXISTS words;"
docker compose exec redis redis-cli FLUSHALL
docker compose exec backend go run cmd/etl/main.go
```

### Run frontend locally (outside Docker)
```bash
cd frontend
pnpm install
pnpm dev
```

### Regenerate TypeScript types from OpenAPI spec
```bash
cd frontend
pnpm run gen-types
```
Output: `src/types/schema.ts`. Run this after changing `backend/meaningful.yaml`.

---

## API Endpoints

All endpoints are under `/api`. Defined in `backend/meaningful.yaml`.

| Method | Path                 | Query Params | Description                              |
|--------|----------------------|-------------|------------------------------------------|
| GET    | `/api/word/{word}`   | —           | Single word definition. Returns `WordEntry`. Underscores in `{word}` are treated as spaces. |
| GET    | `/api/words`         | `w=a,b,c`   | Multiple words in one request. Returns `[]WordEntry`. |
| GET    | `/api/autocomplete`  | `query=pre` | Prefix autocomplete from Redis. Returns `[]string`. |
| GET    | `/health`            | —           | Health check, returns `200 OK`.          |

**Caching headers:**
- Word definitions: `Cache-Control: public, max-age=86400`
- Autocomplete: `Cache-Control: public, max-age=60`
- Cache hits set `X-Cache: HIT`, misses set `X-Cache: MISS`

**CORS:** All origins (`*`) are allowed via the `corsMiddleware` wrapper. Methods: `GET, OPTIONS`.

---

## Data Model

### PostgreSQL — `words` table

| Column        | Type           | Description                                     |
|---------------|----------------|-------------------------------------------------|
| `id`          | `SERIAL`       | Primary key                                     |
| `word`        | `VARCHAR(255)` | The dictionary word (indexed, case-insensitive) |
| `frequency`   | `REAL`         | Zipf score (0–8 scale; higher = more common)    |
| `etymologies` | `JSONB`        | `[]string` — deduplicated etymology strings     |
| `senses`      | `JSONB`        | `map[pos][]Definition` — POS-grouped definitions |
| `synonyms`    | `JSONB`        | `[]string` — deduplicated                       |
| `antonyms`    | `JSONB`        | `[]string` — deduplicated                       |

Queries always use `LOWER(word) = $1` for case-insensitive lookup.

### WordEntry JSON shape
```json
{
  "word": "run",
  "frequency": 5.82,
  "etymologies": ["From Middle English runnen, from Old English rinnan..."],
  "senses": {
    "verb": [{ "definition": "To move faster than a walk.", "examples": ["She ran to catch the bus."] }],
    "noun": [{ "definition": "An act of running.", "examples": [] }]
  },
  "synonyms": ["sprint", "dash"],
  "antonyms": ["walk"]
}
```

### Redis

- **Key `dict_autocomplete`**: RediSearch suggestion dictionary (`FT.SUGADD`). Words ranked by Zipf frequency. Used for `GET /api/autocomplete`.
- **Key pattern `cache:word:<word>`**: JSON-serialized `WordEntry`, TTL 24h. Read-through cache populated on first Postgres hit.

---

## ETL Pipeline Details

**Source:** `https://kaikki.org/dictionary/raw-wiktextract-data.jsonl.gz` (~2.4 GB gzipped JSONL)

**Flow:**
1. Stream + decompress JSONL on-the-fly (no disk writes)
2. Filter to `lang_code == "en"` only
3. Apply `passesDictionaryWordFilter()`:
   - Regex: must start with ASCII letter, contain only letters/digits/spaces/hyphens/apostrophes/Latin diacritics (U+00C0–U+017F), max 45 chars
   - Allowlist: `backend/data/allowlist.txt` for exceptions (101, Q&A, R&D, etc.)
4. Group consecutive entries by word into one `WordEntry` (POS consolidation state machine)
5. Assign Zipf frequency from pre-loaded `wordfreq.json` map
6. Clean etymology: strip "Etymology tree" visual diagrams using `etymology_templates` expansions; truncate at "Cognates" / "Descendants" headings
7. Batch insert to Postgres via `pgx.CopyFrom` (batch size: 2000)
8. Batch insert to Redis via `FT.SUGADD` pipeline

---

## Frontend Architecture

### Rendering split
- **`WordCard.tsx`** — uses `"use server"` directive. The `fetchWord` function runs on the SolidStart Node.js server. Uses `BACKEND_URL` env var (internal Docker hostname `http://backend:8080`). Never exposes the backend host to the browser.
- **`SearchBar.tsx`** — runs entirely client-side (no `"use server"`). Uses `VITE_BACKEND_URL` env var (defaults to `http://localhost:8080`). Must be accessible from the user's browser.

### Environment variables
| Variable          | Used in          | Default                  | Description                         |
|-------------------|-----------------|--------------------------|-------------------------------------|
| `BACKEND_URL`     | `WordCard.tsx`  | `http://backend:8080`    | Server-side backend URL (Docker)    |
| `VITE_BACKEND_URL`| `SearchBar.tsx` | `http://localhost:8080`  | Client-side backend URL (browser)   |

### Routing
- `/` → `routes/index.tsx` → renders `<WordCard word="meaningful" />` (word of the day placeholder)
- `/{word}` → `routes/[word].tsx` → renders `<WordCard word={params.word} />`

### URL encoding convention
Spaces in multi-word entries are encoded as underscores in the URL path. The backend handler converts `_` back to space before querying Postgres.

---

## Backend Code Conventions

- **No external router** — uses `net/http` mux with Go 1.22+ method routing (`GET /api/word/{word}`)
- **Path values** via `r.PathValue("word")` (Go 1.22+)
- **Connection pooling** via `pgxpool.Pool`
- **Error responses** are always `{"error": "message"}` JSON
- **Empty collections** always return `[]` not `null` (nil slices converted before writing)
- **Graceful shutdown** with 5s timeout on SIGINT/SIGTERM

---

## Key Dependencies

### Backend (`go.mod`)
| Package                        | Version | Purpose                          |
|-------------------------------|---------|----------------------------------|
| `github.com/jackc/pgx/v5`     | v5.9.2  | PostgreSQL driver + connection pool |
| `github.com/redis/go-redis/v9`| v9.19.0 | Redis client (pipeline + raw commands for RediSearch) |

### Frontend (`package.json`)
| Package                       | Version       | Purpose                        |
|------------------------------|---------------|--------------------------------|
| `@solidjs/start`             | 2.0.0-alpha.2 | SolidStart SSR framework       |
| `solid-js`                   | ^1.9.5        | Reactive UI primitives         |
| `vite`                       | ^7.0.0        | Build tool + dev server        |
| `@solidjs/router`            | ^0.16.1       | Client-side routing            |
| `openapi-typescript`         | ^7.13.0       | Dev tool: generate `schema.ts` |

---

## Common Development Tasks

### Add a new API endpoint
1. Add the route to `backend/meaningful.yaml` (OpenAPI spec)
2. Add handler method to `backend/internal/api/handlers.go`
3. Register route in `backend/cmd/api/main.go` mux
4. Run `cd frontend && pnpm run gen-types` to update TypeScript types

### Add a word to the ETL allowlist
Edit `backend/data/allowlist.txt` — one word per line, `#` for comments. Re-run ETL to apply.

### Inspect Redis autocomplete data
```bash
docker compose exec redis redis-cli FT.SUGGET dict_autocomplete "hel" MAX 10
```

### Inspect Postgres data
```bash
docker compose exec postgres psql -U postgres -d meaningful -c "SELECT word, frequency FROM words WHERE LOWER(word) = 'hello';"
```

---

## Known Limitations & Notes

- **ETL is manual** — there is no automatic re-ingestion. Run it explicitly.
- **CORS is open** (`*`) — intentional for local dev. Tighten for production.
- **`routes/index.tsx`** renders a hardcoded "meaningful" word card. "Word of the Day" feature is not yet implemented (see `docs/backlog.md`).
- **`GetMultipleWords`** bypasses Redis cache — noted as a future improvement.
- **Etymology** only shows `etymologies[0]` in `WordCard.tsx` — older entries can have multiple etymologies, only the first is displayed.
- **`SolidStart 2.0.0-alpha.2`** is pre-stable — API may change on upgrades.

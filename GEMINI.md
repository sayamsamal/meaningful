# Meaningful - Dictionary Monorepo

## Purpose
"Meaningful" is a fast, comprehensive English dictionary application. It uses data extracted from Wiktionary (via wiktextract) to provide definitions, synonyms, antonyms, and etymology.

## Architecture
- **Frontend**: SolidStart (TypeScript, Vite)
- **Backend**: Golang (Go 1.26)
- **Primary Database**: PostgreSQL (Stores full relational and structured JSONB data)
- **Cache/Autocomplete**: Redis Stack (RediSearch for fast prefix queries & LRU JSON caching)
- **Orchestration**: Docker Compose

## Key Directories
- `frontend/` - SolidStart web app.
- `backend/` - Go API server and ETL scripts.
- `backend/cmd/api` - API entrypoint.
- `backend/cmd/etl` - ETL pipeline entrypoint for parsing Wiktextract data.
- `docs/` - Project documentation and backlog.

## Local Setup
Ensure Docker is installed and running.
1. Run `docker compose up -d`
2. Access the frontend at `http://localhost:5173`
3. Access the backend at `http://localhost:8080`
4. Access Redis UI at `http://localhost:8001`

To run the ETL pipeline:
```bash
docker compose exec backend go run cmd/etl/main.go
```

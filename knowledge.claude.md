# Knowledge Base — Meaningful

> **Maintenance rule:** At the end of every Claude Code session that produces a significant finding, design decision, or resolved pitfall, append a new entry to the relevant section below. Never delete entries — amend or annotate them if they become outdated. Date every new entry `YYYY-MM-DD`.

---

## Project Architecture Log

Migrated from `LEARNINGS.gemini.md` (Gemini PRAR workflow). Each entry is a completed task.

### Monorepo Setup
SolidStart + Go + Docker Compose monorepo. Chose `redis/redis-stack` image (not plain Redis) because RediSearch is required for `FT.SUGADD` autocomplete. Postgres added after initial scaffold. Port mapping verified end-to-end before moving on.

### ETL Pipeline
Streams + decompresses `kaikki.org` JSONL (~2.4 GB) on-the-fly — no disk writes. Batch-inserts via `pgx.CopyFromRows` (2000 rows/batch) and `go-redis` Pipeline. Two early bugs fixed: `REDIS_URL` needed `redis://` scheme; `go-redis` pipeline required explicit `json.Marshal` before insertion (it does not auto-serialize maps).

### POS Consolidation
Wiktextract emits one entry per POS per word, sequentially. A state-machine parser in `StreamAndProcess` accumulates all POS senses in memory and flushes when the word changes. This produces a single `WordEntry` per word with a `map[pos][]Definition` senses field stored as JSONB.

### Dictionary Word Filter (`passesDictionaryWordFilter`)
~1.5% of entries (21K/1.36M) were junk (symbols, non-Latin scripts). Solution: regex (`^[A-Za-z][A-Za-zÀ-ſ0-9 '-]{0,44}$`) + external allowlist (`backend/data/allowlist.txt`, `#`-comments supported) for legitimate exceptions (101, Q&A, résumé, etc.). Allowlist is loaded at `init()` — editable without recompiling.

### Etymology Cleaning (`cleanEtymology`)
Raw `etymology_text` is prefixed with a large "Etymology tree" ASCII diagram. Fragile string heuristics ("Inherited from", "Cognates") were rejected. Instead: parse `etymology_templates[].expansion` from the Wiktextract JSON and exactly subtract those strings from the start of `etymology_text`. Then take only the first `\n`-delimited paragraph to drop trailing "Cognates / Descendants" sections. 100% precision, zero false positives.

### Redis Autocomplete + Word Frequencies
Storing 1.3M full JSON payloads in Redis was wasteful. Replaced with `FT.SUGADD dict_autocomplete <word> <zipf_score>` — Redis only holds the prefix suggestion index. Zipf frequencies come from a one-time Python `wordfreq` export (`backend/scripts/export_wordfreq.py` → `backend/data/wordfreq.json`). Go loads the JSON into a map at startup for O(1) lookups during streaming. ETL never leaves Go at runtime.

### REST API Design
Read-heavy dictionary → REST over ConnectRPC. Standard `net/http` (Go 1.22+) handles `GET /api/word/{word}`, `GET /api/words`, `GET /api/autocomplete` without a third-party router. Two-tier caching: Redis read-through (24h TTL for definitions, 60s for autocomplete) → Postgres fallback. `Cache-Control: public` headers enable edge CDN caching.

---

## AI Enrichment Pipeline

### What It Does
`backend/cmd/enrich/main.go` — fetches raw `WordEntry` records from Postgres and enriches them via Gemini API into a polished format:
- `etymologies []string` → `origin_story string` (single readable narrative)
- `senses map[string][]Definition` → `[]SenseGroup{POS, Definitions}` (deduplicated, consolidated)
- `examples` → optimized to 1–3 modern examples per definition
- New field: `fun_fact string` (≤20 words of trivia)

Output: timestamped CSV (`enriched_YYYY-MM-DDTHH-MM-SS.csv`), one row per definition.

---

### Backend Migration — Gemini/Gemma → NVIDIA NIM Llama 3.3 70B (2026-05-27)

The Gemma 4 31B path (via `google.golang.org/genai`) was abandoned after repeated failures: `ThinkingConfig` rejection, markdown-fenced JSON, and recurring parse errors. Enrichment now runs on **NVIDIA NIM `meta/llama-3.3-70b-instruct`** via a plain `net/http` client (no SDK). The `google.golang.org/genai` dependency was removed.

| Item | Value |
|------|-------|
| Endpoint | `POST https://integrate.api.nvidia.com/v1/chat/completions` (OpenAI-compatible) |
| Auth | `Authorization: Bearer $NVIDIA_API_KEY` (key in `backend/.env`) |
| Model ID | `meta/llama-3.3-70b-instruct` |
| Context window | 128K tokens, **shared** input + output |
| Sampling used | temp=0.5, top_p=0.9 |
| `max_tokens` | **16,384** — must be set explicitly or responses truncate (`finish_reason: "length"`) |
| Rate limit | 40 RPM (user tier); **no documented RPD** → `DailyMax = 0` (unlimited) |

**Client + structured output (2026-05-27):** Calls go through the **`github.com/sashabaranov/go-openai`** SDK (`DefaultConfig` + `cfg.BaseURL = integrate.api.nvidia.com/v1` + `NewClientWithConfig`), using the standard OpenAI **`response_format: {type: json_schema}}`** path — NVIDIA NIM (vLLM engine) supports it. Requests are **streamed** (`CreateChatCompletionStream`, accumulate `Delta.Content`) to dodge the gateway's 504 on long generations; structured json_schema decoding still applies to streamed tokens. Schema is built with `github.com/sashabaranov/go-openai/jsonschema` (`Definition.MarshalJSON` is a **pointer receiver** → the `Schema` field needs `*jsonschema.Definition`).

**Object root is mandatory.** Per the [vLLM structured-outputs docs](https://docs.vllm.ai/en/stable/features/structured_outputs/), guided decoding enforces the schema, but the root must be an **object** — a top-level array root is *not* reliably enforced, which let Llama 3.3 70B drift to returning `definitions` as bare strings (→ `cannot unmarshal string into …EnrichedDefinition`). Fix: wrap results as `{ "entries": [ <entry> ] }` and parse into `struct{ Entries []EnrichedWordEntry }`. All objects set every property `required` + `additionalProperties:false`, `Strict: true`.

vLLM also recommends restating the schema/field shapes **in the prompt** even with guided decoding — [prompt.go](backend/internal/enrich/prompt.go) spells out the structure with an inline example. A tolerant `EnrichedDefinition.UnmarshalJSON` (bare string → `{sense, examples:nil, subsenses:nil}`) plus `jsonrepair.RepairJSON` remain as cheap backstops.

**`enriched_senses` structure (2026-05-29):** a two-level sense hierarchy. Note `senses` is an array at two nesting levels:
```
entries[] → { word, origin_story, senses[] }     # senses[] = pos-groups
  pos-group → { pos, senses[] }                   # senses[] = meanings
    meaning  → { sense, examples[], subsenses[] } # sense = text (singular)
      subsense → { sense, example }               # example = single string
```
Rules baked into the prompt: preserve every input POS group with verbatim labels; **group** meanings that share a common main idea into one meaning whose `sense` is that idea, `examples` = `[]`, and one `subsense` per variant (each subsense gets **exactly 1** example); a standalone **leaf** meaning keeps 1–3 `examples` and `subsenses: []`; never drop a distinct meaning, never force unrelated meanings under one umbrella. Strict json_schema means empty arrays are emitted rather than omitted.

**Batch sizing (revised 2026-05-30):** With a 128K shared context, the binding constraint is **output reliability**, not input space — there's huge headroom. Measured over 770 enriched rows, enrichment **shrinks** content (output/input char ratio **0.89 avg, 1.27 p90**), because origin-story synthesis + sense consolidation compress more than generated examples expand. Adaptive **60,000-char input budget** (~17K tokens) + system prompt (~715 tok) + `maxOutputTok = 32_768` → ~51K tokens total (40% of 128K), with the p90 output (~22K tok) safely under the 32K cap. Yields ~94 typical words/batch (1–few for the heavy high-frequency head). Rate is the bottleneck, not volume: **5 workers under a 30 RPM limiter** (the hosted free tier's effective ceiling is below the advertised 40). Bigger batches were the lever to cut total calls (~halved) since the rate can't rise. Caveat: light/sparse words (avg ~632 chars) may expand more proportionally (examples generated from scratch), so keep the output cap generous — watch for `finish_reason=length`.

The Gemma 4 31B specs below are retained for historical reference.

---

### Enrichment fields exposed via API + rendered in frontend (2026-06-25)

`database.WordEntry` now carries `OriginStory string`, `EnrichedSenses json.RawMessage`, `DataEnriched bool` (`json:"...,omitempty"` on the first two; the bool always serializes) alongside the original six fields. Both `GetWord` and `GetMultipleWords` select `COALESCE(origin_story, '')`, `enriched_senses`, `data_enriched` in addition to the original columns. `meaningful.yaml` gained matching `EnrichedSenseGroup`/`EnrichedDefinition`/`SubDefinition` schemas plus the three new `WordEntry` properties — regenerate frontend types with `pnpm run gen-types` after touching this spec.

`EnrichedSenses` is typed `json.RawMessage`, not the `enrich` package's `[]SenseGroup`: `enrich` already imports `database`, so `database` importing back would cycle. The OpenAPI spec is the frontend's source of typing truth for this field, not the Go struct.

Frontend (`WordCard.tsx`): when `data_enriched = true`, `origin_story` replaces `etymologies[0]` and `enriched_senses` replaces the raw `senses` map (not shown alongside — enrichment is treated as a full upgrade of the raw data, not an addition to it). A small badge (`.word-card__enriched-badge`) renders next to the title. Un-enriched words render through the original raw-data path unchanged.

**Gotcha:** `HandleGetWord`'s 24h Redis read-through cache (`cache:word:<word>`) stores whatever shape was cached at write time. A word cached before a `WordEntry` serialization change (or before its row was enriched) keeps serving the old shape for up to 24h.

To invalidate it, scope the delete to the cache keys only — **do not run `redis-cli FLUSHALL`**: the same Redis instance also holds the `dict_autocomplete` RediSearch suggestion trie (`FT.SUGADD`), which `FLUSHALL` wipes too, taking down the search bar's autocomplete with no error (just empty results) until the dictionary is rebuilt. Rebuilding it requires either a full ETL re-run (slow, and `BatchInsertWords`'s `CopyFrom` has no upsert/conflict handling — it would duplicate every row in `words`) or a standalone read of `(word, frequency)` from Postgres piped into `RedisDB.BatchInsertAutocomplete`. Scoped invalidation:
```
docker compose exec redis redis-cli --scan --pattern 'cache:word:*' | xargs -r docker compose exec -T redis redis-cli UNLINK
```

---

### Model — Gemma 4 31B Verified Specs (2026-05-26)

Verified against: [Google model card](https://ai.google.dev/gemma/docs/core/model_card_4), [Ollama](https://ollama.com/library/gemma4:31b), [HuggingFace](https://huggingface.co/google/gemma-4-31B).

| Specification | Value | Notes |
|---------------|-------|-------|
| API model ID | `gemma-4-31b-it` | `-it` = instruction-tuned; dense (not MoE) |
| Parameters | 30.7B, 60 layers | |
| **Context window** | **256K input tokens** | 128K applies only to smaller E2B/E4B variants — not 31B |
| Max output tokens | **No hard cap documented** | `GenerateContentConfig.MaxOutputTokens` is a plain `int32`; none of the three sources state a ceiling |
| Sliding window (local attn) | 1,024 tokens | Interleaved with full global attention layers |
| Modalities | Text + Image → Text | Audio only on E2B/E4B; 31B has no audio |
| Training data cutoff | January 2025 | Post-cutoff `fun_fact` claims will be hallucinated |
| Reasoning mode | Not exposed via Gemini API | **Do not set `ThinkingConfig` at all** — the API returns `400 INVALID_ARGUMENT: Thinking budget is not supported for this model.` Even `ThinkingBudget: 0` is rejected (verified 2026-05-26). The `<|think|>` token exists in the open-weights model but is not surfaced through Google AI Studio's endpoint. |
| Vocabulary | 262K tokens | |
| Recommended sampling | temp=1.0, top-p=0.95, top-k=64 | Google's own defaults from model card |

**Sliding window + long batches:** The 1,024-token local attention window is interleaved with global layers. Batches up to ~100K input tokens should have adequate cross-entry coherence via global layers. Batches significantly beyond that risk mid-sequence inconsistency.

---

### Structured Output — `senses` Must Be an Array, Not a Map (2026-05-26)

When using `ResponseSchema` in the Google GenAI SDK, `senses` cannot be expressed as `TypeObject` with dynamic keys — the SDK has no `AdditionalProperties` field. A bare `TypeObject` causes the model to emit `"senses": {}` every time, leaving all `pos`, `definition`, and `examples` CSV columns blank.

**Fix:** use `[]SenseGroup` — a `TypeArray` of typed `TypeObject` items, fully expressible in the schema:

```go
type SenseGroup struct {
    POS         string               `json:"pos"`
    Definitions []EnrichedDefinition `json:"definitions"`
}
```

---

### Token Estimation Methodology (2026-05-26)

Tokenizer assumption: **1 token ≈ 3.5 chars** (Google SentencePiece on English prose + JSON).

```
Input tokens/word  = (etym_chars + senses_chars + syn_chars + ant_chars + word_len + 200) / 3.5
Output tokens/word = (senses_chars × 0.6 + 500) / 3.5
```

`+200` = JSON field-name overhead; `×0.6` = ~40% reduction from definition consolidation; `+500` = `origin_story` + `fun_fact` + structural chars.

**Top-100 most frequent words** (representative of the heaviest entries):

| Metric | Value |
|--------|-------|
| System prompt (fixed/request) | ~500 tokens |
| Average input / word | 2,236 |
| Median input / word | 1,897 |
| Min — `has` | 177 |
| Max — `go` | 9,529 (28,763-char senses; all phrasal forms) |
| Average output / word | 1,301 |
| Average total / word | ~3,537 |

These are extreme outliers. Across all 128K words, the average drops to 255 input tokens — a 9× difference (see dataset section below).

---

### Full Dataset — Enrichment Planning (2026-05-26)

#### Dataset (`WHERE frequency > 0`)

| Metric | Value |
|--------|-------|
| Total words with frequency score | **128,144** |
| Full data (senses + etymologies populated) | **72,242** (56.4%) |
| Sparse (senses or etymologies empty) | **55,902** (43.6%) |
| Avg raw chars / word | 693 |
| Median raw chars / word | 247 |
| P90 | 1,639 chars |
| P99 | 6,520 chars |
| Max — `take` | 43,573 chars |
| **Avg input tokens / word** | **255** |
| **Avg output tokens / word** | **241** |
| Grand total tokens (in + out) | **~63.5M** |

#### Token-size distribution

| Tier | Words | % | Avg input tok |
|------|-------|---|--------------|
| Simple (<1K tokens) | 123,506 | 96.4% | ~199 |
| Medium (1K–3K) | 4,263 | 3.3% | ~1,554 |
| Complex (3K–8K) | 367 | 0.3% | ~3,968 |
| Giant (8K+) | 8 | 0.006% | ~9,757 |

#### API limits (Google AI Studio free tier)

| Limit | Value | Binding? |
|-------|-------|---------|
| RPM | 15 | Pacing only |
| TPM (input) | Unlimited | Not a constraint |
| **RPD** | **1,500** | **Yes — daily ceiling on requests** |

#### Output token cap — no hard limit, reliability-driven choice

Earlier analysis assumed 8,192 output tokens (the default for older Gemini 1.5/2.0 models). **This was wrong for Gemma 4 31B.** No output cap is documented in any of the three authoritative sources. `MaxOutputTokens` is a plain `int32` in the SDK with no SDK-enforced ceiling.

The practical cap is a **reliability trade-off** — very large JSON outputs are prone to schema drift:

| `MaxOutputTokens` | Words/batch | Input tok/batch | Requests needed | Schedule |
|------------------|------------|----------------|----------------|---------|
| 32,000 | 132 | ~33,660 | 971 | **Single day, 65 min active** |
| 64,000 | 265 | ~67,575 | 484 | Single day, 32 min active |
| 128,000 | 531 | ~135,405 | 242 | Single day, 16 min active |

**Recommendation: `MaxOutputTokens = 32_000` (132 words/batch)**
- Completes all 128K words in one day (971 of 1,500 RPD used; 529 spare)
- Input stays at ~34K tokens — comfortably within the 256K window
- A failed batch costs only 132 words, not 531
- 32K output avoids schema drift observed in longer structured generations

**Full-data words only (72,242):** 548 requests → single day, ~37 min active.

#### Processing order and implementation notes

- **Sort by frequency DESC** — highest-value words enriched first; if interrupted, the most important entries are done
- **Adaptive batching by char budget** — accumulate words until `sum(raw_chars) > 115,000` (≈33K input tokens ≈ 132 avg words); this subsumes the old tiered approach without explicit tier logic
- **Pacing** — sleep `max(0, 4_000ms − elapsed_ms)` between requests (targets 15 RPM); exponential back-off on HTTP 429
- **Daily quota guard** — stop gracefully at request count ≥ 1,490 (10-request buffer below 1,500 RPD)
- **State tracking** — persist enriched word IDs immediately after each successful batch (Postgres column or flat file); resume query: `WHERE frequency > 0 AND id NOT IN (enriched_ids)`
- **Giant-word handling** — words with `total_chars > 100,000`: batch alone, truncate etymology to 200 chars (not 600)
- **Do not set `ThinkingConfig`** — Gemma 4 31B via Gemini API rejects the field entirely (`400 INVALID_ARGUMENT`); the reasoning-token concern from the open-weights model card doesn't apply through this endpoint
- **Sampling params** — set temp=1.0, top-p=0.95, top-k=64 per Google's model card defaults

#### Context timeout
Set to **30 minutes** minimum. Small batches (10 words) complete in 2–5 min. The original 10-minute timeout was reliably exceeded for batches of 50+ top-100 words; those are the heaviest words in the dataset and not representative of typical throughput.

---

## Standing Pitfalls

| Pitfall | Root Cause | Fix |
|---------|-----------|-----|
| `senses` always `{}` in enriched output | `TypeObject` with no `Properties` in `ResponseSchema` gives model no guidance | Use `TypeArray` of typed `SenseGroup` items |
| `context deadline exceeded` on API call | Prompt too large (>200KB) for context window; 10-min timeout too short | Truncate etymologies to 600 chars; use 30-min timeout; batch ≤25 words |
| `genai.BackendGoogleAI` undefined | Constant renamed in `google.golang.org/genai` v1.x | Use `genai.BackendGeminiAPI` |
| `400 INVALID_ARGUMENT: Thinking budget is not supported for this model` | Setting `ThinkingConfig` (even `ThinkingBudget: 0`) on Gemma 4 31B requests | Omit `ThinkingConfig` from `GenerateContentConfig` entirely |
| `invalid character '` + "`" + `' after top-level value` on enrich response | Gemma 4 31B occasionally wraps structured-output JSON in ` ```json … ``` ` Markdown fences despite `ResponseMIMEType`/`ResponseSchema` | Run the response through `github.com/RealAlexandreAI/json-repair` (`jsonrepair.RepairJSON`) before `json.Unmarshal` — strips fences and patches other minor malformations |
| `cannot unmarshal string into …EnrichedDefinition` (NIM/vLLM) | `response_format: json_schema` with a **top-level array root** isn't strictly enforced by vLLM guided decoding, so the model returns `definitions` as bare strings | Use an **object-rooted** schema (`{entries:[…]}`); restate field shapes in the prompt; keep a tolerant `UnmarshalJSON` backstop |
| HTTP 429 Too Many Requests early in an enrich run | `rate.NewLimiter(..., burst)` with `burst == RPM` starts with a full bucket, so all workers fire at once — a startup surge that trips the server's rolling-window limit | Set limiter **burst = 1** for even pacing; target a few % under the stated RPM cap (38 of 40) for rolling-window headroom |
| HTTP 429 persists despite a paced limiter | **Retries bypassed the limiter** — `limiter.Wait` ran once per batch in the worker, but the retry loop fired up to 6 `EnrichBatch` calls ungated, so retries (from 504s/429s) pushed the true rate over RPM and snowballed | Acquire a token **inside the retry loop**, before every attempt (and count each attempt), so all API calls — initial + retries — are rate-bounded |
| HTTP 429 still appears at a correctly-gated ~38 RPM | The hosted NVIDIA free tier's **effective** sustained ceiling is below the advertised 40 RPM (and/or concurrent streaming connections count against a separate limit) | Run with headroom: `RPM=30`, `Workers=5`. If still 429ing, step down (20 RPM / 3 workers) or fan out across multiple API keys (one limiter per key) |
| `504 Gateway Timeout` (`unexpected end of JSON input`) on enrich requests | NIM gateway gives up waiting for the 70B to finish a long/verbose generation (known issue on the hosted endpoint); retries don't help if a batch consistently exceeds the window | **Stream** the completion (`CreateChatCompletionStream`, accumulate `Delta.Content`) — continuous SSE keeps the connection alive so the gateway never times out |
| `stream recv: context deadline exceeded` on large batches | A fixed per-request total deadline (`context.WithTimeout`) kills a *healthy but slow* long stream — a near-full 32K-token generation on the slow free tier legitimately runs past 10 min | Replace the total deadline with a **per-chunk idle timeout**: a watchdog (own timer, `WithCancelCause`) resets on every token chunk and cancels only after `streamIdleTimeout` (120s) of zero tokens — tolerates any generation length, still fails fast on real stalls |
| Enriched output drops POS groups / distinct senses (e.g. `me`: det+noun+pron → one `pronoun`) | Prompt told the model to "merge / discard redundant entries"; the 70B over-applies it and collapses distinct POS + senses. JSON schema can't enforce "same POS set as input" (data-dependent) | Prompt must demand: preserve every input POS group, copy `pos` labels **verbatim**, keep every distinct sense, merge only true duplicate restatements |
| Case-variant rows wrongly enriched (`ME` populated when only `me` has frequency>0) | `Persist` updated `WHERE LOWER(word) = LOWER($word)`, clobbering all case siblings even though the fetch filtered `frequency>0` | Carry the row `id` from fetch → persist; `UPDATE … WHERE id = $id`; map enriched→source by word (exact, then case-insensitive within batch) |
| JSONB scan into `map[string][]T` silently works in pgx v5 | pgx v5 uses `encoding/json.Unmarshal` as fallback for JSONB → Go types | No fix needed — just document that it works without explicit type registration |
| `/of` and `/OF` both resolve to "OF" the initialism instead of the distinct entries (2026-06-25) | Case-collided rows coexist (`of` freq 7.4 + `OF` freq 0; also `a`/`A`, `it`/`It`/`IT`, `nasa`/`Nasa`/`NASA`). `HandleGetWord` lowercased the URL word, then `GetWord` ran `WHERE LOWER(word)=$1 LIMIT 1` with **no `ORDER BY`** — Postgres returned whichever row the index scan hit first ("OF"). | Stop lowercasing in the handler (preserve URL casing for the cache key + lookup); change the query to `WHERE LOWER(word)=LOWER($1) ORDER BY (word=$1) DESC, frequency DESC NULLS LAST, word LIMIT 1` — exact-case match wins, else most common casing, `word` is a deterministic tiebreak for equal frequencies. Apply the same dedupe (`DISTINCT ON (LOWER(word)) … ORDER BY LOWER(word), frequency DESC`) to `GetMultipleWords`. Flush stale `cache:word:*` keys after deploy (they held wrong payloads under lowercased keys). **Autocomplete needed no change**: FT.SUGADD preserves case (both `of`/`OF` stored), FT.SUGGET prefix-match is case-insensitive and already ranks by frequency, so the common casing surfaces on top and rare uppercase variants are reachable only by typing the URL. |
| Autocomplete can't surface a low-frequency exact-case word (`OF`, `NASA` typed in the search bar) (2026-06-25) | `dict_autocomplete` is a `trietype0` FT.SUGGET trie: **case-insensitive prefix matching ranked purely by frequency**, with no case-sensitive mode and no exact-membership lookup. So `OF`/`of` return the identical list and the exact uppercase `OF` is buried at **rank 128 of 1119** for the `of` prefix — past the `MAX 10` fetched. Widening the fetch + re-ranking is unreliable (rank depth varies; short prefixes have 30k+ candidates). `cache:word:*` is lazily populated so useless as a membership oracle. | In `HandleAutocomplete`: preserve the typed casing (`raw`), use its lowercase only for the FT.SUGGET prefix, then if `suggestions[0] != raw` do a **case-sensitive** `ExactWordExists(raw)` (`SELECT word FROM words WHERE word=$1`, indexed by `idx_words_word`) and **prepend** the exact word. Gated so the hot lowercase path (`of` already rank 1) never hits Postgres. Chose this over a new Redis `SADD dict_words` SET to avoid an ETL change + backfill of existing data. |
| Frontend shows `Error loading "…": fetch failed`; `backend` container `Exited (1)` (2026-06-17) | `ConnectRedis` (`backend/internal/database/redis.go`) did a single `Ping` with no retry. On startup, `redis-stack` was still restoring its persisted RDB dataset and returned `LOADING Redis is loading the dataset in memory` — a normal transient state, not a real failure. The single failed ping made `main.go` call `log.Fatalf`, and `docker-compose.yml` has no `restart` policy on `backend`, so it stayed dead | Added a retry loop to `ConnectRedis` (30 attempts, 1s apart) that tolerates `LOADING` and any other transient ping failure instead of crashing once. Consider also adding `restart: unless-stopped` to the `backend` service for defense in depth |

---

## Deployment & Hosting Log

Production deployment: Cloudflare Workers (frontend SSR) + managed Postgres and Azure Container Apps (backend). Date every new entry `YYYY-MM-DD`.

### Managed Postgres via DATABASE_URL (2026-07-03)
The API assembled its DSN from the `POSTGRES_*` parts with no `sslmode`, so it could not reach managed Postgres that mandates TLS (Azure Flexible Server, `sslmode=require`). `cmd/api/main.go` now prefers a full `DATABASE_URL` connection string when set and falls back to the assembled DSN for local docker-compose dev, so the same binary works in both.

### Multi-stage production backend image (2026-07-03)
`backend/Dockerfile` is a multi-stage build: a static `CGO_ENABLED=0` binary compiled on `golang:alpine`, copied into a minimal `alpine` runtime with `ca-certificates` (required for the managed-Postgres TLS handshake), running as a non-root user.

### SolidStart on Cloudflare Workers — use the official nitro/vite plugin (2026-07-03)
SolidStart 2.0-alpha's `@solidjs/vite-plugin-nitro-2` generates broken Cloudflare output (solid-start#2115): the worker builds but throws "Illegal invocation" in `sendWebResponse` at runtime — a 500 on every route. Fix: switch to Nitro's official vite plugin with `exportConditions: ["worker"]` (the maintainer-confirmed workaround), which resolves worker-safe builds of dependencies. Nitro v3 also emits `.output/server/wrangler.json` (main, assets binding, `nodejs_compat`) and redirects wrangler to it, so `wrangler.toml` is trimmed to just name + compat settings and `npx wrangler deploy` works as-is.

### CI — backend build & deploy (2026-07-07)
`.github/workflows/backend.yml`: on push to `main` touching `backend/**`, build the Go image, push it to `ghcr.io` via the built-in `GITHUB_TOKEN`, then roll a new Azure Container Apps revision (`az containerapp update`) using the `AZURE_CREDENTIALS` secret.

### pnpm pinning + local Nitro dev port (2026-07-08)
Pin pnpm via `package.json` `"packageManager"` so corepack drives the version everywhere (Docker, host, Cloudflare CI). pnpm-latest hard-fails `pnpm install` on esbuild's ignored build script (`ERR_PNPM_IGNORED_BUILDS`) and ignores the package.json allow-list, so also declare `pnpm.onlyBuiltDependencies` for esbuild. After the Nitro/Vite switch the local dev server listens on **3000** (not 5173); docker-compose maps host 5173 -> container 3000.

### process.env on deployed Cloudflare Workers (2026-07-08)
On the deployed Worker, SSR `process.env.BACKEND_URL` (in WordCard) resolved empty because `compatibility_date` predated `nodejs_compat` auto-populating `process.env` from the Worker's bindings, so SSR fell back to `http://backend:8080` and returned 403. Bump `compatibility_date` to >= 2025-04-01 to turn on `nodejs_compat_populate_process_env`, and add `[vars] BACKEND_URL` (the API origin) as the server-side runtime binding. `VITE_BACKEND_URL` stays a build-time var for the browser.

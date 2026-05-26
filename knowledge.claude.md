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
| Reasoning mode | Optional `<|think|>` token | **Must be disabled for JSON generation** — thinking tokens precede output and break `json.Unmarshal` |
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
- **Disable reasoning mode** — set `ThinkingConfig` to off; otherwise `<|think|>` tokens prepend the JSON and break unmarshalling
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
| JSONB scan into `map[string][]T` silently works in pgx v5 | pgx v5 uses `encoding/json.Unmarshal` as fallback for JSONB → Go types | No fix needed — just document that it works without explicit type registration |

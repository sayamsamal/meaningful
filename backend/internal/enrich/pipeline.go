package enrich

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/schollz/progressbar/v3"
	"golang.org/x/time/rate"

	"meaningful-backend/internal/database"
)

type PipelineConfig struct {
	Workers    int // parallel API calls (default 5)
	RPM        int // rate ceiling (default 30)
	DailyMax   int // request cap per run; 0 = unlimited (NVIDIA tier has no RPD)
	CharBudget int // adaptive batch size in raw input chars (default 60_000)
	FetchLimit int // SQL LIMIT for next-batch query (default 500)
	MaxRetries int // retries per batch on transient errors (default 5)
}

func DefaultConfig() PipelineConfig {
	return PipelineConfig{
		Workers:        10,
		RPM:            35,
		DailyMax:       0,
		CharBudget:     60_000,
		FetchLimit:     500,
		MaxRetries:     5,
	}
}

// RunPipeline processes every word with frequency > 0 and data_enriched = false,
// enriching them in batches under the configured rate limit. The state of the
// run lives entirely in Postgres — interrupt and restart is safe.
func (s *Service) RunPipeline(ctx context.Context, cfg PipelineConfig) error {
	remaining, err := s.countRemaining(ctx)
	if err != nil {
		return fmt.Errorf("count remaining: %w", err)
	}
	if remaining == 0 {
		log.Println("nothing to enrich — all frequency>0 words already have data_enriched=true")
		return nil
	}
	log.Printf("resume position: %d words remain to enrich", remaining)

	// Burst 1 paces requests evenly (one per interval). A burst equal to RPM
	// would start with a full bucket and let all workers fire at once, tripping
	// the server's rolling-window 429.
	limiter := rate.NewLimiter(rate.Every(time.Minute/time.Duration(cfg.RPM)), 1)

	var (
		requestsUsed atomic.Int64
		batchesDone  atomic.Int64
		wordsDone    atomic.Int64
		failures     atomic.Int64
	)

	bar := progressbar.NewOptions(remaining,
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionSetDescription("Enriching"),
		progressbar.OptionShowCount(),
		progressbar.OptionShowIts(),
		progressbar.OptionSetItsString("words"),
		progressbar.OptionSetPredictTime(true),
		progressbar.OptionFullWidth(),
		progressbar.OptionThrottle(200*time.Millisecond),
		progressbar.OptionOnCompletion(func() { fmt.Fprintln(os.Stderr) }),
	)

	describe := func() {
		reqField := fmt.Sprintf("%d", requestsUsed.Load())
		if cfg.DailyMax > 0 {
			reqField = fmt.Sprintf("%d/%d", requestsUsed.Load(), cfg.DailyMax)
		}
		bar.Describe(fmt.Sprintf("Enriching | req %s | batches %d | fails %d",
			reqField, batchesDone.Load(), failures.Load()))
	}
	describe()

	batchCh := make(chan []database.WordEntry, cfg.Workers)
	var workersWG sync.WaitGroup
	for i := 0; i < cfg.Workers; i++ {
		workersWG.Add(1)
		go func(id int) {
			defer workersWG.Done()
			for batch := range batchCh {
				enriched, err := s.enrichWithRetry(ctx, limiter, &requestsUsed, batch, cfg)
				if err != nil {
					failures.Add(1)
					_ = bar.Clear()
					log.Printf("[worker %d] batch failed after retries (%d words): %v", id, len(batch), err)
					continue
				}
				if err := s.Persist(ctx, batch, enriched); err != nil {
					failures.Add(1)
					_ = bar.Clear()
					log.Printf("[worker %d] persist failed (%d words): %v", id, len(batch), err)
					continue
				}
				batchesDone.Add(1)
				wordsDone.Add(int64(len(batch)))
				_ = bar.Add(len(batch))
			}
		}(i)
	}

	// Refresh the bar's description every 5s so request/failure counters stay live
	// even while no batches are completing.
	progressCtx, stopProgress := context.WithCancel(ctx)
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-progressCtx.Done():
				return
			case <-t.C:
				describe()
			}
		}
	}()

	// Fetcher: dispatches batches to workers until quota hit, no more work, or ctx cancelled.
	fetchErr := s.runFetcher(ctx, cfg, &requestsUsed, batchCh)

	close(batchCh)
	workersWG.Wait()
	stopProgress()
	_ = bar.Finish()

	log.Printf("pipeline finished: %d batches, %d words enriched, %d failures, %d daily requests used",
		batchesDone.Load(), wordsDone.Load(), failures.Load(), requestsUsed.Load())

	if fetchErr != nil && !errors.Is(fetchErr, context.Canceled) {
		return fetchErr
	}
	return nil
}

func (s *Service) runFetcher(ctx context.Context, cfg PipelineConfig, requestsUsed *atomic.Int64, out chan<- []database.WordEntry) error {
	// Track the last fetched frequency to skip words that are already in flight
	// or queued. Workers commit data_enriched=TRUE after successful persist, so
	// re-querying would otherwise return the same rows.
	queued := make(map[string]struct{})

	for {
		if cfg.DailyMax > 0 && requestsUsed.Load() >= int64(cfg.DailyMax) {
			log.Printf("daily quota reached (%d) — stopping fetcher", cfg.DailyMax)
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		rows, err := s.fetchCandidates(ctx, cfg.FetchLimit, queued)
		if err != nil {
			return fmt.Errorf("fetch candidates: %w", err)
		}
		if len(rows) == 0 {
			log.Println("no more unenriched words — fetcher exiting")
			return nil
		}

		batch := make([]database.WordEntry, 0, len(rows))
		charSum := 0
		for _, r := range rows {
			c := rawChars(r)
			// Always include at least one word (giant words batch alone)
			if len(batch) > 0 && charSum+c > cfg.CharBudget {
				break
			}
			batch = append(batch, r)
			charSum += c
			queued[r.Word] = struct{}{}
		}

		select {
		case out <- batch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// fetchCandidates pulls the next FetchLimit unenriched words ordered by
// frequency DESC, excluding ones already queued in this run.
func (s *Service) fetchCandidates(ctx context.Context, limit int, exclude map[string]struct{}) ([]database.WordEntry, error) {
	rows, err := s.DB.Pool.Query(ctx, `
		SELECT id, word, frequency, etymologies, senses, synonyms, antonyms
		FROM words
		WHERE frequency > 0 AND NOT data_enriched
		ORDER BY frequency DESC
		LIMIT $1
	`, limit*2) // overfetch so we can skip already-queued without coming up short
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []database.WordEntry
	for rows.Next() {
		var e database.WordEntry
		if err := rows.Scan(&e.ID, &e.Word, &e.Frequency, &e.Etymologies, &e.Senses, &e.Synonyms, &e.Antonyms); err != nil {
			return nil, err
		}
		if _, skip := exclude[e.Word]; skip {
			continue
		}
		out = append(out, e)
		if len(out) >= limit {
			break
		}
	}
	return out, rows.Err()
}

func (s *Service) countRemaining(ctx context.Context) (int, error) {
	var n int
	err := s.DB.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM words WHERE frequency > 0 AND NOT data_enriched`,
	).Scan(&n)
	return n, err
}

// enrichWithRetry retries on transient errors with exponential backoff. Every
// attempt (initial and retries) acquires a rate-limiter token, so retries can't
// push the request rate above the configured RPM.
func (s *Service) enrichWithRetry(ctx context.Context, limiter *rate.Limiter, requestsUsed *atomic.Int64, batch []database.WordEntry, cfg PipelineConfig) ([]EnrichedWordEntry, error) {
	var lastErr error
	backoff := 2 * time.Second
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		if err := limiter.Wait(ctx); err != nil {
			return nil, err
		}
		requestsUsed.Add(1)

		// No total deadline here — EnrichBatch enforces a per-chunk idle timeout
		// internally, so healthy-but-slow long generations aren't killed.
		enriched, err := s.EnrichBatch(ctx, batch)
		if err == nil {
			return enriched, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if attempt < cfg.MaxRetries {
			log.Printf("retry %d/%d after %v: %v", attempt+1, cfg.MaxRetries, backoff, err)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			backoff *= 2
		}
	}
	return nil, fmt.Errorf("exhausted %d retries: %w", cfg.MaxRetries, lastErr)
}

// rawChars estimates the input size contribution of a single word entry.
// Matches the field set sent to the model (word + etymologies + senses).
func rawChars(w database.WordEntry) int {
	n := len(w.Word)
	for _, e := range w.Etymologies {
		n += len(e)
	}
	for pos, defs := range w.Senses {
		n += len(pos)
		for _, d := range defs {
			n += len(d.Definition)
			for _, ex := range d.Examples {
				n += len(ex)
			}
		}
	}
	return n
}

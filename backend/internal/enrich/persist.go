package enrich

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"meaningful-backend/internal/database"
)

// Persist writes enriched entries back to Postgres in a single transaction,
// matching each enriched entry to its source row by primary key. Updating by id
// (rather than LOWER(word)) ensures only the exact fetched rows are touched —
// case-variant siblings (e.g. "Me"/"ME" alongside "me") are never clobbered.
// If any update fails, the whole batch rolls back so the next run retries it.
func (s *Service) Persist(ctx context.Context, source []database.WordEntry, entries []EnrichedWordEntry) error {
	if len(entries) == 0 {
		return nil
	}

	// Map each enriched entry back to its source row id by word: exact match
	// first, then a case-insensitive fallback within this batch.
	exact := make(map[string]int, len(source))
	lower := make(map[string]int, len(source))
	for _, w := range source {
		exact[w.Word] = w.ID
		lower[strings.ToLower(w.Word)] = w.ID
	}

	tx, err := s.DB.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	const stmt = `
		UPDATE words
		SET origin_story = $1,
		    enriched_senses = $2,
		    data_enriched = TRUE
		WHERE id = $3
	`

	for _, e := range entries {
		id, ok := exact[e.Word]
		if !ok {
			id, ok = lower[strings.ToLower(e.Word)]
		}
		if !ok {
			log.Printf("warning: enriched word %q not found in source batch — skipping", e.Word)
			continue
		}

		sensesJSON, err := json.Marshal(e.Senses)
		if err != nil {
			return fmt.Errorf("marshal senses for %q: %w", e.Word, err)
		}
		if _, err := tx.Exec(ctx, stmt, e.OriginStory, sensesJSON, id); err != nil {
			return fmt.Errorf("update %q (id %d): %w", e.Word, id, err)
		}
	}

	return tx.Commit(ctx)
}

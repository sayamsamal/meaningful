package enrich

import (
	"context"
	"encoding/json"
	"fmt"
)

// Persist writes enriched entries back to Postgres in a single transaction.
// If any row fails, the entire batch is rolled back — leaving data_enriched
// false for all of them so the next run will retry the batch.
func (s *Service) Persist(ctx context.Context, entries []EnrichedWordEntry) error {
	if len(entries) == 0 {
		return nil
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
		WHERE LOWER(word) = LOWER($3)
	`

	for _, e := range entries {
		sensesJSON, err := json.Marshal(e.Senses)
		if err != nil {
			return fmt.Errorf("marshal senses for %q: %w", e.Word, err)
		}
		if _, err := tx.Exec(ctx, stmt, e.OriginStory, sensesJSON, e.Word); err != nil {
			return fmt.Errorf("update %q: %w", e.Word, err)
		}
	}

	return tx.Commit(ctx)
}

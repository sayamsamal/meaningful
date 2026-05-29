package database

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Definition struct {
	Definition string   `json:"definition"`
	Examples   []string `json:"examples"`
}

type Senses map[string][]Definition

type WordEntry struct {
	ID          int      `json:"-"`
	Word        string   `json:"word"`
	Frequency   float64  `json:"frequency"`
	Etymologies []string `json:"etymologies"`
	Senses      Senses   `json:"senses"`
	Synonyms    []string `json:"synonyms"`
	Antonyms    []string `json:"antonyms"`
}

type PostgresDB struct {
	Pool *pgxpool.Pool
}

func ConnectPostgres(connString string) (*PostgresDB, error) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("unable to connect to database: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("unable to ping database: %w", err)
	}

	return &PostgresDB{Pool: pool}, nil
}

func (db *PostgresDB) Close() {
	db.Pool.Close()
}

func (db *PostgresDB) InitSchema(ctx context.Context) error {
	query := `
	CREATE TABLE IF NOT EXISTS words (
		id SERIAL PRIMARY KEY,
		word VARCHAR(255) NOT NULL,
		frequency REAL,
		etymologies JSONB,
		senses JSONB,
		synonyms JSONB,
		antonyms JSONB
	);
	CREATE INDEX IF NOT EXISTS idx_words_word ON words (word);
	CREATE INDEX IF NOT EXISTS idx_words_word_lower ON words (LOWER(word));

	ALTER TABLE words ADD COLUMN IF NOT EXISTS origin_story TEXT;
	ALTER TABLE words ADD COLUMN IF NOT EXISTS enriched_senses JSONB;
	ALTER TABLE words ADD COLUMN IF NOT EXISTS data_enriched BOOLEAN NOT NULL DEFAULT FALSE;
	CREATE INDEX IF NOT EXISTS idx_words_unenriched
		ON words (frequency DESC)
		WHERE NOT data_enriched AND frequency > 0;
	`
	_, err := db.Pool.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}
	log.Println("Postgres schema initialized")
	return nil
}

func (db *PostgresDB) BatchInsertWords(ctx context.Context, words []WordEntry) error {
	if len(words) == 0 {
		return nil
	}

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Since there could be a lot of data, we will use individual inserts in a transaction
	// pgx also has a CopyFrom which is highly optimized for bulk inserts.
	// We'll use CopyFrom for max performance.
	
	rows := make([][]any, 0, len(words))
	for _, w := range words {
		rows = append(rows, []any{w.Word, w.Frequency, w.Etymologies, w.Senses, w.Synonyms, w.Antonyms})
	}

	_, err = tx.CopyFrom(
		ctx,
		pgx.Identifier{"words"},
		[]string{"word", "frequency", "etymologies", "senses", "synonyms", "antonyms"},
		pgx.CopyFromRows(rows),
	)

	if err != nil {
		return fmt.Errorf("failed to copy rows: %w", err)
	}

	return tx.Commit(ctx)
}

// ResetEnrichment clears all enrichment output, returning the number of rows
// reset. Used to restart the enrichment pipeline from scratch.
func (db *PostgresDB) ResetEnrichment(ctx context.Context) (int64, error) {
	tag, err := db.Pool.Exec(ctx, `
		UPDATE words
		SET origin_story = NULL, enriched_senses = NULL, data_enriched = FALSE
		WHERE data_enriched = TRUE OR enriched_senses IS NOT NULL OR origin_story IS NOT NULL
	`)
	if err != nil {
		return 0, fmt.Errorf("failed to reset enrichment: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (db *PostgresDB) GetWord(ctx context.Context, word string) (*WordEntry, error) {
	query := `
		SELECT id, word, frequency, etymologies, senses, synonyms, antonyms
		FROM words
		WHERE LOWER(word) = $1
		LIMIT 1
	`
	var entry WordEntry
	err := db.Pool.QueryRow(ctx, query, word).Scan(
		&entry.ID,
		&entry.Word,
		&entry.Frequency,
		&entry.Etymologies,
		&entry.Senses,
		&entry.Synonyms,
		&entry.Antonyms,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil // not found
		}
		return nil, fmt.Errorf("failed to query word: %w", err)
	}

	return &entry, nil
}

func (db *PostgresDB) GetMultipleWords(ctx context.Context, words []string) ([]WordEntry, error) {
	if len(words) == 0 {
		return []WordEntry{}, nil
	}

	// Lowercase in Go to keep the SQL simple
	lowerWords := make([]string, len(words))
	for i, w := range words {
		lowerWords[i] = strings.ToLower(w)
	}

	query := `
		SELECT word, frequency, etymologies, senses, synonyms, antonyms
		FROM words
		WHERE LOWER(word) = ANY($1)
	`
	rows, err := db.Pool.Query(ctx, query, lowerWords)
	if err != nil {
		return nil, fmt.Errorf("failed to query multiple words: %w", err)
	}
	defer rows.Close()

	var results []WordEntry
	for rows.Next() {
		var entry WordEntry
		if err := rows.Scan(
			&entry.Word,
			&entry.Frequency,
			&entry.Etymologies,
			&entry.Senses,
			&entry.Synonyms,
			&entry.Antonyms,
		); err != nil {
			return nil, fmt.Errorf("failed to scan word row: %w", err)
		}
		results = append(results, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating word rows: %w", err)
	}

	return results, nil
}

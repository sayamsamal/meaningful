package database

import (
	"context"
	"fmt"
	"log"

	"github.com/redis/go-redis/v9"
)

type RedisDB struct {
	Client *redis.Client
}

func ConnectRedis(connString string) (*RedisDB, error) {
	opt, err := redis.ParseURL(connString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redis url: %w", err)
	}

	client := redis.NewClient(opt)
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to ping redis: %w", err)
	}

	return &RedisDB{Client: client}, nil
}

func (db *RedisDB) Close() {
	db.Client.Close()
}

func (db *RedisDB) InitSchema(ctx context.Context) error {
	// The RediSearch autocomplete dictionary (FT.SUGADD) does not require explicit schema creation.
	// It is created automatically when the first suggestion is added.
	// We assume the caller runs redis-cli FLUSHALL before the ETL pipeline.
	log.Println("Redis schema initialized (Autocomplete dictionary mode)")
	return nil
}

// BatchInsertAutocomplete pushes word strings and their Zipf frequencies into RediSearch
// for extremely fast, low-memory prefix autocomplete.
func (db *RedisDB) BatchInsertAutocomplete(ctx context.Context, words []WordEntry) error {
	if len(words) == 0 {
		return nil
	}

	pipeline := db.Client.Pipeline()
	
	for _, w := range words {
		// FT.SUGADD dict_autocomplete <word> <score>
		// We use the word as both the suggestion string and we assign its Zipf frequency as the score.
		// If frequency is 0, we can give it a tiny baseline score.
		score := w.Frequency
		if score <= 0 {
			score = 0.0001
		}

		pipeline.Do(ctx, "FT.SUGADD", "dict_autocomplete", w.Word, score)
	}

	_, err := pipeline.Exec(ctx)
	if err != nil {
		return fmt.Errorf("redis pipeline error during FT.SUGADD: %w", err)
	}

	return nil
}

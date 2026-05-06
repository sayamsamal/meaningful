package database

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

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

// GetAutocomplete queries the RediSearch autocomplete dictionary
func (db *RedisDB) GetAutocomplete(ctx context.Context, prefix string, maxResults int) ([]string, error) {
	if prefix == "" {
		return []string{}, nil
	}

	// FT.SUGGET dict_autocomplete <prefix> MAX <maxResults>
	res := db.Client.Do(ctx, "FT.SUGGET", "dict_autocomplete", prefix, "MAX", maxResults)
	if err := res.Err(); err != nil {
		if err == redis.Nil {
			return []string{}, nil
		}
		return nil, fmt.Errorf("redis FT.SUGGET error: %w", err)
	}

	rawSuggestions, err := res.StringSlice()
	if err != nil {
		return nil, fmt.Errorf("failed to parse suggestions: %w", err)
	}

	return rawSuggestions, nil
}

// GetCachedWord fetches a full word entry from Redis
func (db *RedisDB) GetCachedWord(ctx context.Context, word string) (*WordEntry, error) {
	key := fmt.Sprintf("cache:word:%s", word)
	val, err := db.Client.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil // cache miss
		}
		return nil, fmt.Errorf("redis get error: %w", err)
	}

	var entry WordEntry
	if err := json.Unmarshal([]byte(val), &entry); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cached word: %w", err)
	}

	return &entry, nil
}

// SetCachedWord stores a word entry in Redis with a TTL
func (db *RedisDB) SetCachedWord(ctx context.Context, word string, entry *WordEntry, ttl time.Duration) error {
	key := fmt.Sprintf("cache:word:%s", word)
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal word for cache: %w", err)
	}

	if err := db.Client.Set(ctx, key, data, ttl).Err(); err != nil {
		return fmt.Errorf("redis set error: %w", err)
	}

	return nil
}

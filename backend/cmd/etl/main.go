package main

import (
	"context"
	"log"
	"os"

	"meaningful-backend/internal/database"
	"meaningful-backend/internal/parser"
)

func main() {
	log.Println("Starting Dictionary ETL Pipeline...")

	pgConn := os.Getenv("DATABASE_URL")
	if pgConn == "" {
		pgConn = "postgres://postgres:postgres@postgres:5432/meaningful?sslmode=disable"
	}

	redisConn := os.Getenv("REDIS_URL")
	if redisConn == "" {
		// Redis connection strings for go-redis use specific format, "redis://<user>:<pass>@localhost:6379/<db>"
		redisConn = "redis://redis:6379"
	}

	pgDB, err := database.ConnectPostgres(pgConn)
	if err != nil {
		log.Fatalf("Postgres connection failed: %v", err)
	}
	defer pgDB.Close()

	redisDB, err := database.ConnectRedis(redisConn)
	if err != nil {
		log.Fatalf("Redis connection failed: %v", err)
	}
	defer redisDB.Close()

	ctx := context.Background()

	if err := pgDB.InitSchema(ctx); err != nil {
		log.Fatalf("Failed to initialize Postgres schema: %v", err)
	}

	if err := redisDB.InitSchema(ctx); err != nil {
		log.Fatalf("Failed to initialize Redis schema: %v", err)
	}

	url := "https://kaikki.org/dictionary/raw-wiktextract-data.jsonl.gz"
	batchSize := 2000 // using a slightly smaller batch to be safe on memory

	err = parser.StreamAndProcess(url, batchSize, func(batch []database.WordEntry) error {
		if err := pgDB.BatchInsertWords(ctx, batch); err != nil {
			log.Printf("Postgres insert error: %v", err)
			return err
		}
		if err := redisDB.BatchInsertAutocomplete(ctx, batch); err != nil {
			log.Printf("Redis insert error: %v", err)
			return err
		}
		return nil
	})

	if err != nil {
		log.Fatalf("ETL Pipeline failed: %v", err)
	}

	log.Println("ETL Pipeline completed successfully!")
}

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"

	"meaningful-backend/internal/database"
	"meaningful-backend/internal/enrich"
)

func main() {
	reset := flag.Bool("reset", false, "clear all prior enrichment output before running")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	for _, p := range []string{".env", "backend/.env", "../.env"} {
		if err := godotenv.Load(p); err == nil {
			log.Printf("loaded env from %s", p)
			break
		}
	}

	host := envOr("POSTGRES_HOST", "localhost")
	user := envOr("POSTGRES_USER", "postgres")
	pass := envOr("POSTGRES_PASSWORD", "postgres")
	name := envOr("POSTGRES_DB", "meaningful")
	connStr := fmt.Sprintf("postgres://%s:%s@%s:5432/%s", user, pass, host, name)

	log.Printf("Connecting to Postgres at %s...", host)
	db, err := database.ConnectPostgres(connStr)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer db.Close()

	// Apply schema migrations (idempotent — adds origin_story, enriched_senses, data_enriched if missing).
	if err := db.InitSchema(ctx); err != nil {
		log.Fatalf("init schema: %v", err)
	}

	if *reset {
		n, err := db.ResetEnrichment(ctx)
		if err != nil {
			log.Fatalf("reset enrichment: %v", err)
		}
		log.Printf("reset enrichment on %d rows", n)
	}

	apiKey := os.Getenv("NVIDIA_API_KEY")
	svc, err := enrich.NewService(db, apiKey)
	if err != nil {
		log.Fatalf("enrich service: %v", err)
	}

	cfg := enrich.DefaultConfig()
	log.Printf("starting enrichment pipeline (workers=%d, rpm=%d, daily_max=%d)",
		cfg.Workers, cfg.RPM, cfg.DailyMax)

	if err := svc.RunPipeline(ctx, cfg); err != nil {
		log.Fatalf("pipeline: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

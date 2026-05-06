package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"meaningful-backend/internal/api"
	"meaningful-backend/internal/database"
)

func main() {
	// 1. Setup Database Connections
	dbHost := os.Getenv("POSTGRES_HOST")
	if dbHost == "" {
		dbHost = "localhost"
	}
	dbUser := os.Getenv("POSTGRES_USER")
	if dbUser == "" {
		dbUser = "postgres"
	}
	dbPassword := os.Getenv("POSTGRES_PASSWORD")
	if dbPassword == "" {
		dbPassword = "postgres"
	}
	dbName := os.Getenv("POSTGRES_DB")
	if dbName == "" {
		dbName = "meaningful"
	}

	pgConnString := fmt.Sprintf("postgres://%s:%s@%s:5432/%s", dbUser, dbPassword, dbHost, dbName)
	log.Printf("Connecting to Postgres at %s", dbHost)
	pgDB, err := database.ConnectPostgres(pgConnString)
	if err != nil {
		log.Fatalf("Failed to connect to Postgres: %v", err)
	}
	defer pgDB.Close()

	redisHost := os.Getenv("REDIS_HOST")
	if redisHost == "" {
		redisHost = "localhost"
	}
	redisConnString := fmt.Sprintf("redis://%s:6379", redisHost)
	log.Printf("Connecting to Redis at %s", redisHost)
	redisDB, err := database.ConnectRedis(redisConnString)
	if err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	defer redisDB.Close()

	// 2. Setup API Handlers
	server := &api.Server{
		PG:    pgDB,
		Redis: redisDB,
	}

	mux := http.NewServeMux()

	// Setup Routes using Go 1.22+ routing enhancements
	mux.HandleFunc("GET /api/word/{word}", corsMiddleware(server.HandleGetWord))
	mux.HandleFunc("GET /api/words", corsMiddleware(server.HandleGetMultipleWords))
	mux.HandleFunc("GET /api/autocomplete", corsMiddleware(server.HandleAutocomplete))

	// Health check
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// 3. Start HTTP Server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("API Server listening on port %s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Listen error: %s\n", err)
		}
	}()

	// 4. Graceful Shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exiting")
}

// corsMiddleware allows cross-origin requests from the SolidStart frontend
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	}
}

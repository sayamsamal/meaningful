package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"meaningful-backend/internal/database"
)

type Server struct {
	PG    *database.PostgresDB
	Redis *database.RedisDB
}

// sendJSON is a helper to write JSON responses
func sendJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		fmt.Printf("Error encoding response: %v\n", err)
	}
}

// sendError is a helper to write error responses
func sendError(w http.ResponseWriter, status int, message string) {
	sendJSON(w, status, map[string]string{"error": message})
}

func (s *Server) HandleGetWord(w http.ResponseWriter, r *http.Request) {
	word := r.PathValue("word")
	if word == "" {
		sendError(w, http.StatusBadRequest, "word is required")
		return
	}

	// Safe character replacement instead of unescaping.
	// This maps URL underscores back to database spaces.
	// Casing is preserved so the lookup can distinguish case-collided
	// entries (e.g. "of" the preposition vs "OF" the initialism).
	word = strings.ReplaceAll(word, "_", " ")

	ctx := r.Context()

	// 1. Try Cache
	cached, err := s.Redis.GetCachedWord(ctx, word)
	if err == nil && cached != nil {
		w.Header().Set("X-Cache", "HIT")
		// Cache-Control for edge CDNs
		w.Header().Set("Cache-Control", "public, max-age=86400")
		sendJSON(w, http.StatusOK, cached)
		return
	}

	// 2. Fallback to Postgres
	entry, err := s.PG.GetWord(ctx, word)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "failed to fetch word")
		return
	}
	if entry == nil {
		sendError(w, http.StatusNotFound, "word not found")
		return
	}

	// 3. Populate Cache asynchronously or synchronously
	// For simplicity, we do it synchronously here with a 24-hour TTL
	_ = s.Redis.SetCachedWord(ctx, word, entry, 24*time.Hour)

	w.Header().Set("X-Cache", "MISS")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	sendJSON(w, http.StatusOK, entry)
}

func (s *Server) HandleGetMultipleWords(w http.ResponseWriter, r *http.Request) {
	wordsParam := r.URL.Query().Get("w")
	if wordsParam == "" {
		sendJSON(w, http.StatusOK, []database.WordEntry{})
		return
	}

	rawWords := strings.Split(wordsParam, ",")
	var words []string
	for _, rw := range rawWords {
		trimmed := strings.ToLower(strings.TrimSpace(rw))
		if trimmed != "" {
			words = append(words, trimmed)
		}
	}

	ctx := r.Context()
	
	// For multiple words, we bypass Redis caching logic for simplicity.
	// You could implement an MGET or pipeline here later.
	entries, err := s.PG.GetMultipleWords(ctx, words)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "failed to fetch words")
		return
	}

	// Make sure we always return an empty array, not null
	if entries == nil {
		entries = []database.WordEntry{}
	}

	w.Header().Set("Cache-Control", "public, max-age=86400")
	sendJSON(w, http.StatusOK, entries)
}

func (s *Server) HandleAutocomplete(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("query")
	if query == "" {
		sendJSON(w, http.StatusOK, []string{})
		return
	}

	raw := strings.TrimSpace(query) // preserve typed casing for the exact-case boost
	prefix := strings.ToLower(raw)  // FT.SUGGET is case-insensitive anyway
	ctx := r.Context()

	// 10 results is usually enough for autocomplete
	suggestions, err := s.Redis.GetAutocomplete(ctx, prefix, 10)
	if err != nil {
		fmt.Printf("Autocomplete error: %v\n", err)
		sendError(w, http.StatusInternalServerError, "failed to fetch suggestions")
		return
	}

	// Ensure empty array instead of null
	if suggestions == nil {
		suggestions = []string{}
	}

	// Case-sensitive boost: if the typed text is itself a stored word with this
	// exact casing (e.g. "OF"), make it the first result. FT.SUGGET ranks purely
	// by frequency, so a low-frequency exact-case variant is otherwise buried far
	// past the top 10. Skip the Postgres hit on the hot path where the top
	// frequency-ranked result already matches the typed text (e.g. "of").
	if len(suggestions) == 0 || suggestions[0] != raw {
		if exists, err := s.PG.ExactWordExists(ctx, raw); err == nil && exists {
			out := make([]string, 0, len(suggestions)+1)
			out = append(out, raw)
			for _, sug := range suggestions {
				if sug != raw {
					out = append(out, sug)
				}
			}
			if len(out) > 10 {
				out = out[:10]
			}
			suggestions = out
		}
	}

	// We shouldn't heavily cache autocomplete at the edge to avoid bursting, 
	// or we can cache it for a short time. Let's use 60 seconds.
	w.Header().Set("Cache-Control", "public, max-age=60")
	sendJSON(w, http.StatusOK, suggestions)
}

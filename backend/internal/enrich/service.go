package enrich

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/genai"

	"meaningful-backend/internal/database"
)

const (
	modelName     = "gemma-4-31b-it"
	maxOutputToks = 32_000
)

type Service struct {
	Client *genai.Client
	DB     *database.PostgresDB
}

func NewService(ctx context.Context, db *database.PostgresDB, apiKey string) (*Service, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY is empty")
	}
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("genai client: %w", err)
	}
	return &Service{Client: client, DB: db}, nil
}

// EnrichBatch sends a slice of raw WordEntry rows to Gemma 4 31B and returns
// the enriched results. Caller is responsible for rate limiting and persistence.
func (s *Service) EnrichBatch(ctx context.Context, words []database.WordEntry) ([]EnrichedWordEntry, error) {
	if len(words) == 0 {
		return nil, nil
	}

	inputs := make([]enrichInput, len(words))
	for i, w := range words {
		inputs[i] = enrichInput{
			Word:        w.Word,
			Etymologies: w.Etymologies,
			Senses:      w.Senses,
		}
	}

	inputJSON, err := json.Marshal(inputs)
	if err != nil {
		return nil, fmt.Errorf("marshal input: %w", err)
	}

	prompt := fmt.Sprintf("%s\n\nHere is the input JSON array:\n%s", systemPrompt, inputJSON)

	temp := float32(1.0)
	topP := float32(0.95)
	topK := float32(64)

	resp, err := s.Client.Models.GenerateContent(
		ctx,
		modelName,
		genai.Text(prompt),
		&genai.GenerateContentConfig{
			Temperature:      &temp,
			TopP:             &topP,
			TopK:             &topK,
			MaxOutputTokens:  maxOutputToks,
			ResponseMIMEType: "application/json",
			ResponseSchema:   responseSchema(),
			// Disable reasoning mode — <|think|> tokens would prepend the JSON
			// and break json.Unmarshal. See knowledge.claude.md.
			ThinkingConfig: &genai.ThinkingConfig{ThinkingBudget: genai.Ptr[int32](0)},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("generate: %w", err)
	}

	var enriched []EnrichedWordEntry
	if err := json.Unmarshal([]byte(resp.Text()), &enriched); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return enriched, nil
}

// EnrichOne is the on-demand single-word path: load the row by name, enrich it,
// persist the result, and return it. Skips the rate limiter — callers (e.g. an
// HTTP handler) are expected to gate this themselves.
func (s *Service) EnrichOne(ctx context.Context, word string) (*EnrichedWordEntry, error) {
	entry, err := s.DB.GetWord(ctx, word)
	if err != nil {
		return nil, fmt.Errorf("load word: %w", err)
	}
	if entry == nil {
		return nil, fmt.Errorf("word %q not found", word)
	}

	enriched, err := s.EnrichBatch(ctx, []database.WordEntry{*entry})
	if err != nil {
		return nil, err
	}
	if len(enriched) == 0 {
		return nil, fmt.Errorf("model returned no entries")
	}

	if err := s.Persist(ctx, enriched); err != nil {
		return nil, fmt.Errorf("persist: %w", err)
	}
	return &enriched[0], nil
}


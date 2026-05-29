package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	jsonrepair "github.com/RealAlexandreAI/json-repair"
	openai "github.com/sashabaranov/go-openai"

	"meaningful-backend/internal/database"
)

const (
	modelName     = "meta/llama-3.3-70b-instruct"
	nvidiaBaseURL = "https://integrate.api.nvidia.com/v1"
	maxOutputTok  = 32768
	// streamIdleTimeout is the max gap with zero tokens before the stream is
	// treated as stalled. Covers time-to-first-token for a large prompt on a
	// loaded free tier plus any inter-token gap; a 2-min silence is a real stall.
	streamIdleTimeout = 120 * time.Second
)

var errStreamIdle = errors.New("stream stalled: no tokens received within idle window")

type Service struct {
	Client *openai.Client
	DB     *database.PostgresDB
}

func NewService(db *database.PostgresDB, apiKey string) (*Service, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("NVIDIA_API_KEY is empty")
	}
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = nvidiaBaseURL
	return &Service{Client: openai.NewClientWithConfig(cfg), DB: db}, nil
}

// enrichResponse matches the object-rooted json_schema: { "entries": [...] }.
type enrichResponse struct {
	Entries []EnrichedWordEntry `json:"entries"`
}

// EnrichBatch sends a slice of raw WordEntry rows to Llama 3.3 70B (NVIDIA NIM)
// and returns the enriched results. Caller handles rate limiting and persistence.
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

	// Stream the response with a per-chunk idle timeout. The hosted NIM gateway
	// 504s if a long generation exceeds its response window; streaming keeps the
	// connection alive token-by-token. A fixed total deadline would kill healthy
	// but slow long generations, so instead a watchdog cancels only when the
	// stream produces nothing for streamIdleTimeout — a real stall.
	streamCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	// The watchdog owns the timer entirely (only it calls Stop/Reset/reads t.C),
	// avoiding the classic time.Reset race. Started before the stream opens so
	// connection setup and time-to-first-token are covered too.
	progress := make(chan struct{}, 1)
	go func() {
		t := time.NewTimer(streamIdleTimeout)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				cancel(errStreamIdle)
				return
			case <-progress:
				if !t.Stop() {
					<-t.C
				}
				t.Reset(streamIdleTimeout)
			case <-streamCtx.Done():
				return
			}
		}
	}()

	stream, err := s.Client.CreateChatCompletionStream(streamCtx, openai.ChatCompletionRequest{
		Model: modelName,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: fmt.Sprintf("Here is the input JSON array:\n%s", inputJSON)},
		},
		Temperature: 0.5,
		TopP:        0.9,
		MaxTokens:   maxOutputTok,
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
			JSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
				Name:   "enriched_words",
				Schema: enrichedSchema(),
				Strict: true,
			},
		},
	})
	if err != nil {
		if cause := context.Cause(streamCtx); errors.Is(cause, errStreamIdle) {
			return nil, cause
		}
		return nil, fmt.Errorf("create chat completion stream: %w", err)
	}
	defer stream.Close()

	var sb strings.Builder
	var finishReason openai.FinishReason
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if cause := context.Cause(streamCtx); errors.Is(cause, errStreamIdle) {
				return nil, cause
			}
			return nil, fmt.Errorf("stream recv: %w", err)
		}
		// Non-blocking progress signal; coalesced via the cap-1 buffer.
		select {
		case progress <- struct{}{}:
		default:
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		sb.WriteString(chunk.Choices[0].Delta.Content)
		if fr := chunk.Choices[0].FinishReason; fr != "" {
			finishReason = fr
		}
	}

	content := sb.String()
	if content == "" {
		return nil, fmt.Errorf("empty streamed response")
	}
	if finishReason == openai.FinishReasonLength {
		log.Printf("warning: response truncated (finish_reason=length) for %d-word batch — consider a smaller batch or higher max_tokens", len(words))
	}

	// json-repair patches any minor malformations before strict unmarshalling.
	repaired, err := jsonrepair.RepairJSON(content)
	if err != nil {
		return nil, fmt.Errorf("repair response: %w (first 300 chars: %q)", err, headSnippet(content))
	}

	var parsed enrichResponse
	if err := json.Unmarshal([]byte(repaired), &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w (first 300 chars: %q)", err, headSnippet(repaired))
	}
	return parsed.Entries, nil
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

	src := []database.WordEntry{*entry}
	enriched, err := s.EnrichBatch(ctx, src)
	if err != nil {
		return nil, err
	}
	if len(enriched) == 0 {
		return nil, fmt.Errorf("model returned no entries")
	}

	if err := s.Persist(ctx, src, enriched); err != nil {
		return nil, fmt.Errorf("persist: %w", err)
	}
	return &enriched[0], nil
}

func headSnippet(s string) string {
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

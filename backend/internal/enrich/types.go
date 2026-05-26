package enrich

import "meaningful-backend/internal/database"

type EnrichedDefinition struct {
	Definition string   `json:"definition"`
	Examples   []string `json:"examples"`
}

type SenseGroup struct {
	POS         string               `json:"pos"`
	Definitions []EnrichedDefinition `json:"definitions"`
}

// enrichInput is the trimmed payload sent to the model. Synonyms, antonyms, and
// frequency are intentionally omitted — they pass through unchanged in the DB
// and don't influence origin_story or sense consolidation.
type enrichInput struct {
	Word        string          `json:"word"`
	Etymologies []string        `json:"etymologies"`
	Senses      database.Senses `json:"senses"`
}

type EnrichedWordEntry struct {
	Word        string       `json:"word"`
	OriginStory string       `json:"origin_story"`
	Senses      []SenseGroup `json:"senses"`
}

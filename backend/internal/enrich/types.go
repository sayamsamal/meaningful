package enrich

import (
	"bytes"
	"encoding/json"

	"meaningful-backend/internal/database"
)

type SubDefinition struct {
	Sense   string `json:"sense"`
	Example string `json:"example"` // exactly one
}

type EnrichedDefinition struct {
	Sense     string          `json:"sense"`     // the meaning text
	Examples  []string        `json:"examples"`  // 1–3 for a leaf; empty when subsenses present
	Subsenses []SubDefinition `json:"subsenses"` // empty for a leaf
}

// UnmarshalJSON tolerates the model occasionally emitting a bare string in place
// of a meaning object — treating it as the sense text with no examples or
// subsenses — so a single off-schema element doesn't fail the whole batch.
func (d *EnrichedDefinition) UnmarshalJSON(data []byte) error {
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		d.Sense, d.Examples, d.Subsenses = s, nil, nil
		return nil
	}
	type alias EnrichedDefinition // avoid recursion
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*d = EnrichedDefinition(a)
	return nil
}

type SenseGroup struct {
	POS    string               `json:"pos"`
	Senses []EnrichedDefinition `json:"senses"`
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

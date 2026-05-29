package enrich

import "github.com/sashabaranov/go-openai/jsonschema"

// enrichedSchema builds the json_schema passed to response_format. vLLM/NIM
// guided decoding requires an object root, so results are wrapped under
// "entries". Every object marks all properties required with
// additionalProperties:false for strict adherence.
//
// Shape: entries[] → { word, origin_story, senses[] }
//   senses[]  (pos-groups) → { pos, senses[] }
//     senses[] (meanings)  → { sense, examples[], subsenses[] }
//       subsenses[]        → { sense, examples[] }
func enrichedSchema() *jsonschema.Definition {
	str := jsonschema.Definition{Type: jsonschema.String}
	strArray := jsonschema.Definition{Type: jsonschema.Array, Items: &jsonschema.Definition{Type: jsonschema.String}}

	subsense := jsonschema.Definition{
		Type: jsonschema.Object,
		Properties: map[string]jsonschema.Definition{
			"sense":   str,
			"example": str,
		},
		Required:             []string{"sense", "example"},
		AdditionalProperties: false,
	}

	meaning := jsonschema.Definition{
		Type: jsonschema.Object,
		Properties: map[string]jsonschema.Definition{
			"sense":     str,
			"examples":  strArray,
			"subsenses": {Type: jsonschema.Array, Items: &subsense},
		},
		Required:             []string{"sense", "examples", "subsenses"},
		AdditionalProperties: false,
	}

	posGroup := jsonschema.Definition{
		Type: jsonschema.Object,
		Properties: map[string]jsonschema.Definition{
			"pos":    str,
			"senses": {Type: jsonschema.Array, Items: &meaning},
		},
		Required:             []string{"pos", "senses"},
		AdditionalProperties: false,
	}

	entry := jsonschema.Definition{
		Type: jsonschema.Object,
		Properties: map[string]jsonschema.Definition{
			"word":         str,
			"origin_story": str,
			"senses":       {Type: jsonschema.Array, Items: &posGroup},
		},
		Required:             []string{"word", "origin_story", "senses"},
		AdditionalProperties: false,
	}

	return &jsonschema.Definition{
		Type: jsonschema.Object,
		Properties: map[string]jsonschema.Definition{
			"entries": {Type: jsonschema.Array, Items: &entry},
		},
		Required:             []string{"entries"},
		AdditionalProperties: false,
	}
}

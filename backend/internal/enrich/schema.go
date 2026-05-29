package enrich

import "github.com/sashabaranov/go-openai/jsonschema"

// enrichedSchema builds the json_schema passed to response_format. vLLM/NIM
// guided decoding requires an object root, so results are wrapped under
// "entries". Every object marks all properties required with
// additionalProperties:false for strict adherence.
func enrichedSchema() *jsonschema.Definition {
	str := jsonschema.Definition{Type: jsonschema.String}
	strArray := jsonschema.Definition{Type: jsonschema.Array, Items: &jsonschema.Definition{Type: jsonschema.String}}

	definition := jsonschema.Definition{
		Type: jsonschema.Object,
		Properties: map[string]jsonschema.Definition{
			"definition": str,
			"examples":   strArray,
		},
		Required:             []string{"definition", "examples"},
		AdditionalProperties: false,
	}

	senseGroup := jsonschema.Definition{
		Type: jsonschema.Object,
		Properties: map[string]jsonschema.Definition{
			"pos":         str,
			"definitions": {Type: jsonschema.Array, Items: &definition},
		},
		Required:             []string{"pos", "definitions"},
		AdditionalProperties: false,
	}

	entry := jsonschema.Definition{
		Type: jsonschema.Object,
		Properties: map[string]jsonschema.Definition{
			"word":         str,
			"origin_story": str,
			"senses":       {Type: jsonschema.Array, Items: &senseGroup},
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

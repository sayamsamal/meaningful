package enrich

import "google.golang.org/genai"

func responseSchema() *genai.Schema {
	entry := &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"word":         {Type: genai.TypeString},
			"origin_story": {Type: genai.TypeString},
			"senses": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"pos": {Type: genai.TypeString},
						"definitions": {
							Type: genai.TypeArray,
							Items: &genai.Schema{
								Type: genai.TypeObject,
								Properties: map[string]*genai.Schema{
									"definition": {Type: genai.TypeString},
									"examples":   {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
								},
								Required:         []string{"definition", "examples"},
								PropertyOrdering: []string{"definition", "examples"},
							},
						},
					},
					Required:         []string{"pos", "definitions"},
					PropertyOrdering: []string{"pos", "definitions"},
				},
			},
		},
		Required:         []string{"word", "origin_story", "senses"},
		PropertyOrdering: []string{"word", "origin_story", "senses"},
	}
	return &genai.Schema{Type: genai.TypeArray, Items: entry}
}

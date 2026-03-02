package openai

import (
	"encoding/json"
	"strings"
)

func NormalizeModel(model string, modelMap map[string]string) string {
	if model == "" {
		return "gpt-5.2"
	}
	modelID := model
	if idx := strings.LastIndex(modelID, "/"); idx >= 0 {
		modelID = modelID[idx+1:]
	}
	if mapped, ok := modelMap[modelID]; ok {
		return mapped
	}
	lower := strings.ToLower(modelID)
	for key, value := range modelMap {
		if strings.ToLower(key) == lower {
			return value
		}
	}
	return model
}

type PrepareOptions struct {
	FallbackModel      string
	OAuth              bool
	ModelMap           map[string]string
	ApplyExtraParams   func(parsed map[string]any, model string) bool
	DefaultInstruction func(model string) (string, bool)
}

func PrepareBody(body []byte, opts PrepareOptions) []byte {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return body
	}

	modified := false

	if currentModel, _ := parsed["model"].(string); currentModel != "" {
		if normalized := NormalizeModel(currentModel, opts.ModelMap); normalized != "" && normalized != currentModel {
			parsed["model"] = normalized
			modified = true
		}
	} else {
		fallback := opts.FallbackModel
		if fallback == "" {
			fallback = "gpt-5.2"
		}
		parsed["model"] = fallback
		modified = true
	}

	effectiveModel, _ := parsed["model"].(string)
	if effectiveModel == "" {
		effectiveModel = opts.FallbackModel
	}
	if opts.ApplyExtraParams != nil && opts.ApplyExtraParams(parsed, effectiveModel) {
		modified = true
	}

	if opts.OAuth {
		if v, ok := parsed["store"].(bool); !ok || v {
			parsed["store"] = false
			modified = true
		}
		if v, ok := parsed["stream"].(bool); !ok || !v {
			parsed["stream"] = true
			modified = true
		}

		for _, key := range []string{
			"max_output_tokens", "max_completion_tokens",
			"temperature", "top_p",
			"frequency_penalty", "presence_penalty",
			"metadata",
			"prompt_cache_retention",
			"stream_options",
			"user",
			"reasoning_effort",
			"response_format",
		} {
			if _, ok := parsed[key]; ok {
				delete(parsed, key)
				modified = true
			}
		}
	}

	if reasoning, ok := parsed["reasoning"].(map[string]any); ok {
		if effort, _ := reasoning["effort"].(string); effort == "minimal" {
			reasoning["effort"] = "none"
			modified = true
		}
	}

	if opts.DefaultInstruction != nil {
		if defaultInstruction, ok := opts.DefaultInstruction(effectiveModel); ok {
			curInstruction, hasInstruction := parsed["instructions"]
			shouldSetDefault := !hasInstruction
			if !shouldSetDefault {
				if s, ok := curInstruction.(string); ok {
					shouldSetDefault = strings.TrimSpace(s) == ""
				}
			}
			if shouldSetDefault {
				parsed["instructions"] = defaultInstruction
				modified = true
			}
		}
	}

	if !modified {
		return body
	}
	newBody, err := json.Marshal(parsed)
	if err != nil {
		return body
	}
	return newBody
}

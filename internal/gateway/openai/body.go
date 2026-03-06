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
		if applyOAuthCompatAliases(parsed) {
			modified = true
		}

		for _, key := range []string{
			"temperature", "top_p",
			"frequency_penalty", "presence_penalty",
			"max_completion_tokens", "user",
			"prompt_cache_retention",
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

func applyOAuthCompatAliases(parsed map[string]any) bool {
	modified := false

	if v, ok := parsed["max_completion_tokens"]; ok {
		if _, exists := parsed["max_output_tokens"]; !exists {
			parsed["max_output_tokens"] = v
			modified = true
		}
		delete(parsed, "max_completion_tokens")
		modified = true
	}

	if v, ok := parsed["reasoning_effort"]; ok {
		if effort, ok := v.(string); ok && strings.TrimSpace(effort) != "" {
			reasoning, _ := parsed["reasoning"].(map[string]any)
			if reasoning == nil {
				reasoning = map[string]any{}
				parsed["reasoning"] = reasoning
				modified = true
			}
			if current, ok := reasoning["effort"].(string); !ok || strings.TrimSpace(current) == "" {
				reasoning["effort"] = effort
				modified = true
			}
		}
		delete(parsed, "reasoning_effort")
		modified = true
	}

	if v, ok := parsed["response_format"]; ok {
		textCfg, _ := parsed["text"].(map[string]any)
		if textCfg == nil {
			textCfg = map[string]any{}
			parsed["text"] = textCfg
			modified = true
		}
		if _, exists := textCfg["format"]; !exists {
			textCfg["format"] = normalizeResponseFormatForResponses(v)
			modified = true
		}
		delete(parsed, "response_format")
		modified = true
	}

	if v, ok := parsed["verbosity"]; ok {
		textCfg, _ := parsed["text"].(map[string]any)
		if textCfg == nil {
			textCfg = map[string]any{}
			parsed["text"] = textCfg
			modified = true
		}
		if _, exists := textCfg["verbosity"]; !exists {
			textCfg["verbosity"] = v
			modified = true
		}
		delete(parsed, "verbosity")
		modified = true
	}

	if v, ok := parsed["user"]; ok {
		if _, exists := parsed["safety_identifier"]; !exists {
			if userID, ok := v.(string); ok && strings.TrimSpace(userID) != "" {
				parsed["safety_identifier"] = userID
				modified = true
			}
		}
		delete(parsed, "user")
		modified = true
	}

	return modified
}

func normalizeResponseFormatForResponses(raw any) any {
	switch v := raw.(type) {
	case string:
		formatType := strings.TrimSpace(v)
		if formatType == "" {
			return raw
		}
		return map[string]any{"type": formatType}
	case map[string]any:
		formatType, _ := v["type"].(string)
		formatType = strings.TrimSpace(formatType)
		if formatType != "json_schema" {
			return raw
		}

		normalized := map[string]any{
			"type": "json_schema",
		}

		// OAuth experimental Responses expects flattened json_schema fields
		// under text.format (name/schema/strict), while Chat Completions
		// commonly nests them under response_format.json_schema.
		if nested, _ := v["json_schema"].(map[string]any); nested != nil {
			if name, _ := nested["name"].(string); strings.TrimSpace(name) != "" {
				normalized["name"] = name
			}
			if schema, ok := nested["schema"]; ok {
				normalized["schema"] = schema
			}
			if strict, ok := nested["strict"]; ok {
				normalized["strict"] = strict
			}
			if description, _ := nested["description"].(string); strings.TrimSpace(description) != "" {
				normalized["description"] = description
			}
		}

		if name, _ := v["name"].(string); strings.TrimSpace(name) != "" {
			normalized["name"] = name
		}
		if schema, ok := v["schema"]; ok {
			normalized["schema"] = schema
		}
		if strict, ok := v["strict"]; ok {
			normalized["strict"] = strict
		}
		if description, _ := v["description"].(string); strings.TrimSpace(description) != "" {
			normalized["description"] = description
		}

		if name, _ := normalized["name"].(string); strings.TrimSpace(name) == "" {
			normalized["name"] = "output_schema"
		}

		return normalized
	default:
		return raw
	}
}

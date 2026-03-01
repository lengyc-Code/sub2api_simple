package openai

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ConvertChatCompletionsRequest maps OpenAI Chat Completions payload to Responses API payload.
func ConvertChatCompletionsRequest(body []byte, modelMap map[string]string) ([]byte, string, bool, error) {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, "", false, fmt.Errorf("invalid JSON: %w", err)
	}

	model, _ := parsed["model"].(string)
	model = NormalizeModel(model, modelMap)
	if strings.TrimSpace(model) == "" {
		model = "gpt-5.2"
	}
	parsed["model"] = model

	stream, _ := parsed["stream"].(bool)

	// Chat Completions uses "messages"; Responses uses "input".
	if _, ok := parsed["input"]; !ok {
		if messages, ok := parsed["messages"]; ok {
			parsed["input"] = messages
		}
	}
	delete(parsed, "messages")

	// Common compatibility mapping.
	if maxTokens, ok := parsed["max_tokens"]; ok {
		if _, exists := parsed["max_output_tokens"]; !exists {
			parsed["max_output_tokens"] = maxTokens
		}
		delete(parsed, "max_tokens")
	}

	// Responses API currently returns a single output; drop unsupported multi-choice hint.
	delete(parsed, "n")

	converted, err := json.Marshal(parsed)
	if err != nil {
		return nil, "", false, err
	}
	return converted, model, stream, nil
}

func ChatCompletionFromResponses(resp map[string]any, fallbackModel string) map[string]any {
	model := fallbackModel
	if v, ok := resp["model"].(string); ok && strings.TrimSpace(v) != "" {
		model = v
	}
	if model == "" {
		model = "gpt-5.2"
	}

	id, _ := resp["id"].(string)
	if id == "" {
		id = fmt.Sprintf("chatcmpl_%d", time.Now().UnixNano())
	}

	created := time.Now().Unix()
	if v, ok := toInt64(resp["created"]); ok {
		created = v
	} else if v, ok := toInt64(resp["created_at"]); ok {
		created = v
	}

	content := ExtractResponseText(resp)

	out := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": created,
		"model":   model,
		"choices": []any{
			map[string]any{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": content,
				},
				"finish_reason": "stop",
			},
		},
	}

	if usage := mapUsage(resp["usage"]); len(usage) > 0 {
		out["usage"] = usage
	}

	return out
}

func ChatCompletionChunk(id, model string, created int64, delta map[string]any, finishReason any) map[string]any {
	if id == "" {
		id = fmt.Sprintf("chatcmpl_%d", time.Now().UnixNano())
	}
	if created <= 0 {
		created = time.Now().Unix()
	}
	if model == "" {
		model = "gpt-5.2"
	}
	return map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []any{
			map[string]any{
				"index":         0,
				"delta":         delta,
				"finish_reason": finishReason,
			},
		},
	}
}

func ExtractResponseText(resp map[string]any) string {
	if outputText, _ := resp["output_text"].(string); strings.TrimSpace(outputText) != "" {
		return outputText
	}

	output, _ := resp["output"].([]any)
	if len(output) == 0 {
		return ""
	}

	var b strings.Builder
	for _, item := range output {
		itemMap, _ := item.(map[string]any)
		if itemMap == nil {
			continue
		}
		content, _ := itemMap["content"].([]any)
		for _, part := range content {
			partMap, _ := part.(map[string]any)
			if partMap == nil {
				continue
			}
			if partType, _ := partMap["type"].(string); partType != "output_text" {
				continue
			}
			text, _ := partMap["text"].(string)
			if text != "" {
				b.WriteString(text)
			}
		}
	}
	return b.String()
}

func mapUsage(raw any) map[string]any {
	usageMap, _ := raw.(map[string]any)
	if usageMap == nil {
		return nil
	}

	out := map[string]any{}

	promptTokens, promptOK := toInt64(usageMap["prompt_tokens"])
	if !promptOK {
		promptTokens, promptOK = toInt64(usageMap["input_tokens"])
	}
	if promptOK {
		out["prompt_tokens"] = promptTokens
	}

	completionTokens, completionOK := toInt64(usageMap["completion_tokens"])
	if !completionOK {
		completionTokens, completionOK = toInt64(usageMap["output_tokens"])
	}
	if completionOK {
		out["completion_tokens"] = completionTokens
	}

	if total, ok := toInt64(usageMap["total_tokens"]); ok {
		out["total_tokens"] = total
	} else if promptOK || completionOK {
		out["total_tokens"] = promptTokens + completionTokens
	}

	return out
}

func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), true
	case json.Number:
		x, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return x, true
	default:
		return 0, false
	}
}

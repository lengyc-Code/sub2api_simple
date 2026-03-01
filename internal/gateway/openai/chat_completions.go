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
			parsed["input"] = convertChatMessagesToResponsesInput(messages)
		}
	}
	delete(parsed, "messages")

	// Chat Completions tools -> Responses tools.
	if tools, ok := parsed["tools"]; ok {
		parsed["tools"] = convertChatTools(tools)
	}
	if funcs, ok := parsed["functions"]; ok {
		if _, hasTools := parsed["tools"]; !hasTools {
			parsed["tools"] = convertLegacyFunctions(funcs)
		}
		delete(parsed, "functions")
	}

	// Chat Completions tool_choice/function_call -> Responses tool_choice.
	if toolChoice, ok := parsed["tool_choice"]; ok {
		parsed["tool_choice"] = convertChatToolChoice(toolChoice)
	}
	if functionCall, ok := parsed["function_call"]; ok {
		if _, hasToolChoice := parsed["tool_choice"]; !hasToolChoice {
			parsed["tool_choice"] = convertLegacyFunctionCall(functionCall)
		}
		delete(parsed, "function_call")
	}

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

func convertChatTools(rawTools any) []any {
	tools, _ := rawTools.([]any)
	if len(tools) == 0 {
		return nil
	}

	out := make([]any, 0, len(tools))
	for _, item := range tools {
		tool, _ := item.(map[string]any)
		if tool == nil {
			continue
		}

		toolType, _ := tool["type"].(string)
		if strings.TrimSpace(toolType) == "" {
			toolType = "function"
		}

		if toolType != "function" {
			out = append(out, tool)
			continue
		}

		fn, _ := tool["function"].(map[string]any)
		name := ""
		if fn != nil {
			name, _ = fn["name"].(string)
		}
		if strings.TrimSpace(name) == "" {
			name, _ = tool["name"].(string)
		}
		if strings.TrimSpace(name) == "" {
			// Skip invalid entries to avoid upstream 400 on missing tools[].name.
			continue
		}

		converted := map[string]any{
			"type": "function",
			"name": name,
		}

		if fn != nil {
			if desc, _ := fn["description"].(string); strings.TrimSpace(desc) != "" {
				converted["description"] = desc
			}
			if params, ok := fn["parameters"]; ok {
				converted["parameters"] = params
			}
			if strict, ok := fn["strict"].(bool); ok {
				converted["strict"] = strict
			}
		}

		if _, ok := converted["description"]; !ok {
			if desc, _ := tool["description"].(string); strings.TrimSpace(desc) != "" {
				converted["description"] = desc
			}
		}
		if _, ok := converted["parameters"]; !ok {
			if params, ok := tool["parameters"]; ok {
				converted["parameters"] = params
			}
		}
		if _, ok := converted["strict"]; !ok {
			if strict, ok := tool["strict"].(bool); ok {
				converted["strict"] = strict
			}
		}

		out = append(out, converted)
	}
	return out
}

func convertLegacyFunctions(rawFunctions any) []any {
	functions, _ := rawFunctions.([]any)
	if len(functions) == 0 {
		return nil
	}

	out := make([]any, 0, len(functions))
	for _, item := range functions {
		fn, _ := item.(map[string]any)
		if fn == nil {
			continue
		}
		name, _ := fn["name"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}

		tool := map[string]any{
			"type": "function",
			"name": name,
		}
		if desc, _ := fn["description"].(string); strings.TrimSpace(desc) != "" {
			tool["description"] = desc
		}
		if params, ok := fn["parameters"]; ok {
			tool["parameters"] = params
		}
		if strict, ok := fn["strict"].(bool); ok {
			tool["strict"] = strict
		}
		out = append(out, tool)
	}
	return out
}

func convertChatToolChoice(raw any) any {
	switch v := raw.(type) {
	case string:
		return v
	case map[string]any:
		toolType, _ := v["type"].(string)
		if strings.TrimSpace(toolType) == "" {
			toolType = "function"
		}
		if toolType != "function" {
			return v
		}

		name := ""
		if fn, _ := v["function"].(map[string]any); fn != nil {
			name, _ = fn["name"].(string)
		}
		if strings.TrimSpace(name) == "" {
			name, _ = v["name"].(string)
		}
		if strings.TrimSpace(name) == "" {
			return "auto"
		}
		return map[string]any{
			"type": "function",
			"name": name,
		}
	default:
		return raw
	}
}

func convertLegacyFunctionCall(raw any) any {
	switch v := raw.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "auto":
			return "auto"
		case "none":
			return "none"
		default:
			return "auto"
		}
	case map[string]any:
		name, _ := v["name"].(string)
		if strings.TrimSpace(name) == "" {
			return "auto"
		}
		return map[string]any{
			"type": "function",
			"name": name,
		}
	default:
		return "auto"
	}
}

func convertChatMessagesToResponsesInput(rawMessages any) []any {
	messages, _ := rawMessages.([]any)
	if len(messages) == 0 {
		return nil
	}

	result := make([]any, 0, len(messages))
	for _, item := range messages {
		msg, _ := item.(map[string]any)
		if msg == nil {
			continue
		}

		role, _ := msg["role"].(string)
		if strings.TrimSpace(role) == "" {
			role = "user"
		}

		out := map[string]any{
			"role": role,
		}
		if name, _ := msg["name"].(string); strings.TrimSpace(name) != "" {
			out["name"] = name
		}

		if content := convertMessageContent(role, msg["content"]); len(content) > 0 {
			out["content"] = content
		}

		result = append(result, out)
	}
	return result
}

func convertMessageContent(role string, raw any) []any {
	switch v := raw.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		return []any{
			map[string]any{
				"type": roleTextPartType(role),
				"text": v,
			},
		}
	case []any:
		out := make([]any, 0, len(v))
		for _, p := range v {
			part, _ := p.(map[string]any)
			if part == nil {
				continue
			}
			converted, ok := convertMessagePart(role, part)
			if !ok {
				continue
			}
			out = append(out, converted)
		}
		return out
	default:
		return nil
	}
}

func convertMessagePart(role string, part map[string]any) (map[string]any, bool) {
	partType, _ := part["type"].(string)
	partType = strings.TrimSpace(partType)

	switch partType {
	case "", "text":
		text, _ := part["text"].(string)
		if strings.TrimSpace(text) == "" {
			return nil, false
		}
		return map[string]any{
			"type": roleTextPartType(role),
			"text": text,
		}, true
	case "input_text", "output_text", "summary_text", "refusal":
		// Already in Responses-compatible shape.
		return part, true
	case "image_url":
		if imageURL, ok := extractImageURL(part["image_url"]); ok {
			return map[string]any{
				"type":      "input_image",
				"image_url": imageURL,
			}, true
		}
		return nil, false
	case "input_image", "input_file", "computer_screenshot":
		return part, true
	default:
		// Best effort fallback for unknown text-like part.
		if text, _ := part["text"].(string); strings.TrimSpace(text) != "" {
			return map[string]any{
				"type": roleTextPartType(role),
				"text": text,
			}, true
		}
		return nil, false
	}
}

func roleTextPartType(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "assistant":
		return "output_text"
	default:
		return "input_text"
	}
}

func extractImageURL(raw any) (string, bool) {
	switch v := raw.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return "", false
		}
		return v, true
	case map[string]any:
		url, _ := v["url"].(string)
		if strings.TrimSpace(url) == "" {
			return "", false
		}
		return url, true
	default:
		return "", false
	}
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

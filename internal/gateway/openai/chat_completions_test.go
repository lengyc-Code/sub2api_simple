package openai

import (
	"encoding/json"
	"testing"
)

func TestConvertChatCompletionsRequest_ToolCallRoundTripInputs(t *testing.T) {
	req := map[string]any{
		"model": "gpt-5.1-codex-mini",
		"tools": []any{
			map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        "get_weather",
					"description": "Get weather by city",
					"parameters": map[string]any{
						"type": "object",
					},
				},
			},
		},
		"messages": []any{
			map[string]any{
				"role":    "user",
				"content": "What's the weather in Shanghai?",
			},
			map[string]any{
				"role": "assistant",
				"tool_calls": []any{
					map[string]any{
						"id":   "call_1",
						"type": "function",
						"function": map[string]any{
							"name":      "get_weather",
							"arguments": "{\"city\":\"Shanghai\"}",
						},
					},
				},
			},
			map[string]any{
				"role":         "tool",
				"tool_call_id": "call_1",
				"content":      "{\"temp\":25}",
			},
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	convertedBody, _, _, err := ConvertChatCompletionsRequest(body, nil)
	if err != nil {
		t.Fatalf("convert request: %v", err)
	}

	var converted map[string]any
	if err := json.Unmarshal(convertedBody, &converted); err != nil {
		t.Fatalf("unmarshal converted: %v", err)
	}

	tools, _ := converted["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	tool0, _ := tools[0].(map[string]any)
	if got, _ := tool0["name"].(string); got != "get_weather" {
		t.Fatalf("expected tool name get_weather, got %q", got)
	}

	input, _ := converted["input"].([]any)
	if len(input) != 3 {
		t.Fatalf("expected 3 input items, got %d", len(input))
	}

	call, _ := input[1].(map[string]any)
	if got, _ := call["type"].(string); got != "function_call" {
		t.Fatalf("expected input[1].type=function_call, got %q", got)
	}
	if got, _ := call["call_id"].(string); got != "call_1" {
		t.Fatalf("expected input[1].call_id=call_1, got %q", got)
	}

	output, _ := input[2].(map[string]any)
	if got, _ := output["type"].(string); got != "function_call_output" {
		t.Fatalf("expected input[2].type=function_call_output, got %q", got)
	}
	if got, _ := output["call_id"].(string); got != "call_1" {
		t.Fatalf("expected input[2].call_id=call_1, got %q", got)
	}
}

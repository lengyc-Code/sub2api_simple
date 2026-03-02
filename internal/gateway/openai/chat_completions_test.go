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

func TestConvertChatCompletionsRequest_ResponseFormatJSONSchemaMapped(t *testing.T) {
	req := map[string]any{
		"model": "gpt-5.2",
		"messages": []any{
			map[string]any{
				"role":    "user",
				"content": "Return JSON only",
			},
		},
		"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{"type": "string"},
					},
					"required": []any{"action"},
				},
				"strict": true,
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

	if _, ok := converted["response_format"]; ok {
		t.Fatal("expected response_format removed")
	}

	textCfg, _ := converted["text"].(map[string]any)
	if textCfg == nil {
		t.Fatal("expected text config")
	}
	format, _ := textCfg["format"].(map[string]any)
	if format == nil {
		t.Fatal("expected text.format")
	}
	if got, _ := format["type"].(string); got != "json_schema" {
		t.Fatalf("expected format.type=json_schema, got %q", got)
	}
	if got, _ := format["name"].(string); got != "output_schema" {
		t.Fatalf("expected default format.name=output_schema, got %q", got)
	}
	if _, ok := format["schema"]; !ok {
		t.Fatal("expected format.schema")
	}
}

func TestConvertChatCompletionsRequest_ReasoningAndStreamOptionsCompat(t *testing.T) {
	req := map[string]any{
		"model":            "gpt-5.2",
		"reasoning_effort": "high",
		"stream_options": map[string]any{
			"include_usage": true,
		},
		"messages": []any{
			map[string]any{
				"role":    "user",
				"content": "hi",
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

	if _, ok := converted["reasoning_effort"]; ok {
		t.Fatal("expected reasoning_effort removed")
	}
	reasoning, _ := converted["reasoning"].(map[string]any)
	if reasoning == nil {
		t.Fatal("expected reasoning object")
	}
	if got, _ := reasoning["effort"].(string); got != "high" {
		t.Fatalf("expected reasoning.effort=high, got %q", got)
	}

	if _, ok := converted["stream_options"]; ok {
		t.Fatal("expected stream_options removed")
	}
}

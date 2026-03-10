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

	convertedBody, _, _, _, err := ConvertChatCompletionsRequest(body, nil)
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
	convertedBody, _, _, _, err := ConvertChatCompletionsRequest(body, nil)
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
	convertedBody, _, _, includeUsage, err := ConvertChatCompletionsRequest(body, nil)
	if err != nil {
		t.Fatalf("convert request: %v", err)
	}
	if !includeUsage {
		t.Fatal("expected include_usage=true to be detected")
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

	streamOptions, _ := converted["stream_options"].(map[string]any)
	if streamOptions == nil {
		t.Fatal("expected stream_options preserved")
	}
	if includeUsage, _ := streamOptions["include_usage"].(bool); !includeUsage {
		t.Fatalf("expected stream_options.include_usage=true, got %#v", streamOptions["include_usage"])
	}
}

func TestConvertChatCompletionsRequest_MaxCompletionTokensVerbosityAndWebSearchMapped(t *testing.T) {
	req := map[string]any{
		"model":                 "gpt-5.2",
		"max_completion_tokens": float64(321),
		"verbosity":             "low",
		"web_search_options": map[string]any{
			"user_location": map[string]any{
				"type":    "approximate",
				"country": "US",
			},
		},
		"messages": []any{
			map[string]any{
				"role":    "user",
				"content": "search this",
			},
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	convertedBody, _, _, _, err := ConvertChatCompletionsRequest(body, nil)
	if err != nil {
		t.Fatalf("convert request: %v", err)
	}

	var converted map[string]any
	if err := json.Unmarshal(convertedBody, &converted); err != nil {
		t.Fatalf("unmarshal converted: %v", err)
	}

	if _, ok := converted["max_completion_tokens"]; ok {
		t.Fatal("expected max_completion_tokens removed")
	}
	if got, _ := converted["max_output_tokens"].(float64); got != 321 {
		t.Fatalf("expected max_output_tokens=321, got %v", converted["max_output_tokens"])
	}
	if _, ok := converted["verbosity"]; ok {
		t.Fatal("expected top-level verbosity removed")
	}

	textCfg, _ := converted["text"].(map[string]any)
	if textCfg == nil {
		t.Fatal("expected text config")
	}
	if got, _ := textCfg["verbosity"].(string); got != "low" {
		t.Fatalf("expected text.verbosity=low, got %q", got)
	}

	tools, _ := converted["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	tool, _ := tools[0].(map[string]any)
	if got, _ := tool["type"].(string); got != "web_search" {
		t.Fatalf("expected web_search tool, got %q", got)
	}
	userLocation, _ := tool["user_location"].(map[string]any)
	if userLocation == nil {
		t.Fatal("expected web_search user_location")
	}
	if got, _ := userLocation["country"].(string); got != "US" {
		t.Fatalf("expected user_location.country=US, got %q", got)
	}
}

func TestConvertChatCompletionsRequest_FileAndImageContentMapped(t *testing.T) {
	req := map[string]any{
		"model": "gpt-5.2",
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{
						"type": "file",
						"file": map[string]any{
							"file_id":  "file_123",
							"filename": "notes.txt",
						},
					},
					map[string]any{
						"type": "image_url",
						"image_url": map[string]any{
							"url":    "https://example.com/image.png",
							"detail": "high",
						},
					},
				},
			},
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	convertedBody, _, _, _, err := ConvertChatCompletionsRequest(body, nil)
	if err != nil {
		t.Fatalf("convert request: %v", err)
	}

	var converted map[string]any
	if err := json.Unmarshal(convertedBody, &converted); err != nil {
		t.Fatalf("unmarshal converted: %v", err)
	}

	input, _ := converted["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("expected 1 input item, got %d", len(input))
	}
	msg, _ := input[0].(map[string]any)
	content, _ := msg["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("expected 2 content parts, got %d", len(content))
	}

	filePart, _ := content[0].(map[string]any)
	if got, _ := filePart["type"].(string); got != "input_file" {
		t.Fatalf("expected input_file part, got %q", got)
	}
	if got, _ := filePart["file_id"].(string); got != "file_123" {
		t.Fatalf("expected file_id=file_123, got %q", got)
	}

	imagePart, _ := content[1].(map[string]any)
	if got, _ := imagePart["type"].(string); got != "input_image" {
		t.Fatalf("expected input_image part, got %q", got)
	}
	if got, _ := imagePart["detail"].(string); got != "high" {
		t.Fatalf("expected image detail=high, got %q", got)
	}
}

func TestConvertChatCompletionsRequest_LogprobsMappedToInclude(t *testing.T) {
	req := map[string]any{
		"model":        "gpt-5.2",
		"logprobs":     true,
		"top_logprobs": float64(3),
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
	convertedBody, _, _, _, err := ConvertChatCompletionsRequest(body, nil)
	if err != nil {
		t.Fatalf("convert request: %v", err)
	}

	var converted map[string]any
	if err := json.Unmarshal(convertedBody, &converted); err != nil {
		t.Fatalf("unmarshal converted: %v", err)
	}

	if _, ok := converted["logprobs"]; ok {
		t.Fatal("expected logprobs removed")
	}
	if got, _ := converted["top_logprobs"].(float64); got != 3 {
		t.Fatalf("expected top_logprobs=3, got %v", converted["top_logprobs"])
	}
	include, _ := converted["include"].([]any)
	if len(include) != 1 {
		t.Fatalf("expected 1 include entry, got %d", len(include))
	}
	if got, _ := include[0].(string); got != "message.output_text.logprobs" {
		t.Fatalf("unexpected include entry %q", got)
	}
}

func TestConvertChatCompletionsRequest_AllowedToolsToolChoiceMapped(t *testing.T) {
	req := map[string]any{
		"model": "gpt-5.2",
		"tool_choice": map[string]any{
			"type": "allowed_tools",
			"mode": "auto",
			"tools": []any{
				map[string]any{
					"type": "function",
					"function": map[string]any{
						"name": "get_weather",
					},
				},
			},
		},
		"messages": []any{
			map[string]any{
				"role":    "user",
				"content": "weather",
			},
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	convertedBody, _, _, _, err := ConvertChatCompletionsRequest(body, nil)
	if err != nil {
		t.Fatalf("convert request: %v", err)
	}

	var converted map[string]any
	if err := json.Unmarshal(convertedBody, &converted); err != nil {
		t.Fatalf("unmarshal converted: %v", err)
	}

	toolChoice, _ := converted["tool_choice"].(map[string]any)
	if toolChoice == nil {
		t.Fatal("expected tool_choice object")
	}
	if got, _ := toolChoice["type"].(string); got != "allowed_tools" {
		t.Fatalf("expected allowed_tools, got %q", got)
	}
	tools, _ := toolChoice["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 allowed tool, got %d", len(tools))
	}
	tool, _ := tools[0].(map[string]any)
	if got, _ := tool["name"].(string); got != "get_weather" {
		t.Fatalf("expected allowed tool name get_weather, got %q", got)
	}
}

func TestConvertChatCompletionsRequest_AudioRejected(t *testing.T) {
	req := map[string]any{
		"model":      "gpt-5.2",
		"modalities": []any{"text", "audio"},
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
	if _, _, _, _, err := ConvertChatCompletionsRequest(body, nil); err == nil {
		t.Fatal("expected audio request to be rejected")
	}
}

func TestConvertChatCompletionsRequest_InputAudioRejected(t *testing.T) {
	req := map[string]any{
		"model": "gpt-5.2",
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{
						"type": "input_audio",
						"input_audio": map[string]any{
							"data":   "abc",
							"format": "wav",
						},
					},
				},
			},
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if _, _, _, _, err := ConvertChatCompletionsRequest(body, nil); err == nil {
		t.Fatal("expected input_audio request to be rejected")
	}
}

func TestChatCompletionFromResponses_UsageMapped(t *testing.T) {
	resp := map[string]any{
		"id":    "resp_usage",
		"model": "gpt-5.4",
		"usage": map[string]any{
			"input_tokens":  11,
			"output_tokens": 7,
		},
		"output": []any{
			map[string]any{
				"type": "message",
				"content": []any{
					map[string]any{
						"type": "output_text",
						"text": "Hello",
					},
				},
			},
		},
	}

	chat := ChatCompletionFromResponses(resp, "")
	usage, _ := chat["usage"].(map[string]any)
	if usage == nil {
		t.Fatal("expected usage on chat completion")
	}
	if got, _ := usage["prompt_tokens"].(int64); got != 11 {
		t.Fatalf("expected prompt_tokens=11, got %v", usage["prompt_tokens"])
	}
	if got, _ := usage["completion_tokens"].(int64); got != 7 {
		t.Fatalf("expected completion_tokens=7, got %v", usage["completion_tokens"])
	}
	if got, _ := usage["total_tokens"].(int64); got != 18 {
		t.Fatalf("expected total_tokens=18, got %v", usage["total_tokens"])
	}
}

func TestChatCompletionUsageChunk_UsageMapped(t *testing.T) {
	chunk := ChatCompletionUsageChunk("chatcmpl_usage", "gpt-5.4", 1772773416, map[string]any{
		"input_tokens":  5,
		"output_tokens": 3,
	})

	choices, _ := chunk["choices"].([]any)
	if len(choices) != 0 {
		t.Fatalf("expected empty choices, got %d", len(choices))
	}
	usage, _ := chunk["usage"].(map[string]any)
	if usage == nil {
		t.Fatal("expected usage on usage chunk")
	}
	if got, _ := usage["prompt_tokens"].(int64); got != 5 {
		t.Fatalf("expected prompt_tokens=5, got %v", usage["prompt_tokens"])
	}
	if got, _ := usage["completion_tokens"].(int64); got != 3 {
		t.Fatalf("expected completion_tokens=3, got %v", usage["completion_tokens"])
	}
	if got, _ := usage["total_tokens"].(int64); got != 8 {
		t.Fatalf("expected total_tokens=8, got %v", usage["total_tokens"])
	}
}

func TestChatCompletionFromResponses_CustomToolCallMapped(t *testing.T) {
	resp := map[string]any{
		"id":         "resp_custom_tool",
		"created_at": float64(1772773416),
		"model":      "gpt-5.4",
		"output": []any{
			map[string]any{
				"id":      "ctc_1",
				"type":    "custom_tool_call",
				"call_id": "call_patch_1",
				"name":    "ApplyPatch",
				"input":   "*** Begin Patch\n*** End Patch\n",
			},
		},
	}

	chat := ChatCompletionFromResponses(resp, "")
	choices, _ := chat["choices"].([]any)
	if len(choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(choices))
	}
	choice, _ := choices[0].(map[string]any)
	if got, _ := choice["finish_reason"].(string); got != "tool_calls" {
		t.Fatalf("expected finish_reason=tool_calls, got %q", got)
	}

	message, _ := choice["message"].(map[string]any)
	if message == nil {
		t.Fatal("expected message object")
	}
	toolCalls, _ := message["tool_calls"].([]any)
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	tc, _ := toolCalls[0].(map[string]any)
	if got, _ := tc["id"].(string); got != "call_patch_1" {
		t.Fatalf("expected tool id call_patch_1, got %q", got)
	}
	fn, _ := tc["function"].(map[string]any)
	if fn == nil {
		t.Fatal("expected function object")
	}
	if got, _ := fn["name"].(string); got != "ApplyPatch" {
		t.Fatalf("expected function name ApplyPatch, got %q", got)
	}
	if got, _ := fn["arguments"].(string); got != "*** Begin Patch\n*** End Patch\n" {
		t.Fatalf("unexpected function arguments: %q", got)
	}
}

func TestChatCompletionFromResponses_LogprobsMapped(t *testing.T) {
	resp := map[string]any{
		"id":    "resp_logprobs",
		"model": "gpt-5.4",
		"output": []any{
			map[string]any{
				"type": "message",
				"content": []any{
					map[string]any{
						"type": "output_text",
						"text": "Hello",
						"logprobs": []any{
							map[string]any{
								"token":   "Hello",
								"logprob": -0.1,
								"bytes":   []any{72, 101, 108, 108, 111},
							},
						},
					},
				},
			},
		},
	}

	chat := ChatCompletionFromResponses(resp, "")
	choices, _ := chat["choices"].([]any)
	if len(choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(choices))
	}
	choice, _ := choices[0].(map[string]any)
	logprobs, _ := choice["logprobs"].(map[string]any)
	if logprobs == nil {
		t.Fatal("expected logprobs on choice")
	}
	content, _ := logprobs["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("expected 1 logprob token, got %d", len(content))
	}
	token0, _ := content[0].(map[string]any)
	if got, _ := token0["token"].(string); got != "Hello" {
		t.Fatalf("expected token Hello, got %q", got)
	}
}

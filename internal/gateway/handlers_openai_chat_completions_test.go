package gateway

import "testing"

func TestChatCompletionToolCallAccumulator_OutputItemDone(t *testing.T) {
	acc := newChatCompletionToolCallAccumulator()
	acc.Consume("response.output_item.done", map[string]any{
		"output_index": 1.0,
		"item": map[string]any{
			"type":      "function_call",
			"id":        "call_weather_1",
			"name":      "web_search",
			"arguments": `{"query":"杭州天气","max_results":5}`,
		},
	})

	toolCalls := acc.ToolCalls()
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0]["id"] != "call_weather_1" {
		t.Fatalf("unexpected id: %q", toolCalls[0]["id"])
	}
	if toolCalls[0]["name"] != "web_search" {
		t.Fatalf("unexpected name: %q", toolCalls[0]["name"])
	}
	if toolCalls[0]["arguments"] != `{"query":"杭州天气","max_results":5}` {
		t.Fatalf("unexpected arguments: %q", toolCalls[0]["arguments"])
	}
}

func TestChatCompletionToolCallAccumulator_ArgumentsDeltaAndDone(t *testing.T) {
	acc := newChatCompletionToolCallAccumulator()
	acc.Consume("response.output_item.added", map[string]any{
		"output_index": 2.0,
		"item": map[string]any{
			"type": "function_call",
			"id":   "call_2",
			"name": "fetch_url",
		},
	})
	acc.Consume("response.function_call_arguments.delta", map[string]any{
		"output_index": 2.0,
		"item_id":      "call_2",
		"delta":        `{"url":"https://example.com`,
	})
	acc.Consume("response.function_call_arguments.delta", map[string]any{
		"output_index": 2.0,
		"item_id":      "call_2",
		"delta":        `"}`,
	})

	toolCalls := acc.ToolCalls()
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0]["arguments"] != `{"url":"https://example.com"}` {
		t.Fatalf("unexpected accumulated arguments: %q", toolCalls[0]["arguments"])
	}

	acc.Consume("response.function_call_arguments.done", map[string]any{
		"output_index": 2.0,
		"item_id":      "call_2",
		"arguments":    `{"url":"https://example.com/docs"}`,
	})
	toolCalls = acc.ToolCalls()
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0]["arguments"] != `{"url":"https://example.com/docs"}` {
		t.Fatalf("unexpected finalized arguments: %q", toolCalls[0]["arguments"])
	}
}

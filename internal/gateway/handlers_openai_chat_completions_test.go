package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

func TestChatCompletionToolCallAccumulator_CustomToolCallDeltaAndDone(t *testing.T) {
	acc := newChatCompletionToolCallAccumulator()
	acc.Consume("response.output_item.added", map[string]any{
		"output_index": 3.0,
		"item": map[string]any{
			"type":    "custom_tool_call",
			"id":      "call_patch_1",
			"name":    "ApplyPatch",
			"call_id": "call_patch_1",
		},
	})
	acc.Consume("response.custom_tool_call_input.delta", map[string]any{
		"output_index": 3.0,
		"item_id":      "call_patch_1",
		"delta":        "*** Begin Patch\n",
	})
	acc.Consume("response.custom_tool_call_input.delta", map[string]any{
		"output_index": 3.0,
		"item_id":      "call_patch_1",
		"delta":        "*** End Patch\n",
	})

	toolCalls := acc.ToolCalls()
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0]["name"] != "ApplyPatch" {
		t.Fatalf("unexpected name: %q", toolCalls[0]["name"])
	}
	if toolCalls[0]["arguments"] != "*** Begin Patch\n*** End Patch\n" {
		t.Fatalf("unexpected accumulated arguments: %q", toolCalls[0]["arguments"])
	}

	acc.Consume("response.custom_tool_call_input.done", map[string]any{
		"output_index": 3.0,
		"item_id":      "call_patch_1",
		"input":        "*** Begin Patch\n*** Update File: a.go\n*** End Patch\n",
	})

	toolCalls = acc.ToolCalls()
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0]["arguments"] != "*** Begin Patch\n*** Update File: a.go\n*** End Patch\n" {
		t.Fatalf("unexpected finalized arguments: %q", toolCalls[0]["arguments"])
	}
}

func TestHandleOpenAIChatCompletionsJSONAsStreamingResponse_IncludeUsage(t *testing.T) {
	g := &Gateway{}
	upstream := map[string]any{
		"id":         "resp_json_usage",
		"created_at": float64(1772773416),
		"model":      "gpt-5.4",
		"usage": map[string]any{
			"input_tokens":  9,
			"output_tokens": 4,
		},
		"output": []any{
			map[string]any{
				"type": "message",
				"role": "assistant",
				"content": []any{
					map[string]any{
						"type": "output_text",
						"text": "Hello",
					},
				},
			},
		},
	}
	body, err := json.Marshal(upstream)
	if err != nil {
		t.Fatalf("marshal upstream: %v", err)
	}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}

	rec := httptest.NewRecorder()
	g.handleOpenAIChatCompletionsJSONAsStreamingResponse(rec, resp, &Account{}, "gpt-5.4", true)

	events := decodeChatCompletionSSEEvents(t, rec.Body.String())
	if len(events) == 0 {
		t.Fatal("expected SSE events")
	}
	usageChunk := events[len(events)-1]
	choices, _ := usageChunk["choices"].([]any)
	if len(choices) != 0 {
		t.Fatalf("expected final usage chunk to have empty choices, got %d", len(choices))
	}
	usage, _ := usageChunk["usage"].(map[string]any)
	if usage == nil {
		t.Fatal("expected usage on final chunk")
	}
	if got, _ := usage["prompt_tokens"].(float64); got != 9 {
		t.Fatalf("expected prompt_tokens=9, got %v", usage["prompt_tokens"])
	}
	if got, _ := usage["completion_tokens"].(float64); got != 4 {
		t.Fatalf("expected completion_tokens=4, got %v", usage["completion_tokens"])
	}
	if got, _ := usage["total_tokens"].(float64); got != 13 {
		t.Fatalf("expected total_tokens=13, got %v", usage["total_tokens"])
	}
}

func TestHandleOpenAIChatCompletionsStreamingResponse_IncludeUsage(t *testing.T) {
	g := &Gateway{}
	sse := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"Hello","response":{"id":"resp_stream_usage","model":"gpt-5.4"}}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_stream_usage","model":"gpt-5.4","usage":{"input_tokens":6,"output_tokens":2},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello"}]}]}}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n")

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}

	rec := httptest.NewRecorder()
	g.handleOpenAIChatCompletionsStreamingResponse(rec, resp, &Account{}, "gpt-5.4", true)

	events := decodeChatCompletionSSEEvents(t, rec.Body.String())
	if len(events) == 0 {
		t.Fatal("expected SSE events")
	}
	usageChunk := events[len(events)-1]
	choices, _ := usageChunk["choices"].([]any)
	if len(choices) != 0 {
		t.Fatalf("expected final usage chunk to have empty choices, got %d", len(choices))
	}
	usage, _ := usageChunk["usage"].(map[string]any)
	if usage == nil {
		t.Fatal("expected usage on final chunk")
	}
	if got, _ := usage["prompt_tokens"].(float64); got != 6 {
		t.Fatalf("expected prompt_tokens=6, got %v", usage["prompt_tokens"])
	}
	if got, _ := usage["completion_tokens"].(float64); got != 2 {
		t.Fatalf("expected completion_tokens=2, got %v", usage["completion_tokens"])
	}
	if got, _ := usage["total_tokens"].(float64); got != 8 {
		t.Fatalf("expected total_tokens=8, got %v", usage["total_tokens"])
	}
}

func decodeChatCompletionSSEEvents(t *testing.T, raw string) []map[string]any {
	t.Helper()

	var events []map[string]any
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		var evt map[string]any
		if err := json.Unmarshal([]byte(payload), &evt); err != nil {
			t.Fatalf("unmarshal SSE payload %q: %v", payload, err)
		}
		events = append(events, evt)
	}
	return events
}

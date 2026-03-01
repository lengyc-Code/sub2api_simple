package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPlatformFromModel(t *testing.T) {
	if got := platformFromModel("claude-sonnet-4-6"); got != "claude" {
		t.Fatalf("expected claude platform, got %q", got)
	}
	if got := platformFromModel("gpt-5.3-codex"); got != "openai" {
		t.Fatalf("expected openai platform, got %q", got)
	}
}

func TestExtractSSEText(t *testing.T) {
	claudeEvent := `{"type":"content_block_delta","delta":{"text":"hello"}}`
	if got := extractSSEText(claudeEvent, "claude", false); got != "hello" {
		t.Fatalf("unexpected claude delta: %q", got)
	}

	openAIEvent := `{"type":"response.output_text.delta","delta":"world"}`
	if got := extractSSEText(openAIEvent, "openai", false); got != "world" {
		t.Fatalf("unexpected openai delta: %q", got)
	}

	if got := extractSSEText(`{"type":"other"}`, "openai", false); got != "" {
		t.Fatalf("expected empty delta for unrelated event, got %q", got)
	}
}

func TestExtractSSETextOpenAICompletedFallback(t *testing.T) {
	completed := `{
		"type":"response.completed",
		"response":{
			"output":[
				{"content":[{"type":"output_text","text":"hello "}]},
				{"content":[{"type":"output_text","text":"world"}]}
			]
		}
	}`
	if got := extractSSEText(completed, "openai", false); got != "hello world" {
		t.Fatalf("unexpected completed fallback text: %q", got)
	}
	if got := extractSSEText(completed, "openai", true); got != "" {
		t.Fatalf("completed text should be ignored after streaming output, got %q", got)
	}
}

func TestChooseModel(t *testing.T) {
	input := strings.NewReader("9\n2\n")
	var output bytes.Buffer

	model, err := chooseModel([]string{"m1", "m2", "m3"}, input, &output)
	if err != nil {
		t.Fatalf("chooseModel returned error: %v", err)
	}
	if model != "m2" {
		t.Fatalf("expected m2, got %q", model)
	}
	if !strings.Contains(output.String(), "Invalid choice") {
		t.Fatalf("expected invalid choice prompt, got: %s", output.String())
	}
}

func TestStreamSSE(t *testing.T) {
	stream := strings.NewReader("" +
		"event: message\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hel\"}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"lo\"}\n\n" +
		"data: [DONE]\n")

	var out bytes.Buffer
	reply, err := streamSSE(stream, "openai", &out)
	if err != nil {
		t.Fatalf("streamSSE returned error: %v", err)
	}
	if reply != "Hello" {
		t.Fatalf("expected reply Hello, got %q", reply)
	}
	if out.String() != "Hello" {
		t.Fatalf("expected output Hello, got %q", out.String())
	}
}

func TestParseSSEDataLine(t *testing.T) {
	if data, ok := parseSSEDataLine("data: {\"x\":1}"); !ok || data != "{\"x\":1}" {
		t.Fatalf("unexpected parse result: ok=%v data=%q", ok, data)
	}
	if data, ok := parseSSEDataLine("event: message"); ok || data != "" {
		t.Fatalf("expected non-data line to be ignored: ok=%v data=%q", ok, data)
	}
}

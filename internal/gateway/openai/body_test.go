package openai

import (
	"encoding/json"
	"testing"
)

func TestPrepareBody_OAuthParamAdaptation(t *testing.T) {
	input := map[string]any{
		"model":                  "gpt-5.1",
		"stream":                 false,
		"store":                  true,
		"metadata":               map[string]any{"trace_id": "abc"},
		"prompt_cache_retention": "24h",
		"stream_options":         map[string]any{"include_usage": true},
		"user":                   "user_123",
		"reasoning_effort":       "minimal",
		"response_format": map[string]any{
			"type": "json_object",
		},
	}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	out := PrepareBody(body, PrepareOptions{
		OAuth: true,
	})

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	if v, _ := got["store"].(bool); v {
		t.Fatalf("expected store=false, got %v", got["store"])
	}
	if v, _ := got["stream"].(bool); !v {
		t.Fatalf("expected stream=true, got %v", got["stream"])
	}

	if _, ok := got["metadata"]; !ok {
		t.Fatal("expected metadata to be preserved")
	}
	if _, ok := got["prompt_cache_retention"]; !ok {
		t.Fatal("expected prompt_cache_retention to be preserved")
	}
	if _, ok := got["stream_options"]; ok {
		t.Fatal("expected stream_options to be removed")
	}

	if _, ok := got["reasoning_effort"]; ok {
		t.Fatal("expected reasoning_effort to be removed")
	}
	reasoning, _ := got["reasoning"].(map[string]any)
	if reasoning == nil {
		t.Fatal("expected reasoning object")
	}
	if effort, _ := reasoning["effort"].(string); effort != "none" {
		t.Fatalf("expected reasoning.effort=none, got %q", effort)
	}

	if _, ok := got["response_format"]; ok {
		t.Fatal("expected response_format to be removed")
	}
	textCfg, _ := got["text"].(map[string]any)
	if textCfg == nil {
		t.Fatal("expected text config")
	}
	format, _ := textCfg["format"].(map[string]any)
	if format == nil {
		t.Fatal("expected text.format")
	}
	if typ, _ := format["type"].(string); typ != "json_object" {
		t.Fatalf("expected text.format.type=json_object, got %q", typ)
	}

	if _, ok := got["user"]; ok {
		t.Fatal("expected user to be removed")
	}
	if sid, _ := got["safety_identifier"].(string); sid != "user_123" {
		t.Fatalf("expected safety_identifier=user_123, got %q", sid)
	}
}

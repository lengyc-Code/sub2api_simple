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
		"max_completion_tokens":  float64(128),
		"temperature":            float64(0.7),
		"top_p":                  float64(0.9),
		"frequency_penalty":      float64(0.2),
		"presence_penalty":       float64(0.1),
		"prompt_cache_retention": "24h",
		"stream_options":         map[string]any{"include_usage": true},
		"user":                   "user_123",
		"verbosity":              "medium",
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

	if _, ok := got["metadata"]; ok {
		t.Fatal("expected metadata removed for OAuth compatibility")
	}
	for _, key := range []string{"temperature", "top_p", "frequency_penalty", "presence_penalty"} {
		if _, ok := got[key]; ok {
			t.Fatalf("expected %s removed for OAuth compatibility", key)
		}
	}
	if _, ok := got["prompt_cache_retention"]; ok {
		t.Fatal("expected prompt_cache_retention to be removed for OAuth compatibility")
	}
	if streamOptions, _ := got["stream_options"].(map[string]any); streamOptions == nil {
		t.Fatal("expected stream_options preserved")
	}
	if gotMax, _ := got["max_output_tokens"].(float64); gotMax != 128 {
		t.Fatalf("expected max_output_tokens=128, got %v", got["max_output_tokens"])
	}
	if _, ok := got["max_completion_tokens"]; ok {
		t.Fatal("expected max_completion_tokens removed")
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
	if gotVerbosity, _ := textCfg["verbosity"].(string); gotVerbosity != "medium" {
		t.Fatalf("expected text.verbosity=medium, got %q", gotVerbosity)
	}

	if _, ok := got["user"]; ok {
		t.Fatal("expected user to be removed")
	}
	if _, ok := got["safety_identifier"]; ok {
		t.Fatal("expected safety_identifier removed for OAuth compatibility")
	}
}

func TestPrepareBody_OAuthResponseFormatJSONSchemaNestedFlatten(t *testing.T) {
	input := map[string]any{
		"model": "gpt-5.1",
		"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name": "answer_schema",
				"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"answer": map[string]any{"type": "string"},
					},
					"required": []any{"answer"},
				},
				"strict": true,
			},
		},
	}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	out := PrepareBody(body, PrepareOptions{OAuth: true})

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	textCfg, _ := got["text"].(map[string]any)
	if textCfg == nil {
		t.Fatal("expected text config")
	}
	format, _ := textCfg["format"].(map[string]any)
	if format == nil {
		t.Fatal("expected text.format")
	}
	if typ, _ := format["type"].(string); typ != "json_schema" {
		t.Fatalf("expected format.type=json_schema, got %q", typ)
	}
	if name, _ := format["name"].(string); name != "answer_schema" {
		t.Fatalf("expected format.name=answer_schema, got %q", name)
	}
	if _, ok := format["schema"]; !ok {
		t.Fatal("expected format.schema")
	}
}

func TestPrepareBody_OAuthResponseFormatJSONSchemaDefaultName(t *testing.T) {
	input := map[string]any{
		"model": "gpt-5.1",
		"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"schema": map[string]any{
					"type": "object",
				},
			},
		},
	}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	out := PrepareBody(body, PrepareOptions{OAuth: true})

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	textCfg, _ := got["text"].(map[string]any)
	if textCfg == nil {
		t.Fatal("expected text config")
	}
	format, _ := textCfg["format"].(map[string]any)
	if format == nil {
		t.Fatal("expected text.format")
	}
	if name, _ := format["name"].(string); name != "output_schema" {
		t.Fatalf("expected default format.name=output_schema, got %q", name)
	}
}

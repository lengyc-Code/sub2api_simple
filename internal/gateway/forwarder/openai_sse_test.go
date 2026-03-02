package forwarder

import "testing"

func TestOpenAISSEAccumulator_ConsumeLine_ErrorEvent(t *testing.T) {
	acc := NewOpenAISSEAccumulator("gpt-5.2")
	err := acc.ConsumeLine(`data: {"type":"error","error":{"message":"Model not found","type":"invalid_request_error","code":"model_not_found"}}`)
	if err == nil {
		t.Fatal("expected stream error, got nil")
	}

	streamErr, ok := AsOpenAIStreamError(err)
	if !ok {
		t.Fatalf("expected OpenAIStreamError, got %T", err)
	}
	if streamErr.Message != "Model not found" {
		t.Fatalf("unexpected message: %q", streamErr.Message)
	}
	if streamErr.Type != "invalid_request_error" {
		t.Fatalf("unexpected type: %q", streamErr.Type)
	}
	if streamErr.Code != "model_not_found" {
		t.Fatalf("unexpected code: %q", streamErr.Code)
	}
}

func TestOpenAISSEAccumulator_ConsumeLine_InvalidJSONIgnored(t *testing.T) {
	acc := NewOpenAISSEAccumulator("gpt-5.2")
	err := acc.ConsumeLine(`data: {"type":`)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

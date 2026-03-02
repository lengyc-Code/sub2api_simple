package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"elysiafly.com/sub2api_simple/internal/gateway/forwarder"
)

func TestWriteOpenAISSEAggregationError_StreamError(t *testing.T) {
	g := &Gateway{}
	rec := httptest.NewRecorder()

	g.writeOpenAISSEAggregationError(rec, &forwarder.OpenAIStreamError{
		Message: "Model not found",
		Type:    "invalid_request_error",
		Code:    "model_not_found",
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	errObj, _ := payload["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("expected error object, got %#v", payload["error"])
	}
	if got, _ := errObj["type"].(string); got != "invalid_request_error" {
		t.Fatalf("unexpected error.type: %q", got)
	}
	if got, _ := errObj["message"].(string); got != "Model not found" {
		t.Fatalf("unexpected error.message: %q", got)
	}
}

func TestWriteOpenAISSEAggregationError_GenericError(t *testing.T) {
	g := &Gateway{}
	rec := httptest.NewRecorder()

	g.writeOpenAISSEAggregationError(rec, errors.New("stream timeout"))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected status %d, got %d", http.StatusBadGateway, rec.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	errObj, _ := payload["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("expected error object, got %#v", payload["error"])
	}
	if got, _ := errObj["type"].(string); got != "api_error" {
		t.Fatalf("unexpected error.type: %q", got)
	}
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "Failed to aggregate upstream stream response") {
		t.Fatalf("unexpected error.message: %q", msg)
	}
	if !strings.Contains(msg, "stream timeout") {
		t.Fatalf("expected underlying error in message, got %q", msg)
	}
}

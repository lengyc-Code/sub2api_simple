// Sub2API Standalone 测试客户端
//
// 用法：go test -v -run TestClient -timeout 120s
//
// 需要先启动 sub2api-standalone 服务端：
//
//	go run . -config config.json
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

const testBaseURL = "http://localhost:8080"

// getAuthToken reads the auth token from the TEST_AUTH_TOKEN env var,
// or falls back to reading the first token from config.json.
func getAuthToken(t *testing.T) string {
	t.Helper()
	if tok := os.Getenv("TEST_AUTH_TOKEN"); tok != "" {
		return tok
	}
	data, err := os.ReadFile("config.json")
	if err != nil {
		t.Fatalf("Cannot read config.json and TEST_AUTH_TOKEN not set: %v", err)
	}
	var cfg struct {
		AuthTokens []string `json:"auth_tokens"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("Cannot parse config.json: %v", err)
	}
	if len(cfg.AuthTokens) == 0 {
		t.Fatal("No auth_tokens in config.json and TEST_AUTH_TOKEN not set")
	}
	return cfg.AuthTokens[0]
}

// ── Health Check ──

func TestHealthCheck(t *testing.T) {
	resp, err := http.Get(testBaseURL + "/health")
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	prettyPrint(t, "Health", result)
}

// ── Models ──

func TestModels(t *testing.T) {
	req, _ := http.NewRequest("GET", testBaseURL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+getAuthToken(t))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("models request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	prettyPrint(t, "Models", result)
}

// ── Claude Messages API (streaming) ──

func TestClaudeMessagesStream(t *testing.T) {
	payload := map[string]any{
		"model":      "claude-sonnet-4-5",
		"max_tokens": 256,
		"stream":     true,
		"messages": []map[string]any{
			{"role": "user", "content": "Say hello in 3 different languages, keep it brief."},
		},
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", testBaseURL+"/v1/messages", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+getAuthToken(t))
	req.Header.Set("Content-Type", "application/json")

	t.Logf("→ POST /v1/messages (stream=true, model=%s)", payload["model"])

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	t.Logf("← Status: %d, Content-Type: %s", resp.StatusCode, resp.Header.Get("Content-Type"))

	if resp.StatusCode == 401 || resp.StatusCode == 503 {
		body, _ := io.ReadAll(resp.Body)
		t.Skipf("Upstream returned %d (check refresh_token/oauth_token/api_key in config.json): %s", resp.StatusCode, body)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	readSSEStream(t, resp.Body, "Claude")
}

// ── Claude Messages API (non-streaming) ──

func TestClaudeMessagesNonStream(t *testing.T) {
	payload := map[string]any{
		"model":      "claude-sonnet-4-5",
		"max_tokens": 64,
		"stream":     false,
		"messages": []map[string]any{
			{"role": "user", "content": "What is 2+2? Answer in one word."},
		},
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", testBaseURL+"/v1/messages", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+getAuthToken(t))
	req.Header.Set("Content-Type", "application/json")

	t.Logf("→ POST /v1/messages (stream=false, model=%s)", payload["model"])

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	t.Logf("← Status: %d", resp.StatusCode)

	if resp.StatusCode == 401 || resp.StatusCode == 503 {
		t.Skipf("Upstream returned %d (check refresh_token/oauth_token/api_key in config.json): %s", resp.StatusCode, respBody)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, respBody)
	}

	var result map[string]any
	json.Unmarshal(respBody, &result)
	prettyPrint(t, "Claude Response", result)
}

// ── OpenAI Responses API (streaming) ──

func TestOpenAIResponsesStream(t *testing.T) {
	payload := map[string]any{
		"model":        "gpt-5.1-codex-mini",
		"stream":       true,
		"instructions": "You are a helpful assistant. Keep responses brief.",
		"input": []map[string]any{
			{"role": "user", "content": "Say hello in 3 different languages, keep it brief."},
		},
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", testBaseURL+"/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+getAuthToken(t))
	req.Header.Set("Content-Type", "application/json")

	t.Logf("→ POST /v1/responses (stream=true, model=%s)", payload["model"])

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	t.Logf("← Status: %d, Content-Type: %s", resp.StatusCode, resp.Header.Get("Content-Type"))

	if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 503 {
		body, _ := io.ReadAll(resp.Body)
		t.Skipf("Upstream returned %d (check refresh_token/oauth_token in config.json): %s", resp.StatusCode, body)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	readSSEStream(t, resp.Body, "OpenAI")
}

// ── OpenAI Responses API (non-streaming via API Key) ──

func TestOpenAIResponsesNonStream(t *testing.T) {
	payload := map[string]any{
		"model":        "gpt-5.1-codex-mini",
		"stream":       false,
		"instructions": "You are a helpful assistant. Answer concisely.",
		"input": []map[string]any{
			{"role": "user", "content": "What is 2+2? Answer in one word."},
		},
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", testBaseURL+"/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+getAuthToken(t))
	req.Header.Set("Content-Type", "application/json")

	t.Logf("→ POST /v1/responses (stream=false, model=%s)", payload["model"])

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	t.Logf("← Status: %d", resp.StatusCode)

	if resp.StatusCode == 401 || resp.StatusCode == 503 {
		t.Skipf("Upstream returned %d (check refresh_token/oauth_token/api_key in config.json): %s", resp.StatusCode, respBody)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, respBody)
	}

	var result map[string]any
	json.Unmarshal(respBody, &result)
	prettyPrint(t, "OpenAI Response", result)
}

// ── Auth Rejection ──

func TestAuthRejection(t *testing.T) {
	endpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/v1/models"},
		{"POST", "/v1/messages"},
		{"POST", "/v1/responses"},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			req, _ := http.NewRequest(ep.method, testBaseURL+ep.path, strings.NewReader("{}"))
			req.Header.Set("Authorization", "Bearer invalid-token")
			req.Header.Set("Content-Type", "application/json")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != 401 {
				t.Errorf("expected 401, got %d", resp.StatusCode)
			} else {
				t.Logf("✓ %s %s correctly rejected with 401", ep.method, ep.path)
			}
		})
	}
}

// ── Run all tests as a client command ──

func TestClient(t *testing.T) {
	if _, err := http.Get(testBaseURL + "/health"); err != nil {
		t.Skipf("Server not running at %s, skipping integration tests. Start with: go run . -config config.json", testBaseURL)
	}

	t.Run("Health", TestHealthCheck)
	t.Run("Models", TestModels)
	t.Run("AuthRejection", TestAuthRejection)

	hasAnthropic := os.Getenv("TEST_CLAUDE") == "1"
	hasOpenAI := os.Getenv("TEST_OPENAI") == "1"

	if !hasAnthropic && !hasOpenAI {
		hasOpenAI = true
		t.Log("Hint: set TEST_CLAUDE=1 or TEST_OPENAI=1 to select platform tests. Defaulting to OpenAI.")
	}

	if hasAnthropic {
		t.Run("Claude/Stream", TestClaudeMessagesStream)
		t.Run("Claude/NonStream", TestClaudeMessagesNonStream)
	}
	if hasOpenAI {
		t.Run("OpenAI/Stream", TestOpenAIResponsesStream)
	}
}

// ── Helpers ──

func readSSEStream(t *testing.T, body io.Reader, platform string) {
	t.Helper()
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)

	eventCount := 0
	var textParts []string

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		eventCount++
		if eventCount <= 5 || strings.Contains(line, "[DONE]") {
			t.Logf("  SSE [%d]: %.200s", eventCount, line)
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var event map[string]any
		if json.Unmarshal([]byte(data), &event) != nil {
			continue
		}

		text := extractTextFromSSE(event, platform)
		if text != "" {
			textParts = append(textParts, text)
		}
	}

	if err := scanner.Err(); err != nil {
		t.Logf("⚠ scanner error: %v", err)
	}

	assembled := strings.Join(textParts, "")
	t.Logf("  Total SSE events: %d", eventCount)
	t.Logf("  Assembled text: %s", truncate(assembled, 500))
}

func extractTextFromSSE(event map[string]any, platform string) string {
	switch platform {
	case "Claude":
		// Claude SSE: event type "content_block_delta" → delta.text
		if eventType, _ := event["type"].(string); eventType == "content_block_delta" {
			if delta, ok := event["delta"].(map[string]any); ok {
				if text, ok := delta["text"].(string); ok {
					return text
				}
			}
		}
	case "OpenAI":
		// OpenAI SSE: type "response.output_text.delta" → delta
		if eventType, _ := event["type"].(string); eventType == "response.output_text.delta" {
			if delta, ok := event["delta"].(string); ok {
				return delta
			}
		}
	}
	return ""
}

func prettyPrint(t *testing.T, label string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "  ", "  ")
	if err != nil {
		t.Logf("%s: %v", label, v)
		return
	}
	out := string(b)
	if len(out) > 2000 {
		out = out[:2000] + "\n  ... (truncated)"
	}
	t.Logf("%s:\n  %s", label, out)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + fmt.Sprintf("... (%d chars total)", len(s))
}

package claude

import (
	"encoding/json"
	"net/http"
	"strings"
)

type PrepareOptions struct {
	Model            string
	OAuth            bool
	ModelIDOverrides map[string]string
	SystemPrompt     string
	ApplyExtraParams func(parsed map[string]any, model string) bool
}

func PrepareBody(body []byte, opts PrepareOptions) []byte {
	if !opts.OAuth && opts.ApplyExtraParams == nil {
		return body
	}

	modified := false
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return body
	}

	effectiveModel := opts.Model
	if currentModel, _ := parsed["model"].(string); currentModel != "" {
		effectiveModel = currentModel
	}

	if opts.OAuth {
		if !strings.Contains(strings.ToLower(effectiveModel), "haiku") {
			if needsSystemPromptInjection(parsed, opts.SystemPrompt) {
				injectSystemPrompt(parsed, opts.SystemPrompt)
				modified = true
			}
		}

		if mapped, ok := opts.ModelIDOverrides[effectiveModel]; ok {
			parsed["model"] = mapped
			effectiveModel = mapped
			modified = true
		}
	}

	if opts.ApplyExtraParams != nil && opts.ApplyExtraParams(parsed, effectiveModel) {
		modified = true
	}

	if !modified {
		return body
	}
	newBody, err := json.Marshal(parsed)
	if err != nil {
		return body
	}
	return newBody
}

func needsSystemPromptInjection(parsed map[string]any, systemPrompt string) bool {
	sys, ok := parsed["system"]
	if !ok {
		return true
	}
	switch v := sys.(type) {
	case string:
		return !strings.Contains(v, systemPrompt)
	case []any:
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if text, _ := m["text"].(string); strings.Contains(text, systemPrompt) {
					return false
				}
			}
		}
		return true
	}
	return true
}

func injectSystemPrompt(parsed map[string]any, systemPrompt string) {
	sys, ok := parsed["system"]
	if !ok || sys == nil {
		parsed["system"] = systemPrompt
		return
	}
	switch v := sys.(type) {
	case string:
		parsed["system"] = systemPrompt + "\n\n" + v
	case []any:
		block := map[string]any{"type": "text", "text": systemPrompt}
		parsed["system"] = append([]any{block}, v...)
	}
}

type OAuthHeaderOptions struct {
	Model                   string
	Stream                  bool
	DefaultHeaders          map[string]string
	BetaOAuth               string
	BetaClaudeCode          string
	BetaInterleavedThinking string
}

func ApplyOAuthHeaders(header http.Header, opts OAuthHeaderOptions) {
	for k, v := range opts.DefaultHeaders {
		header.Set(k, v)
	}

	if strings.Contains(strings.ToLower(opts.Model), "haiku") {
		header.Set("anthropic-beta", opts.BetaOAuth+","+opts.BetaInterleavedThinking)
	} else {
		header.Set("anthropic-beta", opts.BetaClaudeCode+","+opts.BetaOAuth+","+opts.BetaInterleavedThinking)
	}

	if opts.Stream {
		header.Set("accept", "text/event-stream")
	} else {
		header.Set("accept", "application/json")
	}
}

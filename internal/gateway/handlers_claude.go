package gateway

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	claudehandler "elysiafly.com/sub2api_simple/internal/gateway/claude"
)

func (g *Gateway) handleClaudeMessages(w http.ResponseWriter, r *http.Request) {
	if !g.authenticate(r) {
		writeClaudeError(w, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodyMiB<<20))
	if err != nil {
		writeClaudeError(w, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	defer r.Body.Close()

	if len(body) == 0 {
		writeClaudeError(w, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	model, stream, err := parseRequestEssentials(body)
	if err != nil {
		writeClaudeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if model == "" {
		writeClaudeError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}

	authToken := extractBearerToken(r)
	sessionKey := computeSessionHash(platformAnthropic, authToken, model)

	g.forwardWithFailover(w, r, platformAnthropic, model, stream, body, sessionKey,
		g.prepareClaudeBody, g.buildClaudeUpstreamRequest, writeClaudeError)
}

func (g *Gateway) prepareClaudeBody(account *Account, body []byte, model string) []byte {
	var applyExtra func(parsed map[string]any, model string) bool
	if g != nil {
		applyExtra = g.applyModelExtraParams
	}
	return claudehandler.PrepareBody(body, claudehandler.PrepareOptions{
		Model:            model,
		OAuth:            account.IsOAuth(),
		ModelIDOverrides: claudeModelIDOverrides,
		SystemPrompt:     claudeCodeSystemPrompt,
		ApplyExtraParams: applyExtra,
	})
}

func (g *Gateway) buildClaudeUpstreamRequest(ctx context.Context, account *Account, body []byte, model string, stream bool) (*http.Request, error) {
	targetURL := account.GetClaudeTargetURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	if account.IsOAuth() {
		req.Header.Set("Authorization", "Bearer "+account.GetStaticToken())
	} else {
		req.Header.Set("x-api-key", account.GetStaticToken())
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", anthropicVersion)

	if account.IsOAuth() {
		claudehandler.ApplyOAuthHeaders(req.Header, claudehandler.OAuthHeaderOptions{
			Model:                   model,
			Stream:                  stream,
			DefaultHeaders:          claudeOAuthDefaultHeaders,
			BetaOAuth:               betaOAuth,
			BetaClaudeCode:          betaClaudeCode,
			BetaInterleavedThinking: betaInterleavedThinking,
		})
		if strings.TrimSpace(req.Header.Get("accept")) == "" {
			req.Header.Set("accept", "application/json")
		}
	}

	return req, nil
}

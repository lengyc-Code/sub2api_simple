package gateway

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	openaihandler "elysiafly.com/sub2api_simple/internal/gateway/openai"
)

func (g *Gateway) handleOpenAIResponses(w http.ResponseWriter, r *http.Request) {
	if !g.authenticate(r) {
		writeOpenAIError(w, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodyMiB<<20))
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	defer r.Body.Close()

	if len(body) == 0 {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	model, stream, err := parseRequestEssentials(body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if model == "" {
		model = "gpt-5.1"
	}

	authToken := extractBearerToken(r)
	sessionKey := computeSessionHash(platformOpenAI, authToken, model)

	g.forwardWithFailover(w, r, platformOpenAI, model, stream, body, sessionKey,
		g.prepareOpenAIBody, g.buildOpenAIUpstreamRequest, writeOpenAIError)
}

func (g *Gateway) prepareOpenAIBody(account *Account, body []byte, model string) []byte {
	var applyExtra func(parsed map[string]any, model string) bool
	var defaultInstruction func(model string) (string, bool)
	if g != nil {
		applyExtra = g.applyModelExtraParams
		if g.cfg != nil {
			defaultInstruction = g.cfg.getOpenAIDefaultInstruction
		}
	}

	return openaihandler.PrepareBody(body, openaihandler.PrepareOptions{
		FallbackModel:      model,
		OAuth:              account.IsOAuth(),
		ModelMap:           codexModelMap,
		ApplyExtraParams:   applyExtra,
		DefaultInstruction: defaultInstruction,
	})
}

func normalizeCodexModel(model string) string {
	return openaihandler.NormalizeModel(model, codexModelMap)
}

func (g *Gateway) buildOpenAIUpstreamRequest(ctx context.Context, account *Account, body []byte, model string, stream bool) (*http.Request, error) {
	targetURL := account.GetOpenAITargetURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	if account.IsOAuth() {
		accessToken, chatGPTAccountID, tokenErr := g.getOpenAIAccessToken(ctx, account)
		if tokenErr != nil {
			return nil, fmt.Errorf("get access_token: %w", tokenErr)
		}

		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Host = "chatgpt.com"

		if chatGPTAccountID != "" {
			req.Header.Set("chatgpt-account-id", chatGPTAccountID)
		}

		req.Header.Set("OpenAI-Beta", "responses=experimental")
		req.Header.Set("originator", "codex_cli_rs")
		req.Header.Set("accept", "text/event-stream")
		req.Header.Set("User-Agent", codexCLIUserAgent)
	} else {
		req.Header.Set("Authorization", "Bearer "+account.GetStaticToken())
	}

	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

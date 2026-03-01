package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"elysiafly.com/sub2api_simple/internal/gateway/oauth"
)

func (g *Gateway) handleModels(w http.ResponseWriter, r *http.Request) {
	if !g.authenticate(r) {
		writeClaudeError(w, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	hasAnthropic, hasOpenAI := false, false
	for _, a := range g.cfg.Accounts {
		if a.Platform == platformAnthropic {
			hasAnthropic = true
		}
		if a.Platform == platformOpenAI {
			hasOpenAI = true
		}
	}

	allModels := make([]map[string]string, 0, len(claudeModels)+len(openaiModels))
	if hasAnthropic {
		allModels = append(allModels, claudeModels...)
	}
	if hasOpenAI {
		allModels = append(allModels, openaiModels...)
	}
	if len(allModels) == 0 {
		allModels = claudeModels
	}

	resp := map[string]any{
		"data":     allModels,
		"has_more": false,
		"first_id": allModels[0]["id"],
		"last_id":  allModels[len(allModels)-1]["id"],
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

//
// GET /health
//

func (g *Gateway) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := map[string]any{
		"status":   "ok",
		"accounts": len(g.manager.accounts),
	}
	accounts := make([]map[string]any, 0, len(g.manager.accounts))
	for _, a := range g.manager.accounts {
		accounts = append(accounts, map[string]any{
			"name":         a.Config.Name,
			"platform":     a.Config.Platform,
			"type":         a.Config.Type,
			"priority":     a.Config.Priority,
			"concurrency":  a.Config.Concurrency,
			"active":       atomic.LoadInt32(&a.activeRequests),
			"rate_limited": a.IsRateLimited(),
		})
	}
	status["account_details"] = accounts

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

//
// OAuth Login Handlers ?Browser-based Token Acquisition
//

// ensureCallbackListener starts a temporary HTTP server on port 1455
// to receive the OAuth callback from OpenAI. The Codex CLI OAuth client
// (app_EMoamEEZ73f0CkXaXp7hrann) requires redirect_uri=http://localhost:1455/auth/callback.
// The listener auto-shuts down after the OAuth session TTL expires.
func (g *Gateway) ensureCallbackListener() error {
	g.callbackMu.Lock()
	defer g.callbackMu.Unlock()

	if g.callbackSrv != nil {
		return nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", g.handleAuthCallback)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", openaiCallbackPort),
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	g.callbackSrv = srv

	go func() {
		log.Printf("[auth] callback listener started on :%d", openaiCallbackPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[auth] callback listener error: %v", err)
		}
		g.callbackMu.Lock()
		g.callbackSrv = nil
		g.callbackMu.Unlock()
	}()

	// Auto-shutdown after session TTL to avoid leaving a dangling listener
	go func() {
		time.Sleep(oauthSessionTTL + 30*time.Second)
		g.shutdownCallbackListener()
	}()

	return nil
}

func (g *Gateway) shutdownCallbackListener() {
	g.callbackMu.Lock()
	srv := g.callbackSrv
	g.callbackMu.Unlock()

	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("[auth] callback listener shutdown error: %v", err)
		}
		log.Printf("[auth] callback listener on :%d stopped", openaiCallbackPort)
	}
}

// handleAuthLogin initiates the OpenAI OAuth login flow.
//
//	GET /auth/login?account=<name>
//
// Generates PKCE credentials, starts a callback listener on port 1455,
// and redirects the browser to OpenAI's authorization page.
func (g *Gateway) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	accountName := r.URL.Query().Get("account")

	if accountName == "" {
		for _, a := range g.manager.accounts {
			if a.IsOpenAIOAuth() {
				accountName = a.Config.Name
				break
			}
		}
	}
	if accountName == "" {
		writeAuthPage(w, http.StatusBadRequest, "Error",
			"No OpenAI OAuth account found. Add an account with platform=openai, type=oauth in config.")
		return
	}

	var account *Account
	for _, a := range g.manager.accounts {
		if a.Config.Name == accountName {
			account = a
			break
		}
	}
	if account == nil {
		writeAuthPage(w, http.StatusNotFound, "Error",
			fmt.Sprintf("Account %q not found in config.", accountName))
		return
	}
	if !account.IsOpenAI() || !account.IsOAuth() {
		writeAuthPage(w, http.StatusBadRequest, "Error",
			fmt.Sprintf("Account %q is not an OpenAI OAuth account.", accountName))
		return
	}

	state, err := oauth.GenerateState()
	if err != nil {
		writeAuthPage(w, http.StatusInternalServerError, "Error", "Failed to generate OAuth state.")
		return
	}
	codeVerifier, err := oauth.GenerateCodeVerifier()
	if err != nil {
		writeAuthPage(w, http.StatusInternalServerError, "Error", "Failed to generate PKCE verifier.")
		return
	}
	codeChallenge := oauth.GenerateCodeChallenge(codeVerifier)

	clientID := account.Config.ClientID
	if clientID == "" {
		clientID = openaiDefaultClientID
	}

	// Start callback listener on port 1455 (OpenAI Codex CLI registered redirect)
	if err := g.ensureCallbackListener(); err != nil {
		writeAuthPage(w, http.StatusInternalServerError, "Error",
			fmt.Sprintf("Failed to start callback listener on port %d: %v", openaiCallbackPort, err))
		return
	}

	g.oauthSessions.Set(&oauth.PendingSession{
		AccountName:  accountName,
		State:        state,
		CodeVerifier: codeVerifier,
		ClientID:     clientID,
		RedirectURI:  openaiCallbackRedirect,
		CreatedAt:    time.Now(),
	})

	authURL := oauth.BuildAuthURL(state, codeChallenge, openaiCallbackRedirect, clientID)
	log.Printf("[auth] login initiated for account %q, redirecting to OpenAI", accountName)
	log.Printf("[auth] authorize URL: %s", authURL)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// handleAuthCallback handles the OAuth callback from OpenAI on port 1455.
//
//	GET /auth/callback?code=<code>&state=<state>
//
// Exchanges the authorization code for tokens using PKCE, saves them
// to token_state.json, and updates the in-memory token cache.
func (g *Gateway) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	oauthErr := r.URL.Query().Get("error")
	oauthErrDesc := r.URL.Query().Get("error_description")

	if oauthErr != "" {
		msg := fmt.Sprintf("OpenAI denied the authorization request: %s", oauthErr)
		if oauthErrDesc != "" {
			msg += " ?" + oauthErrDesc
		}
		log.Printf("[auth] callback error: %s", msg)
		writeAuthPage(w, http.StatusBadRequest, "Authorization Failed", msg)
		return
	}

	if code == "" || state == "" {
		writeAuthPage(w, http.StatusBadRequest, "Invalid Callback",
			"Missing code or state parameter. Please try logging in again.")
		return
	}

	sess, ok := g.oauthSessions.GetAndDelete(state)
	if !ok {
		writeAuthPage(w, http.StatusBadRequest, "Session Expired",
			"OAuth session not found or expired. Please try logging in again.")
		return
	}

	var account *Account
	for _, a := range g.manager.accounts {
		if a.Config.Name == sess.AccountName {
			account = a
			break
		}
	}
	proxyURL := ""
	if account != nil {
		proxyURL = account.Config.Proxy
	}

	tokenResp, err := g.tokenManager.ExchangeAuthCode(
		r.Context(), code, sess.CodeVerifier, sess.RedirectURI, sess.ClientID, proxyURL)
	if err != nil {
		log.Printf("[auth] token exchange failed for %q: %v", sess.AccountName, err)
		writeAuthPage(w, http.StatusBadGateway, "Token Exchange Failed",
			fmt.Sprintf("Failed to exchange authorization code: %v", err))
		return
	}

	fallbackChatGPTAccountID := ""
	if account != nil {
		fallbackChatGPTAccountID = account.Config.ChatGPTAccountID
	}
	chatGPTAccountID, err := g.tokenManager.SaveOAuthLogin(sess.AccountName, fallbackChatGPTAccountID, tokenResp)
	if err != nil {
		log.Printf("[auth] failed to save oauth login for %q: %v", sess.AccountName, err)
		writeAuthPage(w, http.StatusBadGateway, "Token Save Failed",
			fmt.Sprintf("Failed to save exchanged tokens: %v", err))
		return
	}

	log.Printf("[auth] login successful for %q (chatgpt_account_id=%s, expires_in=%ds)",
		sess.AccountName, chatGPTAccountID, tokenResp.ExpiresIn)

	writeAuthPage(w, http.StatusOK, "Login Successful", fmt.Sprintf(
		`Account <b>%s</b> has been authorized successfully.<br><br>`+
			`<b>ChatGPT Account ID:</b> %s<br>`+
			`<b>Token expires in:</b> %d seconds<br>`+
			`<b>Refresh token:</b> saved to token_state.json<br><br>`+
			`You can close this page now. The gateway will automatically refresh tokens before expiry.`,
		html.EscapeString(sess.AccountName),
		html.EscapeString(chatGPTAccountID),
		tokenResp.ExpiresIn,
	))

	// Schedule callback listener shutdown (give a moment for response to be sent)
	go func() {
		time.Sleep(2 * time.Second)
		g.shutdownCallbackListener()
	}()
}

// writeAuthPage writes a simple HTML page for the OAuth flow results.
func writeAuthPage(w http.ResponseWriter, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	isSuccess := status == http.StatusOK
	bgColor, icon := "#1a1a2e", "&#10060;"
	if isSuccess {
		bgColor, icon = "#0f3460", "&#9989;"
	}
	fmt.Fprintf(w, `<!DOCTYPE html>
<html><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Sub2API ?%s</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;
background:%s;color:#e0e0e0;display:flex;justify-content:center;align-items:center;min-height:100vh}
.card{background:#16213e;border-radius:16px;padding:48px;max-width:520px;width:90%%;
box-shadow:0 8px 32px rgba(0,0,0,.3);text-align:center}
.icon{font-size:48px;margin-bottom:16px}
h1{font-size:24px;margin-bottom:16px;color:#e0e0e0}
.msg{font-size:15px;line-height:1.7;color:#a0a0b0}
.msg b{color:#e0e0e0}
</style></head>
<body><div class="card">
<div class="icon">%s</div>
<h1>%s</h1>
<div class="msg">%s</div>
</div></body></html>`, html.EscapeString(title), bgColor, icon, html.EscapeString(title), message)
}

//
// HTTP Client Helper
//

func (g *Gateway) getHTTPClient(account *Account) *http.Client {
	if account.Config.Proxy == "" {
		return g.httpClient
	}

	proxyURL, err := url.Parse(account.Config.Proxy)
	if err != nil {
		log.Printf("[gateway] invalid proxy URL for %q: %v", account.Config.Name, err)
		return g.httpClient
	}

	return &http.Client{
		Transport: &http.Transport{
			Proxy:               http.ProxyURL(proxyURL),
			MaxIdleConns:        20,
			MaxIdleConnsPerHost: 5,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}

//
// Error Response Helpers
//

// writeClaudeError writes an error in the Anthropic Claude API format.
func writeClaudeError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    errType,
			"message": message,
		},
	})
}

// writeOpenAIError writes an error in the OpenAI API format.
func writeOpenAIError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"type":    errType,
			"message": message,
		},
	})
}

// writeErrorJSON writes a generic error (for non-platform-specific endpoints).
func writeErrorJSON(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"type":    errType,
			"message": message,
		},
	})
}

//
// General Helpers
//

func parseRequestEssentials(body []byte) (model string, stream bool, err error) {
	var req struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return "", false, fmt.Errorf("invalid JSON: %w", err)
	}
	return req.Model, req.Stream, nil
}

func (c *Config) getOpenAIDefaultInstruction(model string) (string, bool) {
	if c == nil || len(c.OpenAIDefaultInstructions) == 0 {
		return "", false
	}

	if instruction, ok := getInstructionByModel(c.OpenAIDefaultInstructions, model); ok {
		return instruction, true
	}

	normalized := normalizeCodexModel(model)
	if normalized != "" && !strings.EqualFold(normalized, model) {
		if instruction, ok := getInstructionByModel(c.OpenAIDefaultInstructions, normalized); ok {
			return instruction, true
		}
	}

	if instruction, ok := getInstructionByModel(c.OpenAIDefaultInstructions, "*"); ok {
		return instruction, true
	}

	return "", false
}

func (c *Config) getModelExtraParams(model string) (map[string]any, bool) {
	if c == nil || len(c.ModelExtraParams) == 0 {
		return nil, false
	}

	if params, ok := getModelParamsByModel(c.ModelExtraParams, model); ok {
		return params, true
	}

	normalized := normalizeCodexModel(model)
	if normalized != "" && !strings.EqualFold(normalized, model) {
		if params, ok := getModelParamsByModel(c.ModelExtraParams, normalized); ok {
			return params, true
		}
	}

	if mapped, ok := claudeModelIDOverrides[model]; ok {
		if params, ok := getModelParamsByModel(c.ModelExtraParams, mapped); ok {
			return params, true
		}
	}
	for alias, mapped := range claudeModelIDOverrides {
		if strings.EqualFold(mapped, model) {
			if params, ok := getModelParamsByModel(c.ModelExtraParams, alias); ok {
				return params, true
			}
			break
		}
	}

	if params, ok := getModelParamsByModel(c.ModelExtraParams, "*"); ok {
		return params, true
	}

	return nil, false
}

func getInstructionByModel(instructions map[string]string, model string) (string, bool) {
	if value, ok := instructions[model]; ok {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed, true
		}
	}
	for key, value := range instructions {
		if strings.EqualFold(key, model) {
			trimmed := strings.TrimSpace(value)
			if trimmed != "" {
				return trimmed, true
			}
			return "", false
		}
	}
	return "", false
}

func getModelParamsByModel(params map[string]map[string]any, model string) (map[string]any, bool) {
	if model == "" {
		return nil, false
	}
	if value, ok := params[model]; ok && len(value) > 0 {
		return value, true
	}
	for key, value := range params {
		if strings.EqualFold(key, model) {
			if len(value) > 0 {
				return value, true
			}
			return nil, false
		}
	}
	return nil, false
}

func (g *Gateway) applyModelExtraParams(parsed map[string]any, model string) bool {
	if g == nil || g.cfg == nil || parsed == nil {
		return false
	}
	defaultParams, ok := g.cfg.getModelExtraParams(model)
	if !ok {
		return false
	}
	return mergeMissingFields(parsed, defaultParams)
}

func mergeMissingFields(dst, defaults map[string]any) bool {
	modified := false
	for key, defaultValue := range defaults {
		currentValue, exists := dst[key]
		if !exists || currentValue == nil {
			dst[key] = deepCopyAny(defaultValue)
			modified = true
			continue
		}

		currentMap, currentIsMap := currentValue.(map[string]any)
		defaultMap, defaultIsMap := defaultValue.(map[string]any)
		if currentIsMap && defaultIsMap {
			if mergeMissingFields(currentMap, defaultMap) {
				modified = true
			}
		}
	}
	return modified
}

func deepCopyAny(v any) any {
	switch vv := v.(type) {
	case map[string]any:
		copied := make(map[string]any, len(vv))
		for key, value := range vv {
			copied[key] = deepCopyAny(value)
		}
		return copied
	case []any:
		copied := make([]any, len(vv))
		for i, value := range vv {
			copied[i] = deepCopyAny(value)
		}
		return copied
	default:
		return vv
	}
}

func extractBearerToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return r.Header.Get("x-api-key")
}

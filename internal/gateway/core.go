package gateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"elysiafly.com/sub2api_simple/internal/gateway/oauth"
	"elysiafly.com/sub2api_simple/internal/gateway/router"
)

//
// Gateway ?Core HTTP Server and Request Handling
//

type Gateway struct {
	cfg           *Config
	manager       *AccountManager
	sessions      *SessionStore
	authTokens    map[string]bool
	httpClient    *http.Client
	tokenManager  *oauth.Manager
	oauthSessions *oauth.SessionStore

	callbackMu  sync.Mutex
	callbackSrv *http.Server // temporary listener on port 1455 for OAuth callback
}

func newGateway(cfg *Config) *Gateway {
	authTokens := make(map[string]bool, len(cfg.AuthTokens))
	for _, t := range cfg.AuthTokens {
		authTokens[t] = true
	}

	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}

	httpClient := &http.Client{Transport: transport}

	return &Gateway{
		cfg:           cfg,
		manager:       newAccountManager(cfg.Accounts),
		sessions:      newSessionStore(cfg.StickySessionTTL.Duration),
		authTokens:    authTokens,
		httpClient:    httpClient,
		tokenManager:  oauth.NewTokenManager(httpClient, cfg.tokenStatePath()),
		oauthSessions: oauth.NewSessionStore(oauthSessionTTL),
	}
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode        int
	bytesWritten      int
	responseSnippet   bytes.Buffer
	responseTruncated bool
}

func newLoggingResponseWriter(w http.ResponseWriter) *loggingResponseWriter {
	return &loggingResponseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
	}
}

func (w *loggingResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *loggingResponseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytesWritten += n
	if n > 0 {
		appendLogSnippet(&w.responseSnippet, &w.responseTruncated, b[:n], maxLogPayloadBytes)
	}
	return n, err
}

func (w *loggingResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *loggingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("underlying ResponseWriter does not support hijacking")
	}
	return h.Hijack()
}

func (w *loggingResponseWriter) Push(target string, opts *http.PushOptions) error {
	p, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return p.Push(target, opts)
}

func (w *loggingResponseWriter) responseBodyForLog() (string, bool) {
	return string(w.responseSnippet.Bytes()), w.responseTruncated
}

type requestBodyCapture struct {
	body      io.ReadCloser
	snippet   bytes.Buffer
	truncated bool
}

func newRequestBodyCapture(body io.ReadCloser) *requestBodyCapture {
	return &requestBodyCapture{body: body}
}

func (c *requestBodyCapture) Read(p []byte) (int, error) {
	n, err := c.body.Read(p)
	if n > 0 {
		appendLogSnippet(&c.snippet, &c.truncated, p[:n], maxLogPayloadBytes)
	}
	return n, err
}

func (c *requestBodyCapture) Close() error {
	return c.body.Close()
}

func (c *requestBodyCapture) requestBodyForLog() (string, bool, bool) {
	if c == nil {
		return "", false, false
	}
	if c.snippet.Len() == 0 {
		return "", false, c.truncated
	}
	return string(c.snippet.Bytes()), true, c.truncated
}

const maxLogPayloadBytes = 8 << 10

func appendLogSnippet(dst *bytes.Buffer, truncated *bool, src []byte, limit int) {
	if len(src) == 0 || limit <= 0 {
		return
	}
	if dst.Len() >= limit {
		*truncated = true
		return
	}
	remain := limit - dst.Len()
	if len(src) > remain {
		dst.Write(src[:remain])
		*truncated = true
		return
	}
	dst.Write(src)
}

func requestCouldContainBody(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	default:
		return true
	}
}

func payloadForLog(raw string, truncated bool) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		s = "<empty>"
	}
	if truncated {
		s += " ...(truncated)"
	}
	return s
}

func redactHeaderValue(key, value string) string {
	lowerKey := strings.ToLower(key)
	switch lowerKey {
	case "authorization", "x-api-key", "proxy-authorization", "cookie", "set-cookie":
		return maskSecret(value)
	default:
		return value
	}
}

func maskSecret(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	parts := strings.SplitN(trimmed, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
		return parts[0] + " " + maskToken(parts[1])
	}
	return maskToken(trimmed)
}

func maskToken(token string) string {
	if len(token) <= 6 {
		return "***"
	}
	return token[:3] + "***" + token[len(token)-2:]
}

func sanitizeHeadersForLog(header http.Header) map[string]string {
	out := make(map[string]string, len(header))
	for key, values := range header {
		out[key] = redactHeaderValue(key, strings.Join(values, ", "))
	}
	return out
}

func asJSONForLog(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func requestURI(r *http.Request) string {
	path := "/"
	if r.URL != nil && r.URL.Path != "" {
		path = r.URL.Path
	}
	if r.URL != nil && r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	return path
}

func clientIPFromRequest(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.Index(xff, ","); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xrip := strings.TrimSpace(r.Header.Get("X-Real-IP")); xrip != "" {
		return xrip
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

// getOpenAIAccessToken returns the access token and chatgpt_account_id for an OpenAI OAuth account.
// Token source priority:
//  1. TokenManager cache/refresh (for refresh_token config or browser-login tokens)
//  2. Legacy static oauth_token from config
func (g *Gateway) getOpenAIAccessToken(ctx context.Context, account *Account) (token string, chatGPTAccountID string, err error) {
	// Check if TokenManager has a token (from refresh_token config or browser login)
	if account.UsesRefreshToken() || g.tokenManager.HasToken(account.Config.Name) {
		return g.tokenManager.GetAccessToken(ctx, oauth.Account{
			Name:             account.Config.Name,
			ClientID:         account.Config.ClientID,
			RefreshToken:     account.Config.RefreshToken,
			ProxyURL:         account.Config.Proxy,
			ChatGPTAccountID: account.Config.ChatGPTAccountID,
		})
	}
	if staticToken := account.GetStaticToken(); staticToken != "" {
		return staticToken, account.Config.ChatGPTAccountID, nil
	}
	return "", "", fmt.Errorf("account %q: no token available ?use /auth/login to authorize via browser", account.Config.Name)
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !g.requestLoggingEnabled() {
		g.dispatchHTTP(w, r)
		return
	}

	start := time.Now()
	lw := newLoggingResponseWriter(w)
	bodyCapture := newRequestBodyCapture(r.Body)
	r.Body = bodyCapture
	defer func() {
		duration := time.Since(start).Round(time.Millisecond)
		uri := requestURI(r)
		clientIP := clientIPFromRequest(r)
		log.Printf("[http] %s %s status=%d bytes=%d duration=%s ip=%s ua=%q",
			r.Method,
			uri,
			lw.statusCode,
			lw.bytesWritten,
			duration,
			clientIP,
			r.UserAgent(),
		)
		if lw.statusCode == http.StatusOK || lw.statusCode == http.StatusNotFound {
			return
		}

		reqBodyRaw, captured, reqBodyTruncated := bodyCapture.requestBodyForLog()
		reqBody := ""
		if captured {
			reqBody = payloadForLog(reqBodyRaw, reqBodyTruncated)
		} else if requestCouldContainBody(r) && r.ContentLength != 0 {
			reqBody = "<not-captured>"
		} else {
			reqBody = "<empty>"
		}

		respBodyRaw, respBodyTruncated := lw.responseBodyForLog()
		respBody := payloadForLog(respBodyRaw, respBodyTruncated)

		log.Printf("[http][detail] method=%s uri=%s status=%d ip=%s req_query=%q req_headers=%s req_body=%q resp_headers=%s resp_body=%q",
			r.Method,
			uri,
			lw.statusCode,
			clientIP,
			r.URL.RawQuery,
			asJSONForLog(sanitizeHeadersForLog(r.Header)),
			reqBody,
			asJSONForLog(sanitizeHeadersForLog(lw.Header())),
			respBody,
		)
	}()

	g.dispatchHTTP(lw, r)
}

func (g *Gateway) requestLoggingEnabled() bool {
	if g == nil || g.cfg == nil {
		return true
	}
	return g.cfg.EnableRequestLog
}

func (g *Gateway) streamDebugLoggingEnabled() bool {
	if g == nil || g.cfg == nil {
		return false
	}
	return g.cfg.EnableStreamDebugLog
}

func (g *Gateway) dispatchHTTP(w http.ResponseWriter, r *http.Request) {
	router.Dispatch(w, r, router.Handlers{
		ClaudeMessages:        g.handleClaudeMessages,
		Models:                g.handleModels,
		OpenAIResponses:       g.handleOpenAIResponses,
		OpenAIChatCompletions: g.handleOpenAIChatCompletions,
		AuthLogin:             g.handleAuthLogin,
		Health:                g.handleHealth,
		NotFound: func(w http.ResponseWriter, r *http.Request) {
			writeErrorJSON(w, http.StatusNotFound, "not_found", "Unknown endpoint: "+r.URL.Path)
		},
	})
}

//  Authentication

func (g *Gateway) authenticate(r *http.Request) bool {
	token := ""
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		token = strings.TrimPrefix(auth, "Bearer ")
	}
	if token == "" {
		token = r.Header.Get("x-api-key")
	}
	return token != "" && g.authTokens[token]
}

//  Session Hash

func computeSessionHash(platform, token, model string) string {
	h := sha256.New()
	h.Write([]byte(platform))
	h.Write([]byte("|"))
	h.Write([]byte(token))
	h.Write([]byte("|"))
	h.Write([]byte(model))
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

//
// Claude Messages Handler ?POST /v1/messages
//

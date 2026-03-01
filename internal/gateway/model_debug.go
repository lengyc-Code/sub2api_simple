package gateway

import (
	"bytes"
	"log"
	"net/http"
	"strings"
)

const maxModelDebugLogPayloadBytes = 256 << 10

func (g *Gateway) modelDebugLoggingEnabled() bool {
	if g == nil || g.cfg == nil {
		return false
	}
	return g.cfg.EnableModelDebugLog
}

func shouldLogModelDebugForRequest(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	if r.Method != http.MethodPost {
		return false
	}
	switch r.URL.Path {
	case "/v1/messages", "/v1/responses", "/responses", "/v1/chat/completions":
		return true
	default:
		return false
	}
}

func (g *Gateway) logModelClientRequest(r *http.Request, clientIP, body string) {
	if !g.modelDebugLoggingEnabled() || r == nil {
		return
	}
	log.Printf("[model][debug][client_request] method=%s uri=%s ip=%s ua=%q headers=%s body=%s",
		r.Method,
		requestURI(r),
		strings.TrimSpace(clientIP),
		r.UserAgent(),
		asJSONForLog(sanitizeHeadersForLog(r.Header)),
		body,
	)
}

func (g *Gateway) logModelClientResponse(r *http.Request, statusCode, bytesWritten int, headers http.Header, body string) {
	if !g.modelDebugLoggingEnabled() || r == nil {
		return
	}
	log.Printf("[model][debug][client_response] method=%s uri=%s status=%d bytes=%d headers=%s body=%s",
		r.Method,
		requestURI(r),
		statusCode,
		bytesWritten,
		asJSONForLog(sanitizeHeadersForLog(headers)),
		body,
	)
}

func (g *Gateway) logModelProviderRequest(platform, accountName, model string, req *http.Request, body []byte) {
	if !g.modelDebugLoggingEnabled() {
		return
	}
	payload, payloadBytes, truncated := modelPayloadForLog(body)
	method := "<unknown>"
	target := "<unknown>"
	headers := "{}"
	if req != nil {
		method = req.Method
		if req.URL != nil {
			target = req.URL.String()
		}
		headers = asJSONForLog(sanitizeHeadersForLog(req.Header))
	}

	log.Printf("[model][debug][provider_request] platform=%s account=%q model=%q method=%s target=%s headers=%s bytes=%d truncated=%t body=%s",
		platform,
		accountName,
		model,
		method,
		target,
		headers,
		payloadBytes,
		truncated,
		payload,
	)
}

func (g *Gateway) logModelProviderResponse(platform, accountName, model string, status int, headers http.Header, body []byte) {
	if !g.modelDebugLoggingEnabled() {
		return
	}
	payload, payloadBytes, truncated := modelPayloadForLog(body)
	log.Printf("[model][debug][provider_response] platform=%s account=%q model=%q status=%d headers=%s bytes=%d truncated=%t body=%s",
		platform,
		accountName,
		model,
		status,
		asJSONForLog(sanitizeHeadersForLog(headers)),
		payloadBytes,
		truncated,
		payload,
	)
}

func modelPayloadForLog(body []byte) (payload string, payloadBytes int, truncated bool) {
	if len(body) == 0 {
		return "<empty>", 0, false
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return "<empty>", 0, false
	}
	if len(trimmed) > maxModelDebugLogPayloadBytes {
		return string(trimmed[:maxModelDebugLogPayloadBytes]) + " ...(truncated)", len(trimmed), true
	}
	return string(trimmed), len(trimmed), false
}

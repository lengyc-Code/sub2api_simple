package gateway

import (
	"bytes"
	"log"
)

func (g *Gateway) modelDebugLoggingEnabled() bool {
	if g == nil || g.cfg == nil {
		return false
	}
	return g.cfg.EnableModelDebugLog
}

func (g *Gateway) logModelUpstreamRequest(platform, accountName, model string, body []byte) {
	if !g.modelDebugLoggingEnabled() {
		return
	}
	log.Printf("[model][debug][upstream] platform=%s account=%q model=%q body=%q",
		platform,
		accountName,
		model,
		modelPayloadForLog(body),
	)
}

func (g *Gateway) logModelDownstreamResponse(platform, accountName, model string, status int, body []byte) {
	if !g.modelDebugLoggingEnabled() {
		return
	}
	log.Printf("[model][debug][downstream] platform=%s account=%q model=%q status=%d body=%q",
		platform,
		accountName,
		model,
		status,
		modelPayloadForLog(body),
	)
}

func modelPayloadForLog(body []byte) string {
	if len(body) == 0 {
		return "<empty>"
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return "<empty>"
	}
	if len(trimmed) > maxLogPayloadBytes {
		return string(trimmed[:maxLogPayloadBytes]) + " ...(truncated)"
	}
	return string(trimmed)
}

package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"elysiafly.com/sub2api_simple/internal/gateway/forwarder"
)

type (
	bodyPrepFunc func(account *Account, body []byte, model string) []byte
	reqBuildFunc func(ctx context.Context, account *Account, body []byte, model string, stream bool) (*http.Request, error)
	errWriteFunc func(w http.ResponseWriter, status int, errType, message string)
)

// forwardWithFailover is the unified failover loop for both Claude and OpenAI platforms.
// It selects accounts, builds upstream requests, and handles failover on transient errors.
func (g *Gateway) forwardWithFailover(
	w http.ResponseWriter, r *http.Request,
	platform, model string, stream bool, body []byte, sessionKey string,
	prepareBody bodyPrepFunc,
	buildReq reqBuildFunc,
	writeErr errWriteFunc,
) {
	stickyAccount := g.sessions.Get(sessionKey)
	excludeIDs := make(map[*Account]bool)
	maxSwitches := g.cfg.MaxAccountSwitches
	if maxSwitches <= 0 {
		maxSwitches = 5
	}

	for attempt := 0; attempt <= maxSwitches; attempt++ {
		account, err := g.manager.SelectAccount(platform, model, stickyAccount, excludeIDs)
		if err != nil {
			if attempt == 0 {
				writeErr(w, http.StatusServiceUnavailable, "api_error", "No available accounts: "+err.Error())
			} else {
				writeErr(w, http.StatusBadGateway, "api_error", "All accounts failed")
			}
			return
		}

		reqBody := prepareBody(account, body, model)
		g.logModelUpstreamRequest(platform, account.Config.Name, model, reqBody)
		upstreamReq, err := buildReq(r.Context(), account, reqBody, model, stream)
		if err != nil {
			account.ReleaseSlot()
			writeErr(w, http.StatusInternalServerError, "api_error", "Failed to build upstream request")
			return
		}

		resp, err := g.getHTTPClient(account).Do(upstreamReq)
		if err != nil {
			account.ReleaseSlot()
			account.RecordError()
			log.Printf("[%s] upstream error for account %q: %v", platform, account.Config.Name, err)
			excludeIDs[account] = true
			stickyAccount = nil
			continue
		}

		if forwarder.ShouldFailover(resp.StatusCode) {
			forwarder.DrainAndClose(resp.Body)
			account.ReleaseSlot()
			account.RecordError()

			if resp.StatusCode == http.StatusTooManyRequests {
				retryAfter := forwarder.ParseRetryAfter(resp.Header.Get("Retry-After"))
				account.SetRateLimited(time.Now().Add(retryAfter))
				log.Printf("[%s] account %q rate-limited for %v", platform, account.Config.Name, retryAfter)
			} else {
				log.Printf("[%s] upstream %d for account %q, failing over", platform, resp.StatusCode, account.Config.Name)
			}

			excludeIDs[account] = true
			stickyAccount = nil
			g.sessions.Remove(sessionKey)
			continue
		}

		// Success ?bind session and forward response
		g.sessions.Set(sessionKey, account)
		account.RecordSuccess()

		shouldStreamUpstream := stream || account.IsOpenAIOAuth()
		if forwarder.ShouldHandleAsStreamingResponse(shouldStreamUpstream, resp.Header.Get("Content-Type")) {
			if stream {
				g.handleStreamingResponse(w, resp, account, model)
				return
			}
			if account.IsOpenAI() {
				g.handleOpenAISSEAsNonStreamingResponse(w, resp, account, model)
				return
			}
			g.handleSSEAsPlainNonStreamingResponse(w, resp, account, model)
			return
		} else {
			if g.streamDebugLoggingEnabled() && shouldStreamUpstream {
				log.Printf("[stream][debug] account=%q model=%q upstream is not SSE, content_type=%q",
					account.Config.Name, model, resp.Header.Get("Content-Type"))
			}
			g.handleNonStreamingResponse(w, resp, account, model)
		}
		return
	}

	writeErr(w, http.StatusBadGateway, "api_error", "All accounts exhausted after failover")
}

//
// Common Response Handling
//

// handleStreamingResponse forwards an SSE stream from upstream to the client.
func (g *Gateway) handleStreamingResponse(w http.ResponseWriter, resp *http.Response, account *Account, model string) {
	defer resp.Body.Close()
	defer account.ReleaseSlot()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErrorJSON(w, http.StatusInternalServerError, "api_error", "Streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if reqID := resp.Header.Get("x-request-id"); reqID != "" {
		w.Header().Set("x-request-id", reqID)
	}
	w.WriteHeader(http.StatusOK)

	type scanEvent struct {
		line string
		err  error
	}

	events := make(chan scanEvent, 16)
	done := make(chan struct{})
	defer close(done)

	go func() {
		defer close(events)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
		for scanner.Scan() {
			select {
			case events <- scanEvent{line: scanner.Text()}:
			case <-done:
				return
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case events <- scanEvent{err: err}:
			case <-done:
			}
		}
	}()

	streamTimeout := g.streamReadTimeout()
	timer := time.NewTimer(streamTimeout)
	defer timer.Stop()

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.err != nil {
				log.Printf("[stream] read error for %q: %v", account.Config.Name, ev.err)
				return
			}

			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(streamTimeout)

			if g.streamDebugLoggingEnabled() && strings.TrimSpace(ev.line) != "" {
				log.Printf("[stream][debug] account=%q model=%q line=%s",
					account.Config.Name,
					model,
					ev.line,
				)
			}
			if g.modelDebugLoggingEnabled() && strings.TrimSpace(ev.line) != "" {
				g.logModelDownstreamResponse(account.Config.Platform, account.Config.Name, model, http.StatusOK, []byte(ev.line))
			}

			_, writeErr := fmt.Fprintf(w, "%s\n", ev.line)
			if writeErr != nil {
				log.Printf("[stream] client disconnected for %q", account.Config.Name)
				return
			}
			flusher.Flush()

		case <-timer.C:
			log.Printf("[stream] upstream data timeout for %q", account.Config.Name)
			fmt.Fprintf(w, "event: error\ndata: {\"error\":\"stream_timeout\"}\n\n")
			flusher.Flush()
			return
		}
	}
}

// handleOpenAISSEAsNonStreamingResponse aggregates upstream OpenAI SSE events
// into a single JSON response for clients that requested stream=false.
func (g *Gateway) handleOpenAISSEAsNonStreamingResponse(w http.ResponseWriter, resp *http.Response, account *Account, model string) {
	defer resp.Body.Close()
	defer account.ReleaseSlot()

	aggregated, err := g.readOpenAISSEAsJSON(resp.Body, account, model)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "Failed to aggregate upstream stream response")
		return
	}

	body, err := json.Marshal(aggregated)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "Failed to encode aggregated response")
		return
	}
	g.logModelDownstreamResponse(platformOpenAI, account.Config.Name, model, http.StatusOK, body)

	if reqID := resp.Header.Get("x-request-id"); reqID != "" {
		w.Header().Set("x-request-id", reqID)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// handleSSEAsPlainNonStreamingResponse is a best-effort fallback for non-OpenAI
// SSE upstream responses when the client requested stream=false.
func (g *Gateway) handleSSEAsPlainNonStreamingResponse(w http.ResponseWriter, resp *http.Response, account *Account, model string) {
	defer resp.Body.Close()
	defer account.ReleaseSlot()

	text, err := g.readSSEText(resp.Body, account, model)
	if err != nil {
		writeErrorJSON(w, http.StatusBadGateway, "api_error", "Failed to aggregate upstream stream response")
		return
	}

	if reqID := resp.Header.Get("x-request-id"); reqID != "" {
		w.Header().Set("x-request-id", reqID)
	}
	payload := map[string]any{
		"type": "sse_aggregated_response",
		"text": text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		writeErrorJSON(w, http.StatusBadGateway, "api_error", "Failed to encode aggregated response")
		return
	}
	g.logModelDownstreamResponse(account.Config.Platform, account.Config.Name, model, http.StatusOK, body)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (g *Gateway) readOpenAISSEAsJSON(body io.Reader, account *Account, model string) (map[string]any, error) {
	acc := forwarder.NewOpenAISSEAccumulator(model)

	type scanEvent struct {
		line string
		err  error
	}

	events := make(chan scanEvent, 16)
	done := make(chan struct{})
	defer close(done)

	go func() {
		defer close(events)
		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
		for scanner.Scan() {
			select {
			case events <- scanEvent{line: scanner.Text()}:
			case <-done:
				return
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case events <- scanEvent{err: err}:
			case <-done:
			}
		}
	}()

	timer := time.NewTimer(g.streamReadTimeout())
	defer timer.Stop()

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return acc.Build(), nil
			}
			if ev.err != nil {
				return nil, ev.err
			}

			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(g.streamReadTimeout())

			if err := acc.ConsumeLine(ev.line); err != nil {
				return nil, err
			}
			if g.streamDebugLoggingEnabled() && strings.TrimSpace(ev.line) != "" {
				log.Printf("[stream][debug] account=%q model=%q line=%s", account.Config.Name, model, ev.line)
			}
		case <-timer.C:
			return nil, fmt.Errorf("stream timeout")
		}
	}
}

func (g *Gateway) readSSEText(body io.Reader, account *Account, model string) (string, error) {
	type scanEvent struct {
		line string
		err  error
	}
	events := make(chan scanEvent, 16)
	done := make(chan struct{})
	defer close(done)

	go func() {
		defer close(events)
		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
		for scanner.Scan() {
			select {
			case events <- scanEvent{line: scanner.Text()}:
			case <-done:
				return
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case events <- scanEvent{err: err}:
			case <-done:
			}
		}
	}()

	timer := time.NewTimer(g.streamReadTimeout())
	defer timer.Stop()
	var textBuilder strings.Builder

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return textBuilder.String(), nil
			}
			if ev.err != nil {
				return "", ev.err
			}

			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(g.streamReadTimeout())

			line := strings.TrimSpace(ev.line)
			if line == "" || !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" || data == "[DONE]" {
				continue
			}
			if textBuilder.Len() > 0 {
				textBuilder.WriteByte('\n')
			}
			textBuilder.WriteString(data)
			if g.streamDebugLoggingEnabled() {
				log.Printf("[stream][debug] account=%q model=%q line=%s", account.Config.Name, model, line)
			}
		case <-timer.C:
			return "", fmt.Errorf("stream timeout")
		}
	}
}

func (g *Gateway) streamReadTimeout() time.Duration {
	if g == nil || g.cfg == nil {
		return 5 * time.Minute
	}
	streamTimeout := g.cfg.StreamReadTimeout.Duration
	if streamTimeout <= 0 {
		return 5 * time.Minute
	}
	return streamTimeout
}

// handleNonStreamingResponse reads the full upstream response and writes it to the client.
func (g *Gateway) handleNonStreamingResponse(w http.ResponseWriter, resp *http.Response, account *Account, model string) {
	defer resp.Body.Close()
	defer account.ReleaseSlot()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		writeErrorJSON(w, http.StatusBadGateway, "api_error", "Failed to read upstream response")
		return
	}
	g.logModelDownstreamResponse(account.Config.Platform, account.Config.Name, model, resp.StatusCode, body)

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	if reqID := resp.Header.Get("x-request-id"); reqID != "" {
		w.Header().Set("x-request-id", reqID)
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

//
// GET /v1/models ?Combined Model List
//

package gateway

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"elysiafly.com/sub2api_simple/internal/gateway/forwarder"
	openaihandler "elysiafly.com/sub2api_simple/internal/gateway/openai"
)

func (g *Gateway) handleOpenAIChatCompletions(w http.ResponseWriter, r *http.Request) {
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

	responsesBody, model, stream, err := openaihandler.ConvertChatCompletionsRequest(body, codexModelMap)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	authToken := extractBearerToken(r)
	sessionKey := computeSessionHash(platformOpenAI, authToken, model)

	g.forwardOpenAIChatCompletionsWithFailover(w, r, model, stream, responsesBody, sessionKey)
}

func (g *Gateway) forwardOpenAIChatCompletionsWithFailover(
	w http.ResponseWriter,
	r *http.Request,
	model string,
	stream bool,
	body []byte,
	sessionKey string,
) {
	stickyAccount := g.sessions.Get(sessionKey)
	excludeIDs := make(map[*Account]bool)
	maxSwitches := g.cfg.MaxAccountSwitches
	if maxSwitches <= 0 {
		maxSwitches = 5
	}

	for attempt := 0; attempt <= maxSwitches; attempt++ {
		account, err := g.manager.SelectAccount(platformOpenAI, model, stickyAccount, excludeIDs)
		if err != nil {
			if attempt == 0 {
				writeOpenAIError(w, http.StatusServiceUnavailable, "api_error", "No available accounts: "+err.Error())
			} else {
				writeOpenAIError(w, http.StatusBadGateway, "api_error", "All accounts failed")
			}
			return
		}

		reqBody := g.prepareOpenAIBody(account, body, model)
		upstreamReq, err := g.buildOpenAIUpstreamRequest(r.Context(), account, reqBody, model, stream)
		if err != nil {
			account.ReleaseSlot()
			writeOpenAIError(w, http.StatusInternalServerError, "api_error", "Failed to build upstream request")
			return
		}

		resp, err := g.getHTTPClient(account).Do(upstreamReq)
		if err != nil {
			account.ReleaseSlot()
			account.RecordError()
			log.Printf("[openai-chat] upstream error for account %q: %v", account.Config.Name, err)
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
				log.Printf("[openai-chat] account %q rate-limited for %v", account.Config.Name, retryAfter)
			} else {
				log.Printf("[openai-chat] upstream %d for account %q, failing over", resp.StatusCode, account.Config.Name)
			}

			excludeIDs[account] = true
			stickyAccount = nil
			g.sessions.Remove(sessionKey)
			continue
		}

		g.sessions.Set(sessionKey, account)
		account.RecordSuccess()

		if resp.StatusCode != http.StatusOK {
			g.handleNonStreamingResponse(w, resp, account)
			return
		}

		shouldStreamUpstream := stream || account.IsOpenAIOAuth()
		if forwarder.ShouldHandleAsStreamingResponse(shouldStreamUpstream, resp.Header.Get("Content-Type")) {
			if stream {
				g.handleOpenAIChatCompletionsStreamingResponse(w, resp, account, model)
				return
			}
			g.handleOpenAIChatCompletionsSSEAsNonStreamingResponse(w, resp, account, model)
			return
		}

		if stream {
			g.handleOpenAIChatCompletionsJSONAsStreamingResponse(w, resp, account, model)
			return
		}
		g.handleOpenAIChatCompletionsJSONAsNonStreamingResponse(w, resp, account, model)
		return
	}

	writeOpenAIError(w, http.StatusBadGateway, "api_error", "All accounts exhausted after failover")
}

func (g *Gateway) handleOpenAIChatCompletionsSSEAsNonStreamingResponse(
	w http.ResponseWriter,
	resp *http.Response,
	account *Account,
	model string,
) {
	defer resp.Body.Close()
	defer account.ReleaseSlot()

	aggregated, err := g.readOpenAISSEAsJSON(resp.Body, account, model)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "Failed to aggregate upstream stream response")
		return
	}

	converted := openaihandler.ChatCompletionFromResponses(aggregated, model)

	if reqID := resp.Header.Get("x-request-id"); reqID != "" {
		w.Header().Set("x-request-id", reqID)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(converted)
}

func (g *Gateway) handleOpenAIChatCompletionsJSONAsNonStreamingResponse(
	w http.ResponseWriter,
	resp *http.Response,
	account *Account,
	model string,
) {
	defer resp.Body.Close()
	defer account.ReleaseSlot()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "Failed to read upstream response")
		return
	}

	var upstream map[string]any
	if err := json.Unmarshal(body, &upstream); err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "Failed to parse upstream response")
		return
	}

	converted := openaihandler.ChatCompletionFromResponses(upstream, model)

	if reqID := resp.Header.Get("x-request-id"); reqID != "" {
		w.Header().Set("x-request-id", reqID)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(converted)
}

func (g *Gateway) handleOpenAIChatCompletionsJSONAsStreamingResponse(
	w http.ResponseWriter,
	resp *http.Response,
	account *Account,
	model string,
) {
	defer resp.Body.Close()
	defer account.ReleaseSlot()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "Streaming not supported")
		return
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "Failed to read upstream response")
		return
	}

	var upstream map[string]any
	if err := json.Unmarshal(body, &upstream); err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "Failed to parse upstream response")
		return
	}

	chatResp := openaihandler.ChatCompletionFromResponses(upstream, model)
	chatID, _ := chatResp["id"].(string)
	chatModel, _ := chatResp["model"].(string)
	created, _ := chatResp["created"].(int64)
	if created == 0 {
		created = time.Now().Unix()
	}

	content := ""
	toolCalls := extractToolCallsFromChatCompletion(chatResp)
	if choices, ok := chatResp["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if message, ok := choice["message"].(map[string]any); ok {
				content, _ = message["content"].(string)
			}
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if reqID := resp.Header.Get("x-request-id"); reqID != "" {
		w.Header().Set("x-request-id", reqID)
	}
	w.WriteHeader(http.StatusOK)

	if err := writeChatCompletionSSEChunk(w, openaihandler.ChatCompletionChunk(
		chatID, chatModel, created, map[string]any{"role": "assistant"}, nil),
	); err != nil {
		return
	}
	flusher.Flush()

	if content != "" {
		if err := writeChatCompletionSSEChunk(w, openaihandler.ChatCompletionChunk(
			chatID, chatModel, created, map[string]any{"content": content}, nil),
		); err != nil {
			return
		}
		flusher.Flush()
	}

	finishReason := "stop"
	if len(toolCalls) > 0 {
		for i, tc := range toolCalls {
			delta := map[string]any{
				"tool_calls": []any{
					map[string]any{
						"index": i,
						"id":    tc["id"],
						"type":  "function",
						"function": map[string]any{
							"name":      tc["name"],
							"arguments": tc["arguments"],
						},
					},
				},
			}
			if err := writeChatCompletionSSEChunk(w, openaihandler.ChatCompletionChunk(
				chatID, chatModel, created, delta, nil),
			); err != nil {
				return
			}
			flusher.Flush()
		}
		finishReason = "tool_calls"
	}

	_ = writeChatCompletionSSEChunk(w, openaihandler.ChatCompletionChunk(
		chatID, chatModel, created, map[string]any{}, finishReason),
	)
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func (g *Gateway) handleOpenAIChatCompletionsStreamingResponse(
	w http.ResponseWriter,
	resp *http.Response,
	account *Account,
	model string,
) {
	defer resp.Body.Close()
	defer account.ReleaseSlot()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "Streaming not supported")
		return
	}

	chatID := fmt.Sprintf("chatcmpl_%d", time.Now().UnixNano())
	chatModel := model
	created := time.Now().Unix()
	roleSent := false
	finished := false

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

	emitFinish := func(reason string) {
		if finished {
			return
		}
		if reason == "" {
			reason = "stop"
		}
		finished = true
		_ = writeChatCompletionSSEChunk(w, openaihandler.ChatCompletionChunk(
			chatID, chatModel, created, map[string]any{}, reason),
		)
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				emitFinish("stop")
				return
			}
			if ev.err != nil {
				log.Printf("[openai-chat] stream read error for %q: %v", account.Config.Name, ev.err)
				emitFinish("stop")
				return
			}

			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(streamTimeout)

			line := strings.TrimSpace(ev.line)
			if line == "" || !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" {
				continue
			}
			if data == "[DONE]" {
				emitFinish("stop")
				return
			}

			var evt map[string]any
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				continue
			}

			if respObj, ok := evt["response"].(map[string]any); ok {
				if id, _ := respObj["id"].(string); id != "" {
					chatID = id
				}
				if m, _ := respObj["model"].(string); m != "" {
					chatModel = m
				}
			}

			evtType, _ := evt["type"].(string)
			switch evtType {
			case "response.output_text.delta":
				delta, _ := evt["delta"].(string)
				if delta == "" {
					continue
				}
				if !roleSent {
					if err := writeChatCompletionSSEChunk(w, openaihandler.ChatCompletionChunk(
						chatID, chatModel, created, map[string]any{"role": "assistant"}, nil),
					); err != nil {
						return
					}
					flusher.Flush()
					roleSent = true
				}

				if err := writeChatCompletionSSEChunk(w, openaihandler.ChatCompletionChunk(
					chatID, chatModel, created, map[string]any{"content": delta}, nil),
				); err != nil {
					return
				}
				flusher.Flush()
			case "response.completed":
				respObj, _ := evt["response"].(map[string]any)
				if respObj == nil {
					emitFinish("stop")
					return
				}
				chatResp := openaihandler.ChatCompletionFromResponses(respObj, chatModel)
				toolCalls := extractToolCallsFromChatCompletion(chatResp)
				if len(toolCalls) > 0 {
					for i, tc := range toolCalls {
						delta := map[string]any{
							"tool_calls": []any{
								map[string]any{
									"index": i,
									"id":    tc["id"],
									"type":  "function",
									"function": map[string]any{
										"name":      tc["name"],
										"arguments": tc["arguments"],
									},
								},
							},
						}
						if err := writeChatCompletionSSEChunk(w, openaihandler.ChatCompletionChunk(
							chatID, chatModel, created, delta, nil),
						); err != nil {
							return
						}
						flusher.Flush()
					}
					emitFinish("tool_calls")
					return
				}
				emitFinish("stop")
				return
			case "response.output_text.done":
				emitFinish("stop")
				return
			case "error":
				emitFinish("stop")
				return
			}
		case <-timer.C:
			log.Printf("[openai-chat] upstream data timeout for %q", account.Config.Name)
			emitFinish("stop")
			return
		}
	}
}

func writeChatCompletionSSEChunk(w http.ResponseWriter, chunk map[string]any) error {
	b, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", b)
	return err
}

func extractToolCallsFromChatCompletion(chatResp map[string]any) []map[string]string {
	choices, _ := chatResp["choices"].([]any)
	if len(choices) == 0 {
		return nil
	}
	choice, _ := choices[0].(map[string]any)
	if choice == nil {
		return nil
	}
	message, _ := choice["message"].(map[string]any)
	if message == nil {
		return nil
	}
	rawToolCalls, _ := message["tool_calls"].([]any)
	if len(rawToolCalls) == 0 {
		return nil
	}

	out := make([]map[string]string, 0, len(rawToolCalls))
	for _, item := range rawToolCalls {
		tc, _ := item.(map[string]any)
		if tc == nil {
			continue
		}
		id, _ := tc["id"].(string)
		fn, _ := tc["function"].(map[string]any)
		name := ""
		args := ""
		if fn != nil {
			name, _ = fn["name"].(string)
			args, _ = fn["arguments"].(string)
		}
		if strings.TrimSpace(name) == "" {
			continue
		}
		out = append(out, map[string]string{
			"id":        id,
			"name":      name,
			"arguments": args,
		})
	}
	return out
}

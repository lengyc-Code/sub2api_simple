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
		g.logModelProviderRequest(platformOpenAI, account.Config.Name, model, upstreamReq, reqBody)

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
			failBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxModelDebugLogPayloadBytes))
			g.logModelProviderResponse(platformOpenAI, account.Config.Name, model, resp.StatusCode, resp.Header, failBody)
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
			g.handleNonStreamingResponse(w, resp, account, model)
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
	upstreamBody, err := json.Marshal(aggregated)
	if err == nil {
		g.logModelProviderResponse(platformOpenAI, account.Config.Name, model, resp.StatusCode, resp.Header, upstreamBody)
	}

	converted := openaihandler.ChatCompletionFromResponses(aggregated, model)
	convertedBody, err := json.Marshal(converted)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "Failed to encode converted response")
		return
	}

	if reqID := resp.Header.Get("x-request-id"); reqID != "" {
		w.Header().Set("x-request-id", reqID)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(convertedBody)
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
	g.logModelProviderResponse(platformOpenAI, account.Config.Name, model, resp.StatusCode, resp.Header, body)

	var upstream map[string]any
	if err := json.Unmarshal(body, &upstream); err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "Failed to parse upstream response")
		return
	}

	converted := openaihandler.ChatCompletionFromResponses(upstream, model)
	convertedBody, err := json.Marshal(converted)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "Failed to encode converted response")
		return
	}

	if reqID := resp.Header.Get("x-request-id"); reqID != "" {
		w.Header().Set("x-request-id", reqID)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(convertedBody)
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
	g.logModelProviderResponse(platformOpenAI, account.Config.Name, model, resp.StatusCode, resp.Header, body)

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

	if err := g.writeChatCompletionSSEChunk(w, openaihandler.ChatCompletionChunk(
		chatID, chatModel, created, map[string]any{"role": "assistant"}, nil),
		account, model,
	); err != nil {
		return
	}
	flusher.Flush()

	if content != "" {
		if err := g.writeChatCompletionSSEChunk(w, openaihandler.ChatCompletionChunk(
			chatID, chatModel, created, map[string]any{"content": content}, nil),
			account, model,
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
			if err := g.writeChatCompletionSSEChunk(w, openaihandler.ChatCompletionChunk(
				chatID, chatModel, created, delta, nil),
				account, model,
			); err != nil {
				return
			}
			flusher.Flush()
		}
		finishReason = "tool_calls"
	}

	_ = g.writeChatCompletionSSEChunk(w, openaihandler.ChatCompletionChunk(
		chatID, chatModel, created, map[string]any{}, finishReason), account, model)
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
	g.logModelProviderResponse(platformOpenAI, account.Config.Name, model, resp.StatusCode, resp.Header, nil)

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
	toolCallAccumulator := newChatCompletionToolCallAccumulator()

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
		_ = g.writeChatCompletionSSEChunk(w, openaihandler.ChatCompletionChunk(
			chatID, chatModel, created, map[string]any{}, reason), account, model)
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}

	emitToolCalls := func(toolCalls []map[string]string) bool {
		if len(toolCalls) == 0 {
			return false
		}

		if !roleSent {
			if err := g.writeChatCompletionSSEChunk(w, openaihandler.ChatCompletionChunk(
				chatID, chatModel, created, map[string]any{"role": "assistant"}, nil),
				account, model,
			); err != nil {
				return false
			}
			flusher.Flush()
			roleSent = true
		}

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
			if err := g.writeChatCompletionSSEChunk(w, openaihandler.ChatCompletionChunk(
				chatID, chatModel, created, delta, nil),
				account, model,
			); err != nil {
				return false
			}
			flusher.Flush()
		}
		return true
	}

	finishWithPendingToolCalls := func(defaultReason string) {
		if finished {
			return
		}
		if emitToolCalls(toolCallAccumulator.ToolCalls()) {
			emitFinish("tool_calls")
			return
		}
		emitFinish(defaultReason)
	}

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				finishWithPendingToolCalls("stop")
				return
			}
			if ev.err != nil {
				log.Printf("[openai-chat] stream read error for %q: %v", account.Config.Name, ev.err)
				finishWithPendingToolCalls("stop")
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
			if g.modelDebugLoggingEnabled() && line != "" {
				g.logModelProviderResponse(platformOpenAI, account.Config.Name, model, resp.StatusCode, nil, []byte(line))
			}
			if line == "" || !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" {
				continue
			}
			if data == "[DONE]" {
				finishWithPendingToolCalls("stop")
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
					if err := g.writeChatCompletionSSEChunk(w, openaihandler.ChatCompletionChunk(
						chatID, chatModel, created, map[string]any{"role": "assistant"}, nil),
						account, model,
					); err != nil {
						return
					}
					flusher.Flush()
					roleSent = true
				}

				if err := g.writeChatCompletionSSEChunk(w, openaihandler.ChatCompletionChunk(
					chatID, chatModel, created, map[string]any{"content": delta}, nil),
					account, model,
				); err != nil {
					return
				}
				flusher.Flush()
			case "response.output_item.added",
				"response.output_item.done",
				"response.function_call_arguments.delta",
				"response.function_call_arguments.done":
				toolCallAccumulator.Consume(evtType, evt)
			case "response.completed":
				respObj, _ := evt["response"].(map[string]any)
				if respObj == nil {
					finishWithPendingToolCalls("stop")
					return
				}
				chatResp := openaihandler.ChatCompletionFromResponses(respObj, chatModel)
				toolCalls := extractToolCallsFromChatCompletion(chatResp)
				if len(toolCalls) == 0 {
					toolCalls = toolCallAccumulator.ToolCalls()
				}
				if len(toolCalls) > 0 {
					if !emitToolCalls(toolCalls) {
						return
					}
					emitFinish("tool_calls")
					return
				}
				emitFinish("stop")
				return
			case "response.output_text.done":
				// output_text.done only means the current text block ended.
				// Tool calls may still arrive in subsequent events (e.g. response.completed).
				continue
			case "error":
				finishWithPendingToolCalls("stop")
				return
			}
		case <-timer.C:
			log.Printf("[openai-chat] upstream data timeout for %q", account.Config.Name)
			finishWithPendingToolCalls("stop")
			return
		}
	}
}

func (g *Gateway) writeChatCompletionSSEChunk(w http.ResponseWriter, chunk map[string]any, account *Account, model string) error {
	_ = account
	_ = model
	b, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", b)
	return err
}

type chatCompletionToolCall struct {
	id        string
	name      string
	arguments string
}

type chatCompletionToolCallAccumulator struct {
	calls         []chatCompletionToolCall
	byID          map[string]int
	byOutputIndex map[int]int
}

func newChatCompletionToolCallAccumulator() *chatCompletionToolCallAccumulator {
	return &chatCompletionToolCallAccumulator{
		calls:         make([]chatCompletionToolCall, 0),
		byID:          make(map[string]int),
		byOutputIndex: make(map[int]int),
	}
}

func (a *chatCompletionToolCallAccumulator) Consume(evtType string, evt map[string]any) {
	if a == nil || evt == nil {
		return
	}
	switch evtType {
	case "response.output_item.added", "response.output_item.done":
		a.consumeOutputItem(evt)
	case "response.function_call_arguments.delta":
		a.consumeFunctionCallArgumentsDelta(evt)
	case "response.function_call_arguments.done":
		a.consumeFunctionCallArgumentsDone(evt)
	}
}

func (a *chatCompletionToolCallAccumulator) ToolCalls() []map[string]string {
	if a == nil || len(a.calls) == 0 {
		return nil
	}

	out := make([]map[string]string, 0, len(a.calls))
	for i, call := range a.calls {
		if strings.TrimSpace(call.name) == "" {
			continue
		}

		callID := strings.TrimSpace(call.id)
		if callID == "" {
			callID = fmt.Sprintf("call_%d", i)
		}

		out = append(out, map[string]string{
			"id":        callID,
			"name":      call.name,
			"arguments": call.arguments,
		})
	}
	return out
}

func (a *chatCompletionToolCallAccumulator) consumeOutputItem(evt map[string]any) {
	item, _ := evt["item"].(map[string]any)
	if item == nil {
		return
	}

	itemType, _ := item["type"].(string)
	if itemType != "function_call" && itemType != "tool_call" {
		return
	}

	callID := firstNonEmptyString(item["call_id"], item["id"])
	outputIndex := intFromAnyOrDefault(evt["output_index"], -1)
	idx := a.ensureCall(callID, outputIndex)

	if name, _ := item["name"].(string); strings.TrimSpace(name) != "" {
		a.calls[idx].name = name
	}
	if args, _ := item["arguments"].(string); args != "" {
		a.calls[idx].arguments = args
	}
}

func (a *chatCompletionToolCallAccumulator) consumeFunctionCallArgumentsDelta(evt map[string]any) {
	callID := firstNonEmptyString(evt["call_id"], evt["item_id"], evt["id"])
	outputIndex := intFromAnyOrDefault(evt["output_index"], -1)
	idx := a.ensureCall(callID, outputIndex)

	if name, _ := evt["name"].(string); strings.TrimSpace(name) != "" {
		a.calls[idx].name = name
	}
	if delta, _ := evt["delta"].(string); delta != "" {
		a.calls[idx].arguments += delta
	}
}

func (a *chatCompletionToolCallAccumulator) consumeFunctionCallArgumentsDone(evt map[string]any) {
	callID := firstNonEmptyString(evt["call_id"], evt["item_id"], evt["id"])
	outputIndex := intFromAnyOrDefault(evt["output_index"], -1)
	idx := a.ensureCall(callID, outputIndex)

	if name, _ := evt["name"].(string); strings.TrimSpace(name) != "" {
		a.calls[idx].name = name
	}
	if args, _ := evt["arguments"].(string); args != "" {
		a.calls[idx].arguments = args
	}
}

func (a *chatCompletionToolCallAccumulator) ensureCall(callID string, outputIndex int) int {
	callID = strings.TrimSpace(callID)
	if callID != "" {
		if idx, ok := a.byID[callID]; ok {
			if outputIndex >= 0 {
				a.byOutputIndex[outputIndex] = idx
			}
			return idx
		}
	}
	if outputIndex >= 0 {
		if idx, ok := a.byOutputIndex[outputIndex]; ok {
			if callID != "" {
				a.byID[callID] = idx
				if strings.TrimSpace(a.calls[idx].id) == "" {
					a.calls[idx].id = callID
				}
			}
			return idx
		}
	}

	if callID == "" {
		if outputIndex >= 0 {
			callID = fmt.Sprintf("call_%d", outputIndex)
		} else {
			callID = fmt.Sprintf("call_%d", len(a.calls))
		}
	}

	idx := len(a.calls)
	a.calls = append(a.calls, chatCompletionToolCall{id: callID})
	a.byID[callID] = idx
	if outputIndex >= 0 {
		a.byOutputIndex[outputIndex] = idx
	}
	return idx
}

func firstNonEmptyString(values ...any) string {
	for _, v := range values {
		s, _ := v.(string)
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func intFromAnyOrDefault(v any, defaultValue int) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		x, err := n.Int64()
		if err == nil {
			return int(x)
		}
	}
	return defaultValue
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

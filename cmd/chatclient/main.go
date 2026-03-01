package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL = "http://127.0.0.1:8080"
	defaultTimeout = 120 * time.Second
)

type modelListResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

type chatClient struct {
	baseURL      string
	token        string
	model        string
	instructions string
	httpClient   *http.Client
}

func main() {
	var (
		baseURL      = flag.String("base-url", defaultBaseURL, "Sub2API gateway base URL")
		token        = flag.String("token", "", "Sub2API auth token")
		model        = flag.String("model", "", "Model ID (empty to choose interactively)")
		instructions = flag.String("instructions", "", "Optional instructions for OpenAI responses API")
	)
	flag.Parse()

	authToken := strings.TrimSpace(*token)
	if authToken == "" {
		authToken = strings.TrimSpace(os.Getenv("SUB2API_TOKEN"))
	}
	if authToken == "" {
		fmt.Fprintln(os.Stderr, "missing token: provide -token or SUB2API_TOKEN")
		os.Exit(1)
	}

	client := &chatClient{
		baseURL:      strings.TrimRight(strings.TrimSpace(*baseURL), "/"),
		token:        authToken,
		model:        strings.TrimSpace(*model),
		instructions: strings.TrimSpace(*instructions),
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}

	if err := client.run(); err != nil {
		fmt.Fprintf(os.Stderr, "chat client failed: %v\n", err)
		os.Exit(1)
	}
}

func (c *chatClient) run() error {
	models, err := c.fetchModels()
	if err != nil {
		return err
	}
	if len(models) == 0 {
		return fmt.Errorf("no models available from %s/v1/models", c.baseURL)
	}

	if c.model == "" {
		chosen, chooseErr := chooseModel(models, os.Stdin, os.Stdout)
		if chooseErr != nil {
			return chooseErr
		}
		c.model = chosen
	}

	fmt.Fprintf(os.Stdout, "Model: %s\n", c.model)
	fmt.Fprintln(os.Stdout, "Type your message. Use /exit to quit.")

	platform := platformFromModel(c.model)
	history := make([]map[string]string, 0, 16)
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Fprint(os.Stdout, "\nYou> ")
		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				fmt.Fprintln(os.Stdout, "\nbye")
				return nil
			}
			return fmt.Errorf("read input: %w", err)
		}

		userText := strings.TrimSpace(line)
		if userText == "" {
			continue
		}
		if userText == "/exit" || userText == "/quit" {
			fmt.Fprintln(os.Stdout, "bye")
			return nil
		}

		history = append(history, map[string]string{
			"role":    "user",
			"content": userText,
		})

		fmt.Fprint(os.Stdout, "AI > ")
		reply, streamErr := c.streamChat(platform, history)
		if streamErr != nil {
			fmt.Fprintf(os.Stdout, "\n[error] %v\n", streamErr)
			continue
		}

		reply = strings.TrimSpace(reply)
		if reply != "" {
			history = append(history, map[string]string{
				"role":    "assistant",
				"content": reply,
			})
		}
	}
}

func (c *chatClient) fetchModels() ([]string, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, fmt.Errorf("fetch models failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload modelListResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}

	ids := make([]string, 0, len(payload.Data))
	for _, m := range payload.Data {
		if strings.TrimSpace(m.ID) != "" {
			ids = append(ids, m.ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func chooseModel(models []string, in io.Reader, out io.Writer) (string, error) {
	fmt.Fprintln(out, "Available models:")
	for i, m := range models {
		fmt.Fprintf(out, "  %d) %s\n", i+1, m)
	}

	reader := bufio.NewReader(in)
	for {
		fmt.Fprintf(out, "Select model [1-%d]: ", len(models))
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("read model selection: %w", err)
		}

		choiceText := strings.TrimSpace(line)
		n, convErr := strconv.Atoi(choiceText)
		if convErr != nil || n < 1 || n > len(models) {
			fmt.Fprintln(out, "Invalid choice, try again.")
			continue
		}
		return models[n-1], nil
	}
}

func platformFromModel(model string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "claude") {
		return "claude"
	}
	return "openai"
}

func (c *chatClient) streamChat(platform string, history []map[string]string) (string, error) {
	endpoint, payload, err := c.buildPayload(platform, history)
	if err != nil {
		return "", err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return "", fmt.Errorf("status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	reply, err := streamSSE(resp.Body, platform, os.Stdout)
	if err != nil {
		return reply, err
	}
	fmt.Fprintln(os.Stdout)
	return reply, nil
}

func (c *chatClient) buildPayload(platform string, history []map[string]string) (string, map[string]any, error) {
	switch platform {
	case "claude":
		return "/v1/messages", map[string]any{
			"model":      c.model,
			"stream":     true,
			"max_tokens": 1024,
			"messages":   history,
		}, nil
	default:
		payload := map[string]any{
			"model":  c.model,
			"stream": true,
			"input":  history,
		}
		if c.instructions != "" {
			payload["instructions"] = c.instructions
		}
		return "/v1/responses", payload, nil
	}
}

func streamSSE(r io.Reader, platform string, out io.Writer) (string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	var reply strings.Builder
	hasOutput := false
	for scanner.Scan() {
		line := scanner.Text()
		data, ok := parseSSEDataLine(line)
		if !ok {
			continue
		}
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}

		text := extractSSEText(data, platform, hasOutput)
		if text == "" {
			continue
		}
		reply.WriteString(text)
		if err := writeStreamChunk(out, text); err != nil {
			return reply.String(), err
		}
		hasOutput = true
	}
	if err := scanner.Err(); err != nil {
		return reply.String(), fmt.Errorf("read stream: %w", err)
	}
	return reply.String(), nil
}

func parseSSEDataLine(line string) (string, bool) {
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "data:")), true
}

func writeStreamChunk(out io.Writer, text string) error {
	if _, err := io.WriteString(out, text); err != nil {
		return fmt.Errorf("write stream output: %w", err)
	}
	if f, ok := out.(interface{ Flush() error }); ok {
		_ = f.Flush()
	}
	if f, ok := out.(*os.File); ok {
		_ = f.Sync()
	}
	return nil
}

func extractSSEText(data, platform string, hasOutput bool) string {
	var event map[string]any
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return ""
	}

	switch platform {
	case "claude":
		eventType, _ := event["type"].(string)
		if eventType != "content_block_delta" {
			return ""
		}
		delta, _ := event["delta"].(map[string]any)
		text, _ := delta["text"].(string)
		return text
	default:
		eventType, _ := event["type"].(string)
		switch eventType {
		case "response.output_text.delta":
			text, _ := event["delta"].(string)
			return text
		case "response.output_text.done":
			if hasOutput {
				return ""
			}
			text, _ := event["text"].(string)
			return text
		case "response.completed":
			if hasOutput {
				return ""
			}
			return extractOpenAICompletedText(event)
		default:
			return ""
		}
	}
}

func extractOpenAICompletedText(event map[string]any) string {
	response, _ := event["response"].(map[string]any)
	if response == nil {
		return ""
	}

	if outputText, _ := response["output_text"].(string); strings.TrimSpace(outputText) != "" {
		return outputText
	}

	output, _ := response["output"].([]any)
	if len(output) == 0 {
		return ""
	}

	var b strings.Builder
	for _, item := range output {
		itemMap, _ := item.(map[string]any)
		if itemMap == nil {
			continue
		}
		content, _ := itemMap["content"].([]any)
		for _, part := range content {
			partMap, _ := part.(map[string]any)
			if partMap == nil {
				continue
			}
			if partType, _ := partMap["type"].(string); partType != "output_text" {
				continue
			}
			text, _ := partMap["text"].(string)
			if text != "" {
				b.WriteString(text)
			}
		}
	}
	return b.String()
}

package forwarder

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type OpenAISSEAccumulator struct {
	model       string
	textBuilder strings.Builder
	lastResp    map[string]any
}

type OpenAIStreamError struct {
	Message string
	Type    string
	Code    string
}

func (e *OpenAIStreamError) Error() string {
	if e == nil {
		return "openai stream error"
	}
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = "openai stream error"
	}
	return msg
}

func AsOpenAIStreamError(err error) (*OpenAIStreamError, bool) {
	var streamErr *OpenAIStreamError
	if !errors.As(err, &streamErr) {
		return nil, false
	}
	return streamErr, true
}

func NewOpenAISSEAccumulator(model string) *OpenAISSEAccumulator {
	return &OpenAISSEAccumulator{model: model}
}

func (a *OpenAISSEAccumulator) ConsumeLine(line string) error {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "event:") || strings.HasPrefix(line, ":") {
		return nil
	}
	if !strings.HasPrefix(line, "data:") {
		return nil
	}

	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" || payload == "[DONE]" {
		return nil
	}

	var evt map[string]any
	if err := json.Unmarshal([]byte(payload), &evt); err != nil {
		return nil
	}

	if evtType, _ := evt["type"].(string); evtType == "response.output_text.delta" {
		if delta, _ := evt["delta"].(string); delta != "" {
			a.textBuilder.WriteString(delta)
		}
	}
	if evtType, _ := evt["type"].(string); evtType == "error" {
		streamErr := &OpenAIStreamError{Type: "api_error"}
		if e, ok := evt["error"].(map[string]any); ok {
			if msg, _ := e["message"].(string); msg != "" {
				streamErr.Message = msg
			}
			if typ, _ := e["type"].(string); strings.TrimSpace(typ) != "" {
				streamErr.Type = typ
			}
			if code, _ := e["code"].(string); strings.TrimSpace(code) != "" {
				streamErr.Code = code
			}
		}
		if strings.TrimSpace(streamErr.Message) == "" {
			if code := strings.TrimSpace(streamErr.Code); code != "" {
				streamErr.Message = fmt.Sprintf("openai stream error (%s)", code)
			} else {
				streamErr.Message = "openai stream error"
			}
		}
		return streamErr
	}

	if respObj, ok := evt["response"].(map[string]any); ok {
		a.lastResp = respObj
	}
	return nil
}

func (a *OpenAISSEAccumulator) Build() map[string]any {
	if a.lastResp != nil {
		resp := deepCopyAny(a.lastResp).(map[string]any)
		if _, ok := resp["status"]; !ok {
			resp["status"] = "completed"
		}
		if a.textBuilder.Len() > 0 {
			ensureOpenAIResponseOutput(resp, a.textBuilder.String())
		}
		return resp
	}

	model := a.model
	if model == "" {
		model = "gpt-5.1"
	}
	return map[string]any{
		"id":         fmt.Sprintf("resp_%d", time.Now().UnixNano()),
		"object":     "response",
		"created_at": time.Now().Unix(),
		"status":     "completed",
		"model":      model,
		"output": []any{
			map[string]any{
				"type": "message",
				"role": "assistant",
				"content": []any{
					map[string]any{
						"type": "output_text",
						"text": a.textBuilder.String(),
					},
				},
			},
		},
	}
}

func ensureOpenAIResponseOutput(resp map[string]any, text string) {
	if text == "" {
		return
	}
	if _, exists := resp["output"]; exists {
		return
	}
	resp["output"] = []any{
		map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []any{
				map[string]any{
					"type": "output_text",
					"text": text,
				},
			},
		},
	}
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

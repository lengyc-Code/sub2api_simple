package router

import "net/http"

type Handlers struct {
	ClaudeMessages        http.HandlerFunc
	Models                http.HandlerFunc
	OpenAIResponses       http.HandlerFunc
	OpenAIChatCompletions http.HandlerFunc
	AuthLogin             http.HandlerFunc
	Health                http.HandlerFunc
	NotFound              http.HandlerFunc
}

func Dispatch(w http.ResponseWriter, r *http.Request, h Handlers) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/messages":
		h.ClaudeMessages(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		h.Models(w, r)
	case r.Method == http.MethodPost && (r.URL.Path == "/v1/responses" || r.URL.Path == "/responses"):
		h.OpenAIResponses(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
		h.OpenAIChatCompletions(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/auth/login":
		h.AuthLogin(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/health":
		h.Health(w, r)
	default:
		h.NotFound(w, r)
	}
}

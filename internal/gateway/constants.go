package gateway

import "time"

//
// Domain Constants ?Anthropic (Claude)
//

const (
	platformAnthropic = "anthropic"
	platformOpenAI    = "openai"

	claudeAPIBetaURL  = "https://api.anthropic.com/v1/messages?beta=true"
	anthropicVersion  = "2023-06-01"
	maxRequestBodyMiB = 32

	betaOAuth               = "oauth-2025-04-20"
	betaClaudeCode          = "claude-code-20250219"
	betaInterleavedThinking = "interleaved-thinking-2025-05-14"

	claudeCodeSystemPrompt = "You are Claude Code, Anthropic's official CLI for Claude."
)

var claudeModels = []map[string]string{
	{"id": "claude-opus-4-6", "type": "model", "display_name": "Claude Opus 4.6", "created_at": "2026-02-06T00:00:00Z"},
	{"id": "claude-sonnet-4-6", "type": "model", "display_name": "Claude Sonnet 4.6", "created_at": "2026-02-18T00:00:00Z"},
	{"id": "claude-sonnet-4-5-20250929", "type": "model", "display_name": "Claude Sonnet 4.5", "created_at": "2025-09-29T00:00:00Z"},
	{"id": "claude-opus-4-5-20251101", "type": "model", "display_name": "Claude Opus 4.5", "created_at": "2025-11-01T00:00:00Z"},
	{"id": "claude-haiku-4-5-20251001", "type": "model", "display_name": "Claude Haiku 4.5", "created_at": "2025-10-01T00:00:00Z"},
}

var claudeModelIDOverrides = map[string]string{
	"claude-sonnet-4-5": "claude-sonnet-4-5-20250929",
	"claude-opus-4-5":   "claude-opus-4-5-20251101",
	"claude-haiku-4-5":  "claude-haiku-4-5-20251001",
}

var claudeOAuthDefaultHeaders = map[string]string{
	"User-Agent":                                "claude-cli/2.1.22 (external, cli)",
	"X-Stainless-Lang":                          "js",
	"X-Stainless-Package-Version":               "0.70.0",
	"X-Stainless-OS":                            "Linux",
	"X-Stainless-Arch":                          "arm64",
	"X-Stainless-Runtime":                       "node",
	"X-Stainless-Runtime-Version":               "v24.13.0",
	"X-Stainless-Retry-Count":                   "0",
	"X-Stainless-Timeout":                       "600",
	"X-App":                                     "cli",
	"Anthropic-Dangerous-Direct-Browser-Access": "true",
}

//
// Domain Constants ?OpenAI (Codex)
//

const (
	chatgptCodexURL      = "https://chatgpt.com/backend-api/codex/responses"
	openaiPlatformAPIURL = "https://api.openai.com/v1/responses"
	codexCLIUserAgent    = "codex_cli_rs/0.104.0"

	// OpenAI OAuth token exchange
	openaiDefaultClientID  = "app_EMoamEEZ73f0CkXaXp7hrann"
	openaiCallbackPort     = 1455
	openaiCallbackRedirect = "http://localhost:1455/auth/callback"
	oauthSessionTTL        = 10 * time.Minute
)

var openaiModels = []map[string]string{
	{"id": "gpt-5.4", "type": "model", "display_name": "GPT-5.4", "created_at": "2026-03-06T00:00:00Z"},
	{"id": "gpt-5.3-codex", "type": "model", "display_name": "GPT-5.3 Codex", "created_at": "2026-02-01T00:00:00Z"},
	{"id": "gpt-5.2", "type": "model", "display_name": "GPT-5.2", "created_at": "2025-12-01T00:00:00Z"},
	{"id": "gpt-5.2-codex", "type": "model", "display_name": "GPT-5.2 Codex", "created_at": "2025-12-01T00:00:00Z"},
	{"id": "gpt-5.1-codex-max", "type": "model", "display_name": "GPT-5.1 Codex Max", "created_at": "2025-10-01T00:00:00Z"},
	{"id": "gpt-5.1-codex-mini", "type": "model", "display_name": "GPT-5.1 Codex Mini", "created_at": "2025-10-01T00:00:00Z"},
}

// codexModelMap normalizes various Codex model aliases to canonical upstream model names.
var codexModelMap = map[string]string{
	"gpt-5.4":            "gpt-5.4",
	"gpt-5.3":            "gpt-5.3-codex",
	"gpt-5.3-none":       "gpt-5.3-codex",
	"gpt-5.3-low":        "gpt-5.3-codex",
	"gpt-5.3-medium":     "gpt-5.3-codex",
	"gpt-5.3-high":       "gpt-5.3-codex",
	"gpt-5.3-codex":      "gpt-5.3-codex",
	"gpt-5.2":            "gpt-5.2",
	"gpt-5.2-codex":      "gpt-5.2-codex",
	"gpt-5.1-codex":      "gpt-5.1-codex",
	"gpt-5.1-codex-mini": "gpt-5.1-codex-mini",
	"gpt-5.1":            "gpt-5.1",
	"gpt-5.1-codex-max":  "gpt-5.1-codex-max",
	"codex-mini-latest":  "gpt-5.1-codex-mini",
	"gpt-5-codex":        "gpt-5.1-codex",
	"gpt-5-codex-mini":   "gpt-5.1-codex-mini",
	"gpt-5":              "gpt-5.1",
}

package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

//
// Configuration
//

type Config struct {
	ListenAddr                string                    `json:"listen_addr"`
	AuthTokens                []string                  `json:"auth_tokens"`
	Accounts                  []AccountConfig           `json:"accounts"`
	OpenAIDefaultInstructions map[string]string         `json:"openai_default_instructions"`
	ModelExtraParams          map[string]map[string]any `json:"model_extra_params"`
	EnableRequestLog          bool                      `json:"enable_request_log"`
	EnableStreamDebugLog      bool                      `json:"enable_stream_debug_log"`
	EnableModelDebugLog       bool                      `json:"enable_model_debug_log"`
	MaxAccountSwitches        int                       `json:"max_account_switches"`
	StickySessionTTL          Duration                  `json:"sticky_session_ttl"`
	StreamReadTimeout         Duration                  `json:"stream_read_timeout"`

	configDir string // directory of the config file (for deriving state file path)
}

type AccountConfig struct {
	Name     string `json:"name"`
	Platform string `json:"platform"` // "anthropic" (default) or "openai"
	Type     string `json:"type"`     // "api_key" or "oauth"

	// API Key credentials (Anthropic or OpenAI)
	APIKey string `json:"api_key"`

	// OAuth credentials: client_id + refresh_token (preferred)
	ClientID     string `json:"client_id"`     // OpenAI OAuth client ID (default: Codex CLI app ID)
	RefreshToken string `json:"refresh_token"` // refresh_token for automatic access_token exchange

	// Legacy: direct bearer token (backward-compat, skips token refresh)
	OAuthToken string `json:"oauth_token"`

	// Manual override (auto-populated from id_token when using refresh_token)
	ChatGPTAccountID string `json:"chatgpt_account_id"`

	BaseURL     string   `json:"base_url"`    // optional custom upstream URL
	Proxy       string   `json:"proxy"`       // optional HTTP/SOCKS5 proxy
	Concurrency int      `json:"concurrency"` // max concurrent requests (default 3)
	Priority    int      `json:"priority"`    // lower = higher priority (default 50)
	Models      []string `json:"models"`      // allowed models (empty = all)
}

// Duration wraps time.Duration for JSON unmarshaling from strings like "1h", "30m".
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		var n int64
		if err2 := json.Unmarshal(b, &n); err2 != nil {
			return err
		}
		d.Duration = time.Duration(n) * time.Second
		return nil
	}
	var err error
	d.Duration, err = time.ParseDuration(s)
	return err
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	absPath, _ := filepath.Abs(path)
	cfg := &Config{
		ListenAddr:         ":8080",
		MaxAccountSwitches: 5,
		StickySessionTTL:   Duration{time.Hour},
		StreamReadTimeout:  Duration{5 * time.Minute},
		EnableRequestLog:   true,
		configDir:          filepath.Dir(absPath),
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if len(cfg.AuthTokens) == 0 {
		return nil, fmt.Errorf("auth_tokens is required")
	}
	if len(cfg.Accounts) == 0 {
		return nil, fmt.Errorf("at least one account is required")
	}
	for i := range cfg.Accounts {
		a := &cfg.Accounts[i]
		if a.Name == "" {
			a.Name = fmt.Sprintf("account-%d", i+1)
		}
		if a.Platform == "" {
			a.Platform = platformAnthropic
		}
		if a.Concurrency <= 0 {
			a.Concurrency = 3
		}
		if a.Priority <= 0 {
			a.Priority = 50
		}
		if a.Type == "" {
			if a.OAuthToken != "" || a.RefreshToken != "" {
				a.Type = "oauth"
			} else {
				a.Type = "api_key"
			}
		}
		if a.Platform == platformOpenAI && a.ClientID == "" {
			a.ClientID = openaiDefaultClientID
		}
	}
	return cfg, nil
}

// tokenStatePath returns the path to the token state file (alongside config file).
func (c *Config) tokenStatePath() string {
	return filepath.Join(c.configDir, "token_state.json")
}

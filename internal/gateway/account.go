package gateway

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//
// Account ?Runtime State for Each Upstream Account
//

type Account struct {
	Config AccountConfig

	mu              sync.Mutex
	activeRequests  int32 // atomic: current concurrent requests
	rateLimitUntil  time.Time
	lastUsedAt      time.Time
	totalForwarded  int64
	consecutiveErrs int
}

func newAccount(cfg AccountConfig) *Account {
	return &Account{Config: cfg}
}

func (a *Account) IsOAuth() bool          { return a.Config.Type == "oauth" }
func (a *Account) IsAPIKey() bool         { return a.Config.Type == "api_key" }
func (a *Account) IsAnthropic() bool      { return a.Config.Platform == platformAnthropic }
func (a *Account) IsOpenAI() bool         { return a.Config.Platform == platformOpenAI }
func (a *Account) IsOpenAIOAuth() bool    { return a.IsOpenAI() && a.IsOAuth() }
func (a *Account) UsesRefreshToken() bool { return a.IsOAuth() && a.Config.RefreshToken != "" }

func (a *Account) IsRateLimited() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return time.Now().Before(a.rateLimitUntil)
}

func (a *Account) SetRateLimited(until time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if until.After(a.rateLimitUntil) {
		a.rateLimitUntil = until
	}
}

func (a *Account) AcquireSlot() bool {
	cur := atomic.AddInt32(&a.activeRequests, 1)
	if int(cur) > a.Config.Concurrency {
		atomic.AddInt32(&a.activeRequests, -1)
		return false
	}
	return true
}

func (a *Account) ReleaseSlot() {
	atomic.AddInt32(&a.activeRequests, -1)
}

func (a *Account) LoadFactor() float64 {
	active := atomic.LoadInt32(&a.activeRequests)
	if a.Config.Concurrency <= 0 {
		return 1.0
	}
	return float64(active) / float64(a.Config.Concurrency)
}

func (a *Account) RecordSuccess() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastUsedAt = time.Now()
	a.totalForwarded++
	a.consecutiveErrs = 0
}

func (a *Account) RecordError() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.consecutiveErrs++
}

func (a *Account) SupportsModel(model string) bool {
	if len(a.Config.Models) == 0 {
		return true
	}
	for _, m := range a.Config.Models {
		if strings.EqualFold(m, model) {
			return true
		}
	}
	return false
}

// GetStaticToken returns the statically configured token (API key or legacy oauth_token).
// For accounts using refresh_token flow, use Gateway.getOpenAIAccessToken() instead.
func (a *Account) GetStaticToken() string {
	if a.IsOAuth() {
		return a.Config.OAuthToken
	}
	return a.Config.APIKey
}

// GetClaudeTargetURL returns the upstream URL for Anthropic Claude requests.
func (a *Account) GetClaudeTargetURL() string {
	if a.Config.BaseURL != "" {
		return strings.TrimRight(a.Config.BaseURL, "/") + "/v1/messages"
	}
	return claudeAPIBetaURL
}

// GetOpenAITargetURL returns the upstream URL for OpenAI Responses API requests.
func (a *Account) GetOpenAITargetURL() string {
	if a.IsOAuth() {
		return chatgptCodexURL
	}
	if a.Config.BaseURL != "" {
		return strings.TrimRight(a.Config.BaseURL, "/") + "/v1/responses"
	}
	return openaiPlatformAPIURL
}

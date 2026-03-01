package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	DefaultClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	tokenURL        = "https://auth.openai.com/oauth/token"
	refreshScopes   = "openid profile email"
	tokenSkew       = 3 * time.Minute
)

type Account struct {
	Name             string
	ClientID         string
	RefreshToken     string
	ProxyURL         string
	ChatGPTAccountID string
}

type tokenEntry struct {
	AccessToken      string
	ChatGPTAccountID string
	ExpiresAt        time.Time
	RefreshToken     string // may be rotated by the token endpoint
}

type persistedTokenState struct {
	Accounts map[string]*persistedAccountToken `json:"accounts"`
}

type persistedAccountToken struct {
	RefreshToken     string `json:"refresh_token"`
	AccessToken      string `json:"access_token,omitempty"`
	ChatGPTAccountID string `json:"chatgpt_account_id,omitempty"`
	ExpiresAt        string `json:"expires_at,omitempty"`
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// Manager handles OpenAI OAuth access token refresh and persistence.
type Manager struct {
	mu            sync.Mutex
	tokens        map[string]*tokenEntry
	client        *http.Client
	stateFilePath string
}

func NewTokenManager(client *http.Client, stateFilePath string) *Manager {
	m := &Manager{
		tokens:        make(map[string]*tokenEntry),
		client:        client,
		stateFilePath: stateFilePath,
	}
	m.loadState()
	return m
}

func (m *Manager) loadState() {
	data, err := os.ReadFile(m.stateFilePath)
	if err != nil {
		return
	}
	var state persistedTokenState
	if err := json.Unmarshal(data, &state); err != nil {
		log.Printf("[token] failed to parse %s: %v", m.stateFilePath, err)
		return
	}
	if state.Accounts == nil {
		return
	}
	for name, pt := range state.Accounts {
		entry := &tokenEntry{
			RefreshToken:     pt.RefreshToken,
			AccessToken:      pt.AccessToken,
			ChatGPTAccountID: pt.ChatGPTAccountID,
		}
		if pt.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, pt.ExpiresAt); err == nil {
				entry.ExpiresAt = t
			}
		}
		m.tokens[name] = entry
	}
	log.Printf("[token] loaded persisted state for %d account(s) from %s", len(state.Accounts), m.stateFilePath)
}

func (m *Manager) saveState() {
	state := persistedTokenState{
		Accounts: make(map[string]*persistedAccountToken, len(m.tokens)),
	}
	for name, entry := range m.tokens {
		state.Accounts[name] = &persistedAccountToken{
			RefreshToken:     entry.RefreshToken,
			AccessToken:      entry.AccessToken,
			ChatGPTAccountID: entry.ChatGPTAccountID,
			ExpiresAt:        entry.ExpiresAt.Format(time.RFC3339),
		}
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		log.Printf("[token] failed to marshal token state: %v", err)
		return
	}
	if err := os.WriteFile(m.stateFilePath, data, 0600); err != nil {
		log.Printf("[token] failed to write %s: %v", m.stateFilePath, err)
	}
}

func (m *Manager) HasToken(accountName string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.tokens[accountName]
	return ok && (entry.AccessToken != "" || entry.RefreshToken != "")
}

func (m *Manager) GetAccessToken(ctx context.Context, account Account) (string, string, error) {
	m.mu.Lock()
	entry, ok := m.tokens[account.Name]
	if ok && entry.AccessToken != "" && time.Until(entry.ExpiresAt) > tokenSkew {
		token, accountID := entry.AccessToken, entry.ChatGPTAccountID
		m.mu.Unlock()
		return token, accountID, nil
	}
	m.mu.Unlock()

	return m.refreshAndCache(ctx, account)
}

func (m *Manager) refreshAndCache(ctx context.Context, account Account) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry, ok := m.tokens[account.Name]; ok && entry.AccessToken != "" && time.Until(entry.ExpiresAt) > tokenSkew {
		return entry.AccessToken, entry.ChatGPTAccountID, nil
	}

	refreshToken := account.RefreshToken
	if existing, ok := m.tokens[account.Name]; ok && existing.RefreshToken != "" {
		refreshToken = existing.RefreshToken
	}
	if refreshToken == "" {
		return "", "", fmt.Errorf("account %q: no refresh_token configured", account.Name)
	}

	tokenResp, err := m.doTokenRefresh(ctx, account.ClientID, refreshToken, account.ProxyURL)
	if err != nil {
		return "", "", fmt.Errorf("account %q: token refresh failed: %w", account.Name, err)
	}

	entry := &tokenEntry{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
	}
	if tokenResp.RefreshToken != "" {
		entry.RefreshToken = tokenResp.RefreshToken
	}
	if tokenResp.IDToken != "" {
		if claims, err := ParseIDToken(tokenResp.IDToken); err == nil {
			if claims.OpenAIAuth != nil && claims.OpenAIAuth.ChatGPTAccountID != "" {
				entry.ChatGPTAccountID = claims.OpenAIAuth.ChatGPTAccountID
			}
		} else {
			log.Printf("[token] failed to parse id_token for %q: %v", account.Name, err)
		}
	}
	if entry.ChatGPTAccountID == "" && account.ChatGPTAccountID != "" {
		entry.ChatGPTAccountID = account.ChatGPTAccountID
	}

	m.tokens[account.Name] = entry
	m.saveState()

	log.Printf("[token] refreshed access_token for %q (expires_in=%ds, chatgpt_account_id=%s)",
		account.Name, tokenResp.ExpiresIn, entry.ChatGPTAccountID)
	return entry.AccessToken, entry.ChatGPTAccountID, nil
}

func (m *Manager) doTokenRefresh(ctx context.Context, clientID, refreshToken, proxyURL string) (*TokenResponse, error) {
	if clientID == "" {
		clientID = DefaultClientID
	}

	formData := url.Values{}
	formData.Set("grant_type", "refresh_token")
	formData.Set("client_id", clientID)
	formData.Set("refresh_token", refreshToken)
	formData.Set("scope", refreshScopes)

	client := m.client
	if proxyURL != "" {
		if pURL, err := url.Parse(proxyURL); err == nil {
			client = &http.Client{
				Transport: &http.Transport{Proxy: http.ProxyURL(pURL)},
				Timeout:   30 * time.Second,
			}
		}
	}

	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, tokenURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "codex-cli/0.91.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("empty access_token in response")
	}
	return &tokenResp, nil
}

func (m *Manager) ExchangeAuthCode(ctx context.Context, code, codeVerifier, redirectURI, clientID, proxyURL string) (*TokenResponse, error) {
	if clientID == "" {
		clientID = DefaultClientID
	}
	formData := url.Values{}
	formData.Set("grant_type", "authorization_code")
	formData.Set("client_id", clientID)
	formData.Set("code", code)
	formData.Set("redirect_uri", redirectURI)
	formData.Set("code_verifier", codeVerifier)

	client := m.client
	if proxyURL != "" {
		if pURL, err := url.Parse(proxyURL); err == nil {
			client = &http.Client{
				Transport: &http.Transport{Proxy: http.ProxyURL(pURL)},
				Timeout:   30 * time.Second,
			}
		}
	}

	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, tokenURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "codex-cli/0.91.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("empty access_token in response")
	}
	return &tokenResp, nil
}

func (m *Manager) SaveOAuthLogin(accountName, fallbackAccountID string, tokenResp *TokenResponse) (string, error) {
	if tokenResp == nil {
		return "", fmt.Errorf("token response is nil")
	}
	entry := &tokenEntry{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
	}
	if tokenResp.IDToken != "" {
		if claims, err := ParseIDToken(tokenResp.IDToken); err == nil {
			if claims.OpenAIAuth != nil && claims.OpenAIAuth.ChatGPTAccountID != "" {
				entry.ChatGPTAccountID = claims.OpenAIAuth.ChatGPTAccountID
			}
		}
	}
	if entry.ChatGPTAccountID == "" {
		entry.ChatGPTAccountID = fallbackAccountID
	}

	m.mu.Lock()
	m.tokens[accountName] = entry
	m.saveState()
	m.mu.Unlock()
	return entry.ChatGPTAccountID, nil
}

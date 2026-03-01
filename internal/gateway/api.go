package gateway

import (
	"context"
	"log"
	"time"
)

// LoadConfig reads and validates gateway config from disk.
func LoadConfig(path string) (*Config, error) {
	return loadConfig(path)
}

// New builds an HTTP gateway from config.
func New(cfg *Config) *Gateway {
	return newGateway(cfg)
}

// PrewarmOpenAITokens pre-fetches access tokens and returns account names that still need browser login.
func (g *Gateway) PrewarmOpenAITokens(ctx context.Context, perAccountTimeout time.Duration) []string {
	if ctx == nil {
		ctx = context.Background()
	}
	if perAccountTimeout <= 0 {
		perAccountTimeout = 30 * time.Second
	}

	needsLogin := make([]string, 0)
	for _, account := range g.manager.accounts {
		if account.UsesRefreshToken() || g.tokenManager.HasToken(account.Config.Name) {
			log.Printf("[token] pre-warming access_token for %q...", account.Config.Name)
			accountCtx, cancel := context.WithTimeout(ctx, perAccountTimeout)
			if _, _, err := g.getOpenAIAccessToken(accountCtx, account); err != nil {
				log.Printf("[token] WARNING: pre-warm failed for %q: %v", account.Config.Name, err)
				needsLogin = append(needsLogin, account.Config.Name)
			} else {
				log.Printf("[token] pre-warm OK for %q", account.Config.Name)
			}
			cancel()
			continue
		}

		if account.IsOpenAIOAuth() && account.Config.OAuthToken == "" {
			needsLogin = append(needsLogin, account.Config.Name)
		}
	}

	return needsLogin
}

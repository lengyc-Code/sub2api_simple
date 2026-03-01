package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	authorizeURL = "https://auth.openai.com/oauth/authorize"
	authScopes   = "openid profile email offline_access"
)

type PendingSession struct {
	AccountName  string
	State        string
	CodeVerifier string
	ClientID     string
	RedirectURI  string
	CreatedAt    time.Time
}

type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]*PendingSession
	ttl      time.Duration
}

func NewSessionStore(ttl time.Duration) *SessionStore {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	s := &SessionStore{
		sessions: make(map[string]*PendingSession),
		ttl:      ttl,
	}
	go s.cleanupLoop()
	return s
}

func (s *SessionStore) Set(session *PendingSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.State] = session
}

func (s *SessionStore) GetAndDelete(state string) (*PendingSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[state]
	if !ok {
		return nil, false
	}
	if time.Since(sess.CreatedAt) > s.ttl {
		delete(s.sessions, state)
		return nil, false
	}
	delete(s.sessions, state)
	return sess, true
}

func (s *SessionStore) cleanupLoop() {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		for state, sess := range s.sessions {
			if time.Since(sess.CreatedAt) > s.ttl {
				delete(s.sessions, state)
			}
		}
		s.mu.Unlock()
	}
}

func GenerateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func GenerateCodeVerifier() (string, error) {
	b := make([]byte, 64)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func GenerateCodeChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return strings.TrimRight(base64.URLEncoding.EncodeToString(hash[:]), "=")
}

func BuildAuthURL(state, codeChallenge, redirectURI, clientID string) string {
	if clientID == "" {
		clientID = DefaultClientID
	}
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("scope", authScopes)
	params.Set("state", state)
	params.Set("code_challenge", codeChallenge)
	params.Set("code_challenge_method", "S256")
	params.Set("id_token_add_organizations", "true")
	params.Set("codex_cli_simplified_flow", "true")
	return authorizeURL + "?" + params.Encode()
}

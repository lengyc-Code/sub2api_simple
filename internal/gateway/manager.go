package gateway

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

//
// AccountManager ?Pool Management & Scheduling
//

type AccountManager struct {
	accounts []*Account
}

func newAccountManager(configs []AccountConfig) *AccountManager {
	accounts := make([]*Account, len(configs))
	for i, cfg := range configs {
		accounts[i] = newAccount(cfg)
	}
	return &AccountManager{accounts: accounts}
}

type accountCandidate struct {
	account  *Account
	priority int
	load     float64
}

// SelectAccount picks the best available account for the given model and platform,
// excluding accounts in the excludeIDs set.
//
// Selection follows a 3-layer funnel (mirroring the full project's scheduling):
//  1. Sticky session ?if stickyAccount is provided and available, use it
//  2. Priority + load-aware ?sort candidates by priority then load factor
//  3. Fallback ?return the least-loaded candidate even if at capacity
func (am *AccountManager) SelectAccount(platform, model string, stickyAccount *Account, excludeIDs map[*Account]bool) (*Account, error) {
	// Layer 1: sticky session
	if stickyAccount != nil && !excludeIDs[stickyAccount] {
		if stickyAccount.Config.Platform == platform && stickyAccount.SupportsModel(model) &&
			!stickyAccount.IsRateLimited() && stickyAccount.AcquireSlot() {
			return stickyAccount, nil
		}
	}

	// Layer 2: priority + load aware selection
	candidates := make([]accountCandidate, 0, len(am.accounts))
	for _, a := range am.accounts {
		if excludeIDs[a] {
			continue
		}
		if a.Config.Platform != platform {
			continue
		}
		if !a.SupportsModel(model) {
			continue
		}
		if a.IsRateLimited() {
			continue
		}
		candidates = append(candidates, accountCandidate{
			account:  a,
			priority: a.Config.Priority,
			load:     a.LoadFactor(),
		})
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no available %s account for model %q", platform, model)
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].priority != candidates[j].priority {
			return candidates[i].priority < candidates[j].priority
		}
		return candidates[i].load < candidates[j].load
	})

	for _, c := range candidates {
		if c.account.AcquireSlot() {
			return c.account, nil
		}
	}

	// Layer 3: fallback ?force-acquire for availability
	best := candidates[0].account
	atomic.AddInt32(&best.activeRequests, 1)
	return best, nil
}

//
// SessionStore ?In-Memory Sticky Session Cache
//

type sessionEntry struct {
	account   *Account
	expiresAt time.Time
}

type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]sessionEntry
	ttl      time.Duration
}

func newSessionStore(ttl time.Duration) *SessionStore {
	ss := &SessionStore{
		sessions: make(map[string]sessionEntry),
		ttl:      ttl,
	}
	go ss.cleanupLoop()
	return ss
}

func (ss *SessionStore) Get(key string) *Account {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	entry, ok := ss.sessions[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil
	}
	return entry.account
}

func (ss *SessionStore) Set(key string, account *Account) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.sessions[key] = sessionEntry{
		account:   account,
		expiresAt: time.Now().Add(ss.ttl),
	}
}

func (ss *SessionStore) Remove(key string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	delete(ss.sessions, key)
}

func (ss *SessionStore) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		ss.mu.Lock()
		now := time.Now()
		for k, v := range ss.sessions {
			if now.After(v.expiresAt) {
				delete(ss.sessions, k)
			}
		}
		ss.mu.Unlock()
	}
}

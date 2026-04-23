package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

// ErrNotFound is returned by Get when no session exists for the given ID.
var ErrNotFound = errors.New("session: not found")

// Session holds the OAuth2 token set and resolved identity for one authenticated browser user.
type Session struct {
	ID           string
	Sub          string
	Email        string
	Name         string
	AccessToken  string
	RefreshToken string
	IDToken      string
	ExpiresAt    time.Time // access token expiry
	CreatedAt    time.Time
}

// Store manages server-side sessions and short-lived PKCE pending states.
type Store interface {
	Create(s *Session) (id string, err error)
	Get(id string) (*Session, error)
	Update(s *Session) error
	Delete(id string)
	SavePending(state, verifier string)
	TakePending(state string) (verifier string, ok bool)
}

type pendingEntry struct {
	verifier  string
	expiresAt time.Time
}

// InMemoryStore is the default in-process Store implementation.
// Sessions and PKCE pending states are kept in separate mutex-protected maps.
type InMemoryStore struct {
	maxAge   time.Duration
	stateTTL time.Duration

	mu       sync.RWMutex
	sessions map[string]*Session

	pmu     sync.Mutex
	pending map[string]pendingEntry
}

// NewInMemoryStore returns an InMemoryStore.
// maxAge is the maximum session lifetime; PKCE pending states always expire after 5 minutes.
func NewInMemoryStore(maxAge time.Duration) *InMemoryStore {
	return &InMemoryStore{
		maxAge:   maxAge,
		stateTTL: 5 * time.Minute,
		sessions: make(map[string]*Session),
		pending:  make(map[string]pendingEntry),
	}
}

// GenerateSID returns a cryptographically random 32-byte hex session ID.
func GenerateSID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Create assigns a new random ID to s, records CreatedAt, stores it, and returns the ID.
func (s *InMemoryStore) Create(sess *Session) (string, error) {
	id, err := GenerateSID()
	if err != nil {
		return "", err
	}
	sess.ID = id
	sess.CreatedAt = time.Now()
	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()
	return id, nil
}

// Get returns a copy of the session or ErrNotFound.
// A copy is returned so callers can safely mutate fields (e.g. during token refresh)
// without racing against other goroutines that also hold a reference to the session.
func (s *InMemoryStore) Get(id string) (*Session, error) {
	s.mu.RLock()
	sess := s.sessions[id]
	s.mu.RUnlock()
	if sess == nil {
		return nil, ErrNotFound
	}
	cp := *sess
	return &cp, nil
}

// Update replaces the session record. Returns ErrNotFound if the session was concurrently deleted.
func (s *InMemoryStore) Update(sess *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[sess.ID]; !ok {
		return ErrNotFound
	}
	s.sessions[sess.ID] = sess
	return nil
}

// Delete removes the session; no-op if not found.
func (s *InMemoryStore) Delete(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// SavePending stores a PKCE state→verifier pair for up to 5 minutes.
func (s *InMemoryStore) SavePending(state, verifier string) {
	s.pmu.Lock()
	s.pending[state] = pendingEntry{verifier: verifier, expiresAt: time.Now().Add(s.stateTTL)}
	s.pmu.Unlock()
}

// TakePending atomically retrieves and deletes the verifier for state.
// Returns ("", false) if the state is unknown or expired.
func (s *InMemoryStore) TakePending(state string) (string, bool) {
	s.pmu.Lock()
	defer s.pmu.Unlock()
	entry, ok := s.pending[state]
	if !ok || time.Now().After(entry.expiresAt) {
		delete(s.pending, state)
		return "", false
	}
	delete(s.pending, state)
	return entry.verifier, true
}

// CookieDomain resolves the Domain attribute for session cookies.
// "auto" derives the shared parent domain from the request Host header (e.g.
// bff.fusion.local → .fusion.local) so the cookie is visible across subdomains.
// On localhost or single-label hosts the domain attribute is omitted (empty string).
func CookieDomain(configured, requestHost string) string {
	if configured != "auto" {
		return configured
	}
	host := requestHost
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	if host == "localhost" || !strings.Contains(host, ".") {
		return ""
	}
	return "." + host[strings.Index(host, ".")+1:]
}

// Reap removes sessions older than maxAge and expired PKCE pending states.
// Call this periodically from a context-aware background goroutine.
func (s *InMemoryStore) Reap() {
	now := time.Now()

	s.mu.Lock()
	for id, sess := range s.sessions {
		if now.After(sess.CreatedAt.Add(s.maxAge)) {
			delete(s.sessions, id)
		}
	}
	s.mu.Unlock()

	s.pmu.Lock()
	for state, entry := range s.pending {
		if now.After(entry.expiresAt) {
			delete(s.pending, state)
		}
	}
	s.pmu.Unlock()
}

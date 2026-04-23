package allowlist

import (
	"strings"
	"sync"
	"time"
)

// Checker determines whether a user is permitted to access the platform.
// Implementations may be swapped (in-memory, database, etc.) without changing
// the auth middleware.
type Checker interface {
	Permitted(sub, email string) bool
}

// staticChecker is an in-memory Checker built from a list of entries.
// Entries containing "@" are matched against the email claim;
// all other entries are matched against the sub claim.
// An empty entry list allows any authenticated user.
type staticChecker struct {
	emails   map[string]struct{}
	subs     map[string]struct{}
	allowAll bool
}

// New builds a Checker from a list of entries.
func New(entries []string) Checker {
	if len(entries) == 0 {
		return &staticChecker{allowAll: true}
	}
	c := &staticChecker{
		emails: make(map[string]struct{}),
		subs:   make(map[string]struct{}),
	}
	for _, e := range entries {
		if strings.Contains(e, "@") {
			c.emails[e] = struct{}{}
		} else {
			c.subs[e] = struct{}{}
		}
	}
	return c
}

func (c *staticChecker) Permitted(sub, email string) bool {
	if c.allowAll {
		return true
	}
	if _, ok := c.subs[sub]; ok {
		return true
	}
	_, ok := c.emails[email]
	return ok
}

// WithTTLCache wraps a Checker with a per-user result cache.
// The result for each (sub, email) pair is cached for ttl; on expiry the inner
// Checker is consulted. Useful when the inner Checker performs I/O.
func WithTTLCache(ttl time.Duration, inner Checker) Checker {
	return &cachedChecker{
		inner: inner,
		ttl:   ttl,
		cache: make(map[string]cachedEntry),
	}
}

type cachedEntry struct {
	permitted bool
	expiresAt time.Time
}

type cachedChecker struct {
	inner Checker
	ttl   time.Duration
	mu    sync.Mutex
	cache map[string]cachedEntry
}

func (c *cachedChecker) Permitted(sub, email string) bool {
	key := sub + "\x00" + email
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.cache[key]; ok && now.Before(e.expiresAt) {
		return e.permitted
	}
	result := c.inner.Permitted(sub, email)
	c.cache[key] = cachedEntry{permitted: result, expiresAt: now.Add(c.ttl)}
	return result
}

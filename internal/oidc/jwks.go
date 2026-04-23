package oidc

import (
	"context"
	"sync"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
)

// cachingKeySet wraps go-oidc's RemoteKeySet with a configurable TTL.
// On TTL expiry the next call replaces the inner key set, forcing a fresh
// JWKS fetch independent of HTTP cache headers from the provider.
type cachingKeySet struct {
	mu        sync.RWMutex
	inner     gooidc.KeySet
	expiresAt time.Time
	jwksURL   string
	ttl       time.Duration
}

func newCachingKeySet(ctx context.Context, jwksURL string, ttl time.Duration) gooidc.KeySet {
	return &cachingKeySet{
		inner:     gooidc.NewRemoteKeySet(ctx, jwksURL),
		expiresAt: time.Now().Add(ttl),
		jwksURL:   jwksURL,
		ttl:       ttl,
	}
}

func (c *cachingKeySet) VerifySignature(ctx context.Context, jwt string) ([]byte, error) {
	c.mu.RLock()
	expired := time.Now().After(c.expiresAt)
	inner := c.inner
	c.mu.RUnlock()

	if expired {
		c.mu.Lock()
		if time.Now().After(c.expiresAt) {
			// Use context.Background() so a cancelled app context (e.g. after SIGTERM)
			// does not break JWKS refreshes during graceful drain.
			c.inner = gooidc.NewRemoteKeySet(context.Background(), c.jwksURL)
			c.expiresAt = time.Now().Add(c.ttl)
		}
		inner = c.inner
		c.mu.Unlock()
	}

	return inner.VerifySignature(ctx, jwt)
}

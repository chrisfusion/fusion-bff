package token

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Provider supplies a Kubernetes SA token for upstream authentication.
type Provider interface {
	Token(ctx context.Context) (string, error)
}

// FileProvider reads a SA token from disk with a TTL-based in-memory cache.
// Kubernetes rotates projected SA tokens; the TTL ensures rotation is picked up
// without reading the file on every request.
type FileProvider struct {
	mu        sync.RWMutex
	cached    string
	expiresAt time.Time
	path      string
	ttl       time.Duration
}

func NewFileProvider(path string, ttl time.Duration) *FileProvider {
	return &FileProvider{path: path, ttl: ttl}
}

func (p *FileProvider) Token(_ context.Context) (string, error) {
	p.mu.RLock()
	if time.Now().Before(p.expiresAt) {
		t := p.cached
		p.mu.RUnlock()
		return t, nil
	}
	p.mu.RUnlock()

	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Now().Before(p.expiresAt) {
		return p.cached, nil
	}
	data, err := os.ReadFile(p.path)
	if err != nil {
		return "", fmt.Errorf("reading SA token from %s: %w", p.path, err)
	}
	p.cached = strings.TrimSpace(string(data))
	p.expiresAt = time.Now().Add(p.ttl)
	return p.cached, nil
}

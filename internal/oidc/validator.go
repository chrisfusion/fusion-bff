package oidc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
)

// groupClaim accepts either a plain group name string (the standard Keycloak
// "Group Membership" mapper output) or a Keycloak GroupRepresentation object
// (some custom mappers emit the full object, e.g. {"name": "...", "path": "..."})
// instead of just the name.
type groupClaim string

func (g *groupClaim) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		*g = groupClaim(name)
		return nil
	}
	var obj struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	*g = groupClaim(obj.Name)
	return nil
}

// TokenValidator validates a raw Bearer token and returns the resolved claims.
type TokenValidator interface {
	Validate(ctx context.Context, rawToken string) (*UserClaims, error)
}

type oidcValidator struct {
	verifier *gooidc.IDTokenVerifier
}

// NewValidator constructs a TokenValidator that verifies JWTs against the given
// JWKS URL without performing OIDC provider discovery at startup.
func NewValidator(ctx context.Context, issuerURL, clientID, jwksURL string, cacheTTL time.Duration) (TokenValidator, error) {
	keySet := newCachingKeySet(ctx, jwksURL, cacheTTL)
	verifier := gooidc.NewVerifier(issuerURL, keySet, &gooidc.Config{ClientID: clientID})
	return &oidcValidator{verifier: verifier}, nil
}

func (v *oidcValidator) Validate(ctx context.Context, rawToken string) (*UserClaims, error) {
	token, err := v.verifier.Verify(ctx, rawToken)
	if err != nil {
		return nil, fmt.Errorf("token verification failed: %w", err)
	}

	var raw struct {
		Sub    string       `json:"sub"`
		Email  string       `json:"email"`
		Name   string       `json:"name"`
		Groups []groupClaim `json:"groups"`
	}
	if err := token.Claims(&raw); err != nil {
		return nil, fmt.Errorf("extracting claims: %w", err)
	}

	// Keycloak sends groups with a leading "/" (e.g. "/team-data"); normalise to bare names.
	groups := make([]string, 0, len(raw.Groups))
	for _, g := range raw.Groups {
		groups = append(groups, strings.TrimLeft(string(g), "/"))
	}

	return &UserClaims{
		Subject: raw.Sub,
		Email:   raw.Email,
		Name:    raw.Name,
		Groups:  groups,
	}, nil
}

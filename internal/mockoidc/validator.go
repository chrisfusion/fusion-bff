package mockoidc

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	oidcpkg "github.com/fusion-platform/fusion-bff/internal/oidc"
)

// mockValidator implements oidc.TokenValidator using the in-memory RSA key.
// It does not perform any HTTP requests — signature verification uses the key
// held by the Server that created it.
type mockValidator struct {
	publicKey *rsa.PublicKey
}

func (v *mockValidator) Validate(_ context.Context, rawToken string) (*oidcpkg.UserClaims, error) {
	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format")
	}

	sigInput := parts[0] + "." + parts[1]
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}

	h := sha256.Sum256([]byte(sigInput))
	if err := rsa.VerifyPKCS1v15(v.publicKey, crypto.SHA256, h[:], sig); err != nil {
		return nil, fmt.Errorf("invalid token signature: %w", err)
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}

	var claims struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
		Name  string `json:"name"`
		Exp   int64  `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal claims: %w", err)
	}

	if time.Now().Unix() > claims.Exp {
		return nil, fmt.Errorf("token expired")
	}

	return &oidcpkg.UserClaims{
		Subject: claims.Sub,
		Email:   claims.Email,
		Name:    claims.Name,
	}, nil
}

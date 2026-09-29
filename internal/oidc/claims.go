package oidc

import (
	"encoding/json"
)

// DefaultUserIDClaim is the claim used as the instance-scoped unique user identifier
// when OIDC_USER_ID_CLAIM is not configured.
const DefaultUserIDClaim = "sub"

// UserClaims holds the identity fields extracted from a validated JWT.
type UserClaims struct {
	Subject string
	// UserID is the value of the configured identifier claim (OIDC_USER_ID_CLAIM, default "sub").
	// It is unique within one OIDC instance but may differ between instances; empty when the
	// claim is absent from the token.
	UserID string
	Email  string
	Name   string   // "name" claim; empty if not present in the token
	Groups []string // "groups" claim; Keycloak sends leading "/" which is stripped
}

// extractStringClaim returns the named top-level claim as a string. String and numeric
// claims are supported; anything else (missing, null, object, array) yields "".
func extractStringClaim(claims map[string]json.RawMessage, name string) string {
	raw, ok := claims[name]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String()
	}
	return ""
}

// ExtractUserID returns the value of the identifier claim from a decoded claim set.
func ExtractUserID(claims map[string]json.RawMessage, claim string) string {
	if claim == "" {
		claim = DefaultUserIDClaim
	}
	return extractStringClaim(claims, claim)
}

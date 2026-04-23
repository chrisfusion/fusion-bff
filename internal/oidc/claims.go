package oidc

// UserClaims holds the identity fields extracted from a validated JWT.
type UserClaims struct {
	Subject string
	Email   string
	Name    string // "name" claim; empty if not present in the token
}

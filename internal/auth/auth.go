// Package auth provides token claims, validation, and role model primitives
// for Ohara's HTTP bearer authentication. It establishes the foundation for
// future scoped authorization (Phase 2.2+) while keeping the current static
// token path fully backwards compatible.
//
// No external JWT dependency is introduced at this layer. Phase 2.2 may add
// one for structured token parsing; this package provides the interface and
// types that isolate that decision.
package auth

import (
	"context"
	"crypto/subtle"
	"errors"
)

// Role represents a granted access level for authorization decisions.
// Higher roles implicitly grant access to lower-role operations:
//
//	RoleAdmin → RoleWrite → RoleRead
type Role string

const (
	// RoleRead is the lowest authenticated role: can read non-sensitive data.
	RoleRead Role = "read"
	// RoleWrite can create and update resources (non-destructive mutations).
	RoleWrite Role = "write"
	// RoleAdmin can perform destructive and administrative operations.
	RoleAdmin Role = "admin"
)

// roleRank maps roles to their hierarchy level for HasRole comparisons.
var roleRank = map[Role]int{
	RoleRead:  0,
	RoleWrite: 1,
	RoleAdmin: 2,
}

// Claims represents the identity and permissions derived from a validated token.
// Future phases may add fields like ProjectScopes, ExpiresAt, or Metadata.
type Claims struct {
	// Subject identifies the token principal (e.g. "static-token", or a JWT sub claim).
	Subject string
	// Roles enumerates the access levels granted to this principal.
	Roles []Role
	// Token is the raw bearer token value (included for audit/logging).
	Token string
	// AllowedProjects constrains which projects this principal may access.
	// Nil or empty means unrestricted (all projects allowed).
	// Non-empty means the principal may only access listed projects.
	AllowedProjects []string
}

// AllProjectsAllowed reports whether this principal has unrestricted
// project access (nil or empty AllowedProjects).
func (c *Claims) AllProjectsAllowed() bool {
	return len(c.AllowedProjects) == 0
}

// IsProjectAllowed checks whether the given project is in the principal's
// allowlist. Always returns true when the allowlist is empty (unrestricted).
func (c *Claims) IsProjectAllowed(project string) bool {
	if c.AllProjectsAllowed() {
		return true
	}
	for _, allowed := range c.AllowedProjects {
		if allowed == project {
			return true
		}
	}
	return false
}

// IsLowTrust reports whether the principal is a low-trust caller.
// Low-trust principals have only RoleRead (no write or admin access)
// and should receive filtered/redacted memory responses.
// Nil claims (no authentication) are NOT low-trust (backward compatible).
func (c *Claims) IsLowTrust() bool {
	if c == nil {
		return false
	}
	return !c.HasRole(RoleWrite)
}

// HasRole reports whether the claims grant at least the given role level.
// Higher roles implicitly satisfy lower role requirements (e.g. RoleAdmin
// satisfies RoleWrite and RoleRead).
func (c *Claims) HasRole(required Role) bool {
	requiredRank := roleRank[required]
	for _, r := range c.Roles {
		if roleRank[r] >= requiredRank {
			return true
		}
	}
	return false
}

// ErrInvalidToken is returned by Authenticator when the token is not valid.
var ErrInvalidToken = errors.New("invalid token")

// ErrInsufficientRole is returned when the authenticated principal does not
// have the required role level for the requested operation.
var ErrInsufficientRole = errors.New("insufficient role")

// RequireRole checks that claims grant at least the given role level.
// Returns ErrInsufficientRole if claims is nil or the required role is not met.
func RequireRole(claims *Claims, required Role) error {
	if claims == nil || !claims.HasRole(required) {
		return ErrInsufficientRole
	}
	return nil
}

// ErrProjectNotAllowed is returned when the principal attempts to access a
// project that is not in their AllowedProjects allowlist.
var ErrProjectNotAllowed = errors.New("project not allowed")

// RequireProject checks that the claims allow access to the given project.
// An empty or nil AllowedProjects means unrestricted (all projects allowed).
// Returns ErrProjectNotAllowed if the project is not in the allowlist.
func RequireProject(claims *Claims, project string) error {
	if claims == nil || claims.AllProjectsAllowed() {
		return nil
	}
	if !claims.IsProjectAllowed(project) {
		return ErrProjectNotAllowed
	}
	return nil
}

// Authenticator validates bearer tokens and returns the associated Claims.
//
// Implementations:
//   - StaticTokenAuthenticator — simple constant-time comparison (Phase 2.1+)
//   - (future) JWTAuthenticator — token parsing with signature verification (Phase 2.2+)
type Authenticator interface {
	// Authenticate validates a raw bearer token and returns the parsed claims.
	// Returns ErrInvalidToken if the token is not valid.
	Authenticate(token string) (*Claims, error)
}

// StaticTokenAuthenticator implements Authenticator using a single pre-shared
// static bearer token with constant-time comparison. This preserves the exact
// behaviour from Phase 0.2 while exposing Claims for future authorization.
type StaticTokenAuthenticator struct {
	token string
}

// NewStaticTokenAuthenticator creates an authenticator that accepts exactly
// the given token value.
func NewStaticTokenAuthenticator(token string) *StaticTokenAuthenticator {
	return &StaticTokenAuthenticator{token: token}
}

// Authenticate validates the raw token using constant-time comparison.
// On success, returns Claims with RoleAdmin (static token implies full access).
func (a *StaticTokenAuthenticator) Authenticate(raw string) (*Claims, error) {
	if subtle.ConstantTimeCompare([]byte(raw), []byte(a.token)) != 1 {
		return nil, ErrInvalidToken
	}
	return &Claims{
		Subject: "static-token",
		Roles:   []Role{RoleAdmin},
		Token:   raw,
	}, nil
}

// contextKey is an unexported type for context value keys to avoid collisions.
type contextKey string

const claimsContextKey contextKey = "ohara-auth-claims"

// ContextWithClaims attaches authenticated Claims to the context.
func ContextWithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, claimsContextKey, claims)
}

// ClaimsFromContext extracts authenticated Claims from the context.
// Returns nil if the request has not been authenticated.
func ClaimsFromContext(ctx context.Context) *Claims {
	claims, _ := ctx.Value(claimsContextKey).(*Claims)
	return claims
}

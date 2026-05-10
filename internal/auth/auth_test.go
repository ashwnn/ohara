package auth

import (
	"context"
	"errors"
	"testing"
)

func TestStaticTokenAuthenticator_ValidToken(t *testing.T) {
	a := NewStaticTokenAuthenticator("my-secret-token")
	claims, err := a.Authenticate("my-secret-token")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if claims.Subject != "static-token" {
		t.Errorf("expected subject 'static-token', got %q", claims.Subject)
	}
	if len(claims.Roles) != 1 || claims.Roles[0] != RoleAdmin {
		t.Errorf("expected [admin] role, got %v", claims.Roles)
	}
	if claims.Token != "my-secret-token" {
		t.Errorf("expected token in claims, got %q", claims.Token)
	}
}

func TestStaticTokenAuthenticator_InvalidToken(t *testing.T) {
	a := NewStaticTokenAuthenticator("real-token")
	_, err := a.Authenticate("wrong-token")
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

func TestStaticTokenAuthenticator_EmptyToken(t *testing.T) {
	a := NewStaticTokenAuthenticator("real-token")
	_, err := a.Authenticate("")
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken for empty token, got %v", err)
	}
}

func TestStaticTokenAuthenticator_TimingSafe(t *testing.T) {
	// Basic verification that different-length tokens don't panic or match.
	a := NewStaticTokenAuthenticator("short")
	_, err := a.Authenticate("a-longer-wrong-token")
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken for different length, got %v", err)
	}
}

func TestClaimsContextRoundTrip(t *testing.T) {
	claims := &Claims{
		Subject: "test-subject",
		Roles:   []Role{RoleAdmin, RoleWrite},
		Token:   "abc123",
	}
	ctx := ContextWithClaims(context.Background(), claims)
	got := ClaimsFromContext(ctx)
	if got == nil {
		t.Fatal("expected non-nil claims from context")
	}
	if got.Subject != "test-subject" {
		t.Errorf("expected subject 'test-subject', got %q", got.Subject)
	}
	if len(got.Roles) != 2 {
		t.Errorf("expected 2 roles, got %d", len(got.Roles))
	}
	if got.Token != "abc123" {
		t.Errorf("expected token 'abc123', got %q", got.Token)
	}
}

func TestHasRole_AdminSatisfiesAll(t *testing.T) {
	c := &Claims{Roles: []Role{RoleAdmin}}
	if !c.HasRole(RoleRead) {
		t.Error("admin should satisfy read")
	}
	if !c.HasRole(RoleWrite) {
		t.Error("admin should satisfy write")
	}
	if !c.HasRole(RoleAdmin) {
		t.Error("admin should satisfy admin")
	}
}

func TestHasRole_WriteSatisfiesRead(t *testing.T) {
	c := &Claims{Roles: []Role{RoleWrite}}
	if !c.HasRole(RoleRead) {
		t.Error("write should satisfy read")
	}
	if !c.HasRole(RoleWrite) {
		t.Error("write should satisfy write")
	}
	if c.HasRole(RoleAdmin) {
		t.Error("write should NOT satisfy admin")
	}
}

func TestHasRole_ReadOnly(t *testing.T) {
	c := &Claims{Roles: []Role{RoleRead}}
	if !c.HasRole(RoleRead) {
		t.Error("read should satisfy read")
	}
	if c.HasRole(RoleWrite) {
		t.Error("read should NOT satisfy write")
	}
	if c.HasRole(RoleAdmin) {
		t.Error("read should NOT satisfy admin")
	}
}

func TestRequireRole_NilClaims(t *testing.T) {
	err := RequireRole(nil, RoleRead)
	if !errors.Is(err, ErrInsufficientRole) {
		t.Fatalf("expected ErrInsufficientRole, got %v", err)
	}
}

func TestRequireRole_EmptyRoles(t *testing.T) {
	err := RequireRole(&Claims{}, RoleRead)
	if !errors.Is(err, ErrInsufficientRole) {
		t.Fatalf("expected ErrInsufficientRole for empty roles, got %v", err)
	}
}

func TestRequireRole_Passes(t *testing.T) {
	err := RequireRole(&Claims{Roles: []Role{RoleAdmin}}, RoleWrite)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestAllProjectsAllowed_Nil(t *testing.T) {
	c := &Claims{AllowedProjects: nil}
	if !c.AllProjectsAllowed() {
		t.Fatal("nil AllowedProjects should mean unrestricted")
	}
}

func TestAllProjectsAllowed_Empty(t *testing.T) {
	c := &Claims{AllowedProjects: []string{}}
	if !c.AllProjectsAllowed() {
		t.Fatal("empty AllowedProjects should mean unrestricted")
	}
}

func TestAllProjectsAllowed_NonEmpty(t *testing.T) {
	c := &Claims{AllowedProjects: []string{"ohara"}}
	if c.AllProjectsAllowed() {
		t.Fatal("non-empty AllowedProjects should NOT mean unrestricted")
	}
}

func TestIsProjectAllowed_AllowsListed(t *testing.T) {
	c := &Claims{AllowedProjects: []string{"alpha", "beta"}}
	if !c.IsProjectAllowed("alpha") {
		t.Fatal("expected 'alpha' to be allowed")
	}
	if !c.IsProjectAllowed("beta") {
		t.Fatal("expected 'beta' to be allowed")
	}
	if c.IsProjectAllowed("gamma") {
		t.Fatal("expected 'gamma' to NOT be allowed")
	}
}

func TestIsProjectAllowed_Unrestricted(t *testing.T) {
	c := &Claims{AllowedProjects: nil}
	if !c.IsProjectAllowed("anything") {
		t.Fatal("unrestricted claims should allow any project")
	}
}

func TestRequireProject_NilClaims(t *testing.T) {
	err := RequireProject(nil, "ohara")
	if err != nil {
		t.Fatalf("nil claims should pass RequireProject (no auth context), got %v", err)
	}
}

func TestRequireProject_Unrestricted(t *testing.T) {
	err := RequireProject(&Claims{}, "anything")
	if err != nil {
		t.Fatalf("empty AllowedProjects should pass any project, got %v", err)
	}
}

func TestRequireProject_Allowed(t *testing.T) {
	c := &Claims{AllowedProjects: []string{"ohara", "memory"}}
	err := RequireProject(c, "memory")
	if err != nil {
		t.Fatalf("expected 'memory' to be allowed, got %v", err)
	}
}

func TestRequireProject_Forbidden(t *testing.T) {
	c := &Claims{AllowedProjects: []string{"alpha"}}
	err := RequireProject(c, "beta")
	if !errors.Is(err, ErrProjectNotAllowed) {
		t.Fatalf("expected ErrProjectNotAllowed for 'beta', got %v", err)
	}
}

func TestClaimsFromContext_NilWhenNoAuth(t *testing.T) {
	claims := ClaimsFromContext(context.Background())
	if claims != nil {
		t.Fatal("expected nil claims from unauthenticated context")
	}
}

func TestClaimsFromContext_WrongType(t *testing.T) {
	ctx := context.WithValue(context.Background(), claimsContextKey, "not-a-claims")
	claims := ClaimsFromContext(ctx)
	if claims != nil {
		t.Fatal("expected nil claims for wrong value type")
	}
}

func TestIsLowTrust_NilClaims(t *testing.T) {
	if (*Claims)(nil).IsLowTrust() {
		t.Fatal("nil claims should NOT be low-trust (backward compat for stdio)")
	}
}

func TestIsLowTrust_RoleReadOnly(t *testing.T) {
	c := &Claims{Roles: []Role{RoleRead}}
	if !c.IsLowTrust() {
		t.Fatal("RoleRead-only should be low-trust")
	}
}

func TestIsLowTrust_RoleWrite(t *testing.T) {
	c := &Claims{Roles: []Role{RoleWrite}}
	if c.IsLowTrust() {
		t.Fatal("RoleWrite should NOT be low-trust")
	}
}

func TestIsLowTrust_RoleAdmin(t *testing.T) {
	c := &Claims{Roles: []Role{RoleAdmin}}
	if c.IsLowTrust() {
		t.Fatal("RoleAdmin should NOT be low-trust")
	}
}

func TestIsLowTrust_MultipleRoles(t *testing.T) {
	c := &Claims{Roles: []Role{RoleRead, RoleWrite}}
	if c.IsLowTrust() {
		t.Fatal("principal with Read+Write should NOT be low-trust")
	}
}

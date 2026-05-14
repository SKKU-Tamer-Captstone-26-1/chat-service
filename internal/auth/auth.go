package auth

import (
	"context"
	"errors"
	"strings"
)

var (
	ErrUnauthenticated = errors.New("unauthenticated")
	ErrInvalidToken    = errors.New("invalid token")
)

type Principal struct {
	UserID          string
	Email           string
	Nickname        string
	ProfileImageURL string
	Role            Role
}

type principalContextKey struct{}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	if !ok || strings.TrimSpace(principal.UserID) == "" {
		return Principal{}, false
	}
	return principal, true
}

type Authenticator interface {
	Authenticate(ctx context.Context, bearerToken string) (Principal, error)
}

type Role int32

const (
	RoleUnspecified Role = 0
	RoleNormal      Role = 1
	RoleAdmin       Role = 2
	RoleBar         Role = 3
	RoleReque       Role = 4
)

func (r Role) String() string {
	switch r {
	case RoleNormal:
		return "ROLE_NORMAL"
	case RoleAdmin:
		return "ROLE_ADMIN"
	case RoleBar:
		return "ROLE_BAR"
	case RoleReque:
		return "ROLE_REQUE"
	default:
		return "ROLE_UNSPECIFIED"
	}
}

func (p Principal) IsAdmin() bool {
	return p.Role == RoleAdmin
}

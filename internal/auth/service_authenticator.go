package auth

import (
	"context"
	"fmt"
	"strings"
)

type TokenClaims struct {
	Valid     bool
	UserID    string
	Email     string
	Role      Role
	Reason    string
	Nickname  string
	ExpiresAt string
}

type UserProfile struct {
	UserID          string
	Email           string
	Nickname        string
	ProfileImageURL string
	Role            Role
}

type AuthServiceClient interface {
	ValidateToken(ctx context.Context, accessToken string) (TokenClaims, error)
	GetMe(ctx context.Context, userID string) (UserProfile, error)
}

type AuthServiceAuthenticator struct {
	client AuthServiceClient
}

func NewAuthServiceAuthenticator(client AuthServiceClient) *AuthServiceAuthenticator {
	return &AuthServiceAuthenticator{client: client}
}

func (a *AuthServiceAuthenticator) Authenticate(ctx context.Context, bearerToken string) (Principal, error) {
	if a == nil || a.client == nil {
		return Principal{}, ErrUnauthenticated
	}
	accessToken := strings.TrimSpace(bearerToken)
	if accessToken == "" {
		return Principal{}, ErrUnauthenticated
	}

	claims, err := a.client.ValidateToken(ctx, accessToken)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: validate token failed: %v", ErrInvalidToken, err)
	}
	if !claims.Valid {
		reason := strings.TrimSpace(claims.Reason)
		if reason == "" {
			reason = "token rejected"
		}
		return Principal{}, fmt.Errorf("%w: %s", ErrInvalidToken, reason)
	}
	if strings.TrimSpace(claims.UserID) == "" {
		return Principal{}, fmt.Errorf("%w: user_id is empty", ErrInvalidToken)
	}

	profile, err := a.client.GetMe(ctx, claims.UserID)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: get self profile failed: %v", ErrInvalidToken, err)
	}
	if strings.TrimSpace(profile.UserID) != "" && profile.UserID != claims.UserID {
		return Principal{}, fmt.Errorf("%w: profile user_id mismatch", ErrInvalidToken)
	}

	principal := Principal{
		UserID:          claims.UserID,
		Email:           firstNonEmpty(profile.Email, claims.Email),
		Nickname:        firstNonEmpty(profile.Nickname, claims.Nickname),
		ProfileImageURL: profile.ProfileImageURL,
		Role:            claims.Role,
	}
	if principal.Role == RoleUnspecified {
		principal.Role = profile.Role
	}
	return principal, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

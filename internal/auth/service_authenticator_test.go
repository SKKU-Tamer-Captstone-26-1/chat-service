package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeAuthServiceClient struct {
	claims  TokenClaims
	profile UserProfile
	err     error
}

func (f fakeAuthServiceClient) ValidateToken(context.Context, string) (TokenClaims, error) {
	if f.err != nil {
		return TokenClaims{}, f.err
	}
	return f.claims, nil
}

func (f fakeAuthServiceClient) GetMe(context.Context, string) (UserProfile, error) {
	if f.err != nil {
		return UserProfile{}, f.err
	}
	return f.profile, nil
}

func TestAuthServiceAuthenticatorBuildsPrincipalWithSelfProfile(t *testing.T) {
	authenticator := NewAuthServiceAuthenticator(fakeAuthServiceClient{
		claims: TokenClaims{
			Valid:    true,
			UserID:   "user-1",
			Email:    "claims@example.com",
			Role:     RoleNormal,
			Nickname: "claims-name",
		},
		profile: UserProfile{
			UserID:          "user-1",
			Email:           "profile@example.com",
			Nickname:        "profile-name",
			ProfileImageURL: "https://cdn.example/avatar.png",
			Role:            RoleNormal,
		},
	})

	principal, err := authenticator.Authenticate(context.Background(), "token")
	if err != nil {
		t.Fatal(err)
	}
	if principal.UserID != "user-1" {
		t.Fatalf("expected user-1, got %q", principal.UserID)
	}
	if principal.Email != "profile@example.com" {
		t.Fatalf("expected profile email, got %q", principal.Email)
	}
	if principal.Nickname != "profile-name" {
		t.Fatalf("expected profile nickname, got %q", principal.Nickname)
	}
	if principal.ProfileImageURL != "https://cdn.example/avatar.png" {
		t.Fatalf("expected profile image url, got %q", principal.ProfileImageURL)
	}
	if principal.Role != RoleNormal {
		t.Fatalf("expected normal role, got %s", principal.Role)
	}
}

func TestAuthServiceAuthenticatorRejectsInvalidToken(t *testing.T) {
	authenticator := NewAuthServiceAuthenticator(fakeAuthServiceClient{
		claims: TokenClaims{Valid: false, Reason: "TOKEN_EXPIRED"},
	})

	_, err := authenticator.Authenticate(context.Background(), "token")
	if err == nil {
		t.Fatal("expected invalid token error")
	}
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
	if !strings.Contains(err.Error(), "TOKEN_EXPIRED") {
		t.Fatalf("expected reason in error, got %v", err)
	}
}

func TestAuthServiceAuthenticatorRejectsProfileMismatch(t *testing.T) {
	authenticator := NewAuthServiceAuthenticator(fakeAuthServiceClient{
		claims:  TokenClaims{Valid: true, UserID: "user-1", Role: RoleNormal},
		profile: UserProfile{UserID: "user-2", Role: RoleNormal},
	})

	_, err := authenticator.Authenticate(context.Background(), "token")
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

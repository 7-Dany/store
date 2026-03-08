package google

import (
	"context"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	goauth "golang.org/x/oauth2/google"
)

// GoogleProvider implements OAuthProvider using the Google OIDC endpoints.
// It is safe for concurrent use after construction.
type GoogleProvider struct {
	cfg      *oauth2.Config
	verifier *oidc.IDTokenVerifier
}

// compile-time interface check.
var _ OAuthProvider = (*GoogleProvider)(nil)

// NewGoogleProvider creates a GoogleProvider.
//
// It calls Google's OIDC discovery endpoint to obtain the verifier; ctx
// is used only for that one-time HTTP request. The returned provider is
// safe for concurrent use once construction succeeds.
func NewGoogleProvider(ctx context.Context, clientID, clientSecret, redirectURI string) (*GoogleProvider, error) {
	provider, err := oidc.NewProvider(ctx, "https://accounts.google.com")
	if err != nil {
		return nil, fmt.Errorf("google.NewGoogleProvider: OIDC discovery: %w", err)
	}

	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURI,
		Endpoint:     goauth.Endpoint,
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: clientID})

	return &GoogleProvider{cfg: cfg, verifier: verifier}, nil
}

// ExchangeCode exchanges the authorization code for a token set using PKCE.
func (p *GoogleProvider) ExchangeCode(ctx context.Context, code, codeVerifier string) (GoogleTokens, error) {
	tok, err := p.cfg.Exchange(ctx, code, oauth2.VerifierOption(codeVerifier))
	if err != nil {
		return GoogleTokens{}, fmt.Errorf("google.ExchangeCode: %w", err)
	}

	rawIDToken, ok := tok.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return GoogleTokens{}, fmt.Errorf("google.ExchangeCode: missing id_token in token response")
	}

	return GoogleTokens{
		IDToken:     rawIDToken,
		AccessToken: tok.AccessToken,
	}, nil
}

// VerifyIDToken verifies the Google OIDC ID token signature and extracts claims.
func (p *GoogleProvider) VerifyIDToken(ctx context.Context, rawIDToken string) (GoogleClaims, error) {
	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return GoogleClaims{}, fmt.Errorf("google.VerifyIDToken: %w", err)
	}

	var payload struct {
		Sub     string `json:"sub"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
	}
	if err := idToken.Claims(&payload); err != nil {
		return GoogleClaims{}, fmt.Errorf("google.VerifyIDToken: extract claims: %w", err)
	}

	return GoogleClaims{
		Sub:     payload.Sub,
		Email:   payload.Email,
		Name:    payload.Name,
		Picture: payload.Picture,
	}, nil
}

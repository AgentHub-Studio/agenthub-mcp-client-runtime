// Pacote oauth implementa o fluxo OAuth 2.1 Client Credentials para o MCP client runtime.
// Gerencia o ciclo de vida dos tokens de acesso, incluindo cache e renovação automática.
package oauth

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// TokenClient manages OAuth 2.1 Client Credentials tokens.
// Tokens are cached and automatically renewed before expiry.
type TokenClient struct {
	source oauth2.TokenSource
}

// NewTokenClient creates a new OAuth token client using the Client Credentials flow.
// scopes may be nil or empty when the authorization server does not require them.
func NewTokenClient(ctx context.Context, tokenURL, clientID, clientSecret string, scopes []string) (*TokenClient, error) {
	if tokenURL == "" {
		return nil, fmt.Errorf("tokenURL é obrigatório")
	}
	if clientID == "" {
		return nil, fmt.Errorf("clientID é obrigatório")
	}

	cfg := &clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     tokenURL,
		Scopes:       scopes,
	}

	// ReuseTokenSource caches the token and auto-renews when it nears expiry.
	source := oauth2.ReuseTokenSource(nil, cfg.TokenSource(ctx))

	return &TokenClient{source: source}, nil
}

// NewAuthCodeTokenClient creates a token client for the Authorization Code flow.
// This assumes the initial token/refresh token are already obtained.
func NewAuthCodeTokenClient(ctx context.Context, tokenURL, clientID, clientSecret, accessToken, refreshToken string) *TokenClient {
	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint: oauth2.Endpoint{
			TokenURL: tokenURL,
		},
	}

	initialToken := &oauth2.Token{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		Expiry:       time.Now().Add(1 * time.Hour), // Assume 1h if unknown, source will refresh if expired
	}

	source := cfg.TokenSource(ctx, initialToken)
	return &TokenClient{source: source}
}

// Token returns a valid access token, fetching or renewing it as needed.
func (c *TokenClient) Token() (string, error) {
	t, err := c.source.Token()
	if err != nil {
		return "", fmt.Errorf("erro ao obter token OAuth: %w", err)
	}
	return t.AccessToken, nil
}

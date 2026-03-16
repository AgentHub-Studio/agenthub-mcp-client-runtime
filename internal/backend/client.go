package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// TokenProvider abstracts the source of bearer tokens used to authenticate
// requests to the agenthub backend. Both static tokens and OAuth2 client
// credentials flows satisfy this interface.
type TokenProvider interface {
	Token() (string, error)
}

// staticToken is a TokenProvider backed by a pre-configured static string.
type staticToken struct{ value string }

func (s *staticToken) Token() (string, error) { return s.value, nil }

// NewStaticTokenProvider returns a TokenProvider that always returns the given token.
// Suitable for development or when an external process manages token rotation.
func NewStaticTokenProvider(token string) TokenProvider {
	return &staticToken{value: token}
}

// ServerConfig represents an MCP server configuration loaded from the backend.
type ServerConfig struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	TransportType     string            `json:"transportType"`
	HTTPBaseURL       string            `json:"httpBaseUrl"`
	Command           string            `json:"command"`
	Args              []string          `json:"args"`
	Env               map[string]string `json:"env"`
	OAuthTokenURL     string            `json:"oauthTokenUrl"`
	OAuthClientID     string            `json:"oauthClientId"`
	OAuthClientSecret string            `json:"oauthClientSecret"`
	OAuthScopes       []string          `json:"oauthScopes"`
	AutoStart         bool              `json:"autoStart"`
	Enabled           bool              `json:"enabled"`
}

// BackendClient fetches MCP server configurations from the agenthub backend.
//
// Authentication: the backend determines the tenant exclusively from the JWT
// iss claim (Keycloak realm = tenant UUID). The TokenProvider must supply a
// token issued by the tenant's Keycloak realm.
type BackendClient struct {
	baseURL       string
	tokenProvider TokenProvider
	httpClient    *http.Client
}

// NewBackendClient creates a BackendClient that authenticates via a TokenProvider.
func NewBackendClient(baseURL string, tokenProvider TokenProvider) *BackendClient {
	return &BackendClient{
		baseURL:       baseURL,
		tokenProvider: tokenProvider,
		httpClient:    &http.Client{Timeout: 10 * time.Second},
	}
}

// NewBackendClientWithStaticToken creates a BackendClient using a fixed bearer token.
// Suitable for development or when an external tool manages token rotation.
func NewBackendClientWithStaticToken(baseURL, token string) *BackendClient {
	return NewBackendClient(baseURL, &staticToken{value: token})
}

// ListConfigs loads all MCP server configurations from GET /api/mcp-server-configs/bootstrap.
//
// The bootstrap endpoint includes oauthClientSecret (required to configure OAuth for MCP
// servers) and is restricted to callers with the mcp-client-runtime role.
// The backend routes the request to the correct tenant schema based on the
// JWT iss claim — no additional tenant header is required or sent.
func (c *BackendClient) ListConfigs(ctx context.Context) ([]ServerConfig, error) {
	url := fmt.Sprintf("%s/api/mcp-server-configs/bootstrap", c.baseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	if c.tokenProvider != nil {
		token, err := c.tokenProvider.Token()
		if err != nil {
			return nil, fmt.Errorf("obtaining bearer token: %w", err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("backend returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var configs []ServerConfig
	if err := json.NewDecoder(resp.Body).Decode(&configs); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return configs, nil
}

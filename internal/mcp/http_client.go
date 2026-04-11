package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agenthub/mcp-client-runtime/internal/oauth"
)

// HTTPClient implements the MCP client over Streamable HTTP transport (spec 2025-03-26).
// Sends JSON-RPC requests via POST /mcp with Accept: text/event-stream
// and parses SSE responses from the server.
type HTTPClient struct {
	config     ClientConfig
	baseURL    string
	oauth      OAuthTokenProvider
	httpClient *http.Client

	mu                        sync.RWMutex
	serverInfo                *ServerInfo
	isRunning                 bool
	startedAt                 time.Time
	authMetadata              AuthMetadata
	oauthProvider             OAuthTokenProvider
	dynamicClientID           string
	sessionID                 string
	negotiatedProtocolVersion string

	nextID atomic.Int64
}

// NewHTTPClient creates a new MCP client using Streamable HTTP transport.
// oauth may be nil for servers that do not require authentication.
func NewHTTPClient(config ClientConfig, oauth OAuthTokenProvider) *HTTPClient {
	return &HTTPClient{
		config:     config,
		baseURL:    strings.TrimSuffix(config.HTTPBaseURL, "/"),
		oauth:      oauth,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Start performs the MCP initialize handshake and marks the client as running.
func (c *HTTPClient) Start(ctx context.Context) error {
	c.mu.RLock()
	running := c.isRunning
	c.mu.RUnlock()

	if running {
		return fmt.Errorf("client already running")
	}

	// NOTE: initialize calls sendRequest which may need to acquire c.mu
	// on 401 responses to store auth metadata. We must NOT hold the lock here.
	if err := c.initialize(ctx); err != nil {
		return fmt.Errorf("failed to initialize MCP HTTP connection: %w", err)
	}

	c.mu.Lock()
	c.isRunning = true
	c.startedAt = time.Now()
	c.mu.Unlock()
	return nil
}

// SetDynamicClientID sets the client ID obtained from DCR.
func (c *HTTPClient) SetDynamicClientID(clientID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dynamicClientID = clientID
}

// Stop marks the client as stopped. No network call is required for HTTP transport.
func (c *HTTPClient) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.isRunning = false
	return nil
}

// IsRunning reports whether the client has been successfully started.
func (c *HTTPClient) IsRunning() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.isRunning
}

// GetServerInfo returns the server info received during the initialize handshake.
func (c *HTTPClient) GetServerInfo() *ServerInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serverInfo
}

// GetConfig returns the configuration used to create this client.
func (c *HTTPClient) GetConfig() ClientConfig {
	return c.config
}

// initialize performs the MCP handshake via POST /mcp.
func (c *HTTPClient) initialize(ctx context.Context) error {
	params := InitializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    map[string]interface{}{},
		ClientInfo: ClientInfo{
			Name:    "agenthub-mcp-client",
			Version: "1.0.0",
		},
	}

	response, headers, err := c.sendRequestWithHeaders(ctx, "initialize", params)
	if err != nil {
		return fmt.Errorf("initialize request failed: %w", err)
	}
	if response.Error != nil {
		return fmt.Errorf("initialize error: %s", response.Error.Message)
	}

	var result InitializeResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return fmt.Errorf("failed to parse initialize result: %w", err)
	}

	c.mu.Lock()
	c.serverInfo = &result.ServerInfo
	c.sessionID = headers.Get("Mcp-Session-Id")
	if result.ProtocolVersion != "" {
		c.negotiatedProtocolVersion = result.ProtocolVersion
	} else {
		c.negotiatedProtocolVersion = ProtocolVersion
	}
	c.mu.Unlock()

	// Send initialized notification (fire-and-forget)
	_ = c.sendNotification(ctx, "notifications/initialized", nil)
	return nil
}

// sendRequest sends a JSON-RPC request via POST /mcp with Accept: text/event-stream
// and returns the first JSON-RPC response from the SSE stream (or JSON fallback).
func (c *HTTPClient) sendRequest(ctx context.Context, method string, params interface{}) (*JSONRPCResponse, error) {
	response, _, err := c.sendRequestWithHeaders(ctx, method, params)
	return response, err
}

func (c *HTTPClient) sendRequestWithHeaders(ctx context.Context, method string, params interface{}) (*JSONRPCResponse, http.Header, error) {
	id := c.nextID.Add(1)

	reqMsg, err := NewJSONRPCRequest(id, method, params)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build JSON-RPC request: %w", err)
	}

	body, err := json.Marshal(reqMsg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	c.setProtocolHeaders(httpReq, method)
	c.setAuthHeader(httpReq)

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer httpResp.Body.Close()
	respHeaders := httpResp.Header.Clone()

	switch httpResp.StatusCode {
	case http.StatusUnauthorized:
		// MCP Security Spec: Discover OAuth metadata from WWW-Authenticate header.
		// Example: WWW-Authenticate: Bearer resource_metadata="https://example.com/.well-known/oauth-protected-resource"
		authHeader := httpResp.Header.Get("WWW-Authenticate")
		metadata := AuthMetadata{}
		if strings.Contains(authHeader, "resource_metadata=") {
			// Basic parsing of resource_metadata="URL"
			parts := strings.Split(authHeader, "resource_metadata=\"")
			if len(parts) > 1 {
				urlParts := strings.Split(parts[1], "\"")
				metadata.ResourceMetadataURL = urlParts[0]
			}
		}

		// Fallback: Check well-known URL if not in header.
		if metadata.ResourceMetadataURL == "" {
			if parsed, err := url.Parse(c.baseURL); err == nil {
				metadata.ResourceMetadataURL = parsed.Scheme + "://" + parsed.Host + "/.well-known/oauth-protected-resource"
			}
		}

		// Active Discovery: If we have a metadata URL, fetch it.
		if metadata.ResourceMetadataURL != "" {
			_ = c.fetchResourceMetadata(ctx, &metadata)
		}

		// Always persist discovered metadata so GetAuthMetadata() can return it
		c.mu.Lock()
		c.authMetadata = metadata
		c.mu.Unlock()

		if c.config.OnAuthRequired != nil {
			go c.config.OnAuthRequired(metadata)
		}

		return nil, respHeaders, fmt.Errorf("authentication failed (401) — OAuth access token invalid or expired. Metadata: %+v", metadata)
	case http.StatusForbidden:
		return nil, respHeaders, fmt.Errorf("access forbidden (403) — insufficient scopes or permissions")
	case http.StatusNoContent:
		return nil, respHeaders, fmt.Errorf("server returned 204 for a request expecting a response")
	}
	if httpResp.StatusCode >= 400 {
		return nil, respHeaders, fmt.Errorf("server returned HTTP %d%s", httpResp.StatusCode, readErrorSuffix(httpResp.Body))
	}

	ct := httpResp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/event-stream") {
		response, err := parseSSEResponse(httpResp)
		return response, respHeaders, err
	}

	// Fallback: plain JSON response
	var jsonResp JSONRPCResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&jsonResp); err != nil {
		return nil, respHeaders, fmt.Errorf("failed to parse JSON response: %w", err)
	}
	return &jsonResp, respHeaders, nil
}

// parseSSEResponse reads the SSE stream and returns the first data event as a JSONRPCResponse.
func parseSSEResponse(resp *http.Response) (*JSONRPCResponse, error) {
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var jsonResp JSONRPCResponse
		if err := json.Unmarshal([]byte(data), &jsonResp); err != nil {
			return nil, fmt.Errorf("failed to parse SSE data as JSON-RPC: %w", err)
		}
		// Some MCP servers emit intermediary SSE events that are not the terminal
		// JSON-RPC response for the request. Ignore payloads that carry neither
		// result nor error and keep scanning until the actual response arrives.
		if len(jsonResp.Result) == 0 && jsonResp.Error == nil {
			continue
		}
		return &jsonResp, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading SSE stream: %w", err)
	}
	return nil, fmt.Errorf("SSE stream ended without a data event")
}

// sendNotification sends a JSON-RPC notification (no response expected).
func (c *HTTPClient) sendNotification(ctx context.Context, method string, params interface{}) error {
	reqMsg, err := NewJSONRPCRequest(nil, method, params)
	if err != nil {
		return err
	}

	body, err := json.Marshal(reqMsg)
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	c.setProtocolHeaders(httpReq, method)
	c.setAuthHeader(httpReq)

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode == http.StatusAccepted || httpResp.StatusCode == http.StatusNoContent || httpResp.StatusCode == http.StatusOK {
		return nil
	}
	if httpResp.StatusCode >= 400 {
		return fmt.Errorf("notification %s failed with HTTP %d%s", method, httpResp.StatusCode, readErrorSuffix(httpResp.Body))
	}
	return nil
}

// GetAuthMetadata returns the discovered OAuth metadata.
func (c *HTTPClient) GetAuthMetadata() *AuthMetadata {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.authMetadata.ResourceMetadataURL == "" && c.authMetadata.AuthorizationURL == "" {
		return nil
	}
	copy := c.authMetadata
	if c.dynamicClientID != "" {
		copy.ClientID = c.dynamicClientID
	}
	return &copy
}

// fetchResourceMetadata fetches and parses the OAuth Protected Resource metadata.
func (c *HTTPClient) fetchResourceMetadata(ctx context.Context, metadata *AuthMetadata) error {
	req, err := http.NewRequestWithContext(ctx, "GET", metadata.ResourceMetadataURL, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("resource metadata endpoint returned %d", resp.StatusCode)
	}

	var data struct {
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return err
	}

	if len(data.AuthorizationServers) > 0 {
		metadata.Issuer = data.AuthorizationServers[0]
		// Discover authorization server metadata (RFC 8414)
		_ = c.fetchAuthServerMetadata(ctx, metadata)
	}

	return nil
}

// fetchAuthServerMetadata discovers authorization server endpoints using RFC 8414.
func (c *HTTPClient) fetchAuthServerMetadata(ctx context.Context, metadata *AuthMetadata) error {
	discoveryURL := strings.TrimSuffix(metadata.Issuer, "/") + "/.well-known/oauth-authorization-server"
	// Also try OpenID Connect discovery fallback
	if strings.Contains(metadata.Issuer, "auth.atlassian.com") {
		discoveryURL = metadata.Issuer + "/.well-known/openid-configuration"
	}

	req, err := http.NewRequestWithContext(ctx, "GET", discoveryURL, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("auth server metadata endpoint returned %d", resp.StatusCode)
	}

	var data struct {
		AuthorizationEndpoint string   `json:"authorization_endpoint"`
		TokenEndpoint         string   `json:"token_endpoint"`
		RegistrationEndpoint  string   `json:"registration_endpoint"`
		ScopesSupported       []string `json:"scopes_supported"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return err
	}

	metadata.AuthorizationURL = data.AuthorizationEndpoint
	metadata.TokenURL = data.TokenEndpoint
	metadata.RegistrationURL = data.RegistrationEndpoint
	metadata.ScopesSupported = data.ScopesSupported

	// If we have a registration endpoint and no client ID, try DCR!
	if metadata.RegistrationURL != "" && c.dynamicClientID == "" {
		c.tryDynamicRegistration(ctx, metadata)
	}

	return nil
}

func (c *HTTPClient) tryDynamicRegistration(ctx context.Context, metadata *AuthMetadata) {
	req := oauth.DynamicClientRegistrationRequest{
		ClientName:    "AgentHub MCP Client",
		RedirectURIs:  []string{"https://api.cezar.dev/api/mcp/callback"}, // TODO: Make configurable
		GrantTypes:    []string{"authorization_code", "refresh_token"},
		ResponseTypes: []string{"code"},
	}

	resp, err := oauth.RegisterDynamicClient(ctx, metadata.RegistrationURL, req)
	if err == nil && resp.ClientID != "" {
		c.mu.Lock()
		c.dynamicClientID = resp.ClientID
		c.mu.Unlock()
	}
}

// setAuthHeader adds the Bearer token to the request when an OAuth provider is configured.
func (c *HTTPClient) setAuthHeader(req *http.Request) {
	if c.oauth == nil {
		return
	}
	token, err := c.oauth.Token()
	if err == nil && token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func (c *HTTPClient) setProtocolHeaders(req *http.Request, method string) {
	c.mu.RLock()
	sessionID := c.sessionID
	protocolVersion := c.negotiatedProtocolVersion
	c.mu.RUnlock()

	if protocolVersion == "" {
		protocolVersion = ProtocolVersion
	}
	req.Header.Set("MCP-Protocol-Version", protocolVersion)

	if method != "initialize" && sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
}

func readErrorSuffix(body io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(body, 2048))
	if err != nil {
		return ""
	}
	msg := strings.TrimSpace(string(data))
	if msg == "" {
		return ""
	}
	return fmt.Sprintf(": %s", msg)
}

// ========== MCP methods ==========

// ListTools lists all available tools from the remote MCP server.
func (c *HTTPClient) ListTools(ctx context.Context) (*ListToolsResult, error) {
	if !c.IsRunning() {
		return nil, fmt.Errorf("client not running")
	}
	resp, err := c.sendRequest(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("tools/list error: %s", resp.Error.Message)
	}
	var result ListToolsResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse tools/list result: %w", err)
	}
	return &result, nil
}

// CallTool executes a tool on the remote MCP server.
func (c *HTTPClient) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*CallToolResult, error) {
	if !c.IsRunning() {
		return nil, fmt.Errorf("client not running")
	}
	resp, err := c.sendRequest(ctx, "tools/call", CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("tools/call error: %s", resp.Error.Message)
	}
	var result CallToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse tools/call result: %w", err)
	}
	return &result, nil
}

// ListPrompts lists all available prompts from the remote MCP server.
func (c *HTTPClient) ListPrompts(ctx context.Context) (*ListPromptsResult, error) {
	if !c.IsRunning() {
		return nil, fmt.Errorf("client not running")
	}
	resp, err := c.sendRequest(ctx, "prompts/list", nil)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("prompts/list error: %s", resp.Error.Message)
	}
	var result ListPromptsResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse prompts/list result: %w", err)
	}
	return &result, nil
}

// GetPrompt retrieves a rendered prompt from the remote MCP server.
func (c *HTTPClient) GetPrompt(ctx context.Context, name string, arguments map[string]interface{}) (*GetPromptResult, error) {
	if !c.IsRunning() {
		return nil, fmt.Errorf("client not running")
	}
	resp, err := c.sendRequest(ctx, "prompts/get", GetPromptParams{Name: name, Arguments: arguments})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("prompts/get error: %s", resp.Error.Message)
	}
	var result GetPromptResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse prompts/get result: %w", err)
	}
	return &result, nil
}

// ListResources lists all available resources from the remote MCP server.
func (c *HTTPClient) ListResources(ctx context.Context) (*ListResourcesResult, error) {
	if !c.IsRunning() {
		return nil, fmt.Errorf("client not running")
	}
	resp, err := c.sendRequest(ctx, "resources/list", nil)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("resources/list error: %s", resp.Error.Message)
	}
	var result ListResourcesResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse resources/list result: %w", err)
	}
	return &result, nil
}

// ReadResource reads a resource by URI from the remote MCP server.
func (c *HTTPClient) ReadResource(ctx context.Context, uri string) (*ReadResourceResult, error) {
	if !c.IsRunning() {
		return nil, fmt.Errorf("client not running")
	}
	resp, err := c.sendRequest(ctx, "resources/read", ReadResourceParams{URI: uri})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("resources/read error: %s", resp.Error.Message)
	}
	var result ReadResourceResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse resources/read result: %w", err)
	}
	return &result, nil
}

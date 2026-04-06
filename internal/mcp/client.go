package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// Client represents an MCP client connected to an external MCP server
type Client struct {
	name       string
	command    string
	args       []string
	env        []string
	
	cmd        *exec.Cmd
	protocol   *Protocol
	serverInfo *ServerInfo
	
	mu         sync.RWMutex
	isRunning  bool
	startedAt  time.Time
}

// ClientConfig holds configuration for creating an MCP client.
type ClientConfig struct {
	Name string

	// Stdio transport fields (TransportType = "stdio", default)
	Command string
	Args    []string
	Env     []string

	// HTTP transport fields (TransportType = "http")
	TransportType string // "stdio" (default) or "http"
	HTTPBaseURL   string

	// Optional OAuth provider for the HTTP transport
	OAuthProvider OAuthTokenProvider

	// OnAuthRequired is called when the server requires authentication.
	// It provides the discovered metadata (e.g. authURL, tokenURL).
	OnAuthRequired func(metadata AuthMetadata)
}

// AuthMetadata contains discovered OAuth2 metadata from the MCP server.
type AuthMetadata struct {
	ResourceMetadataURL string   `json:"resource_metadata_url,omitempty"`
	AuthorizationURL    string   `json:"authorization_url,omitempty"`
	TokenURL            string   `json:"token_url,omitempty"`
	RegistrationURL     string   `json:"registration_url,omitempty"`
	Issuer              string   `json:"issuer,omitempty"`
	ScopesSupported     []string `json:"scopes_supported,omitempty"`
	ClientID            string   `json:"client_id,omitempty"`
}

// NewClient creates a new MCP client
func NewClient(config ClientConfig) *Client {
	return &Client{
		name:    config.Name,
		command: config.Command,
		args:    config.Args,
		env:     config.Env,
	}
}

// Start starts the MCP server process and initializes the connection
func (c *Client) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isRunning {
		return fmt.Errorf("client already running")
	}

	// Create command
	c.cmd = exec.CommandContext(ctx, c.command, c.args...)
	c.cmd.Env = append(c.cmd.Env, c.env...)

	// Setup stdio pipes
	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := c.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start process
	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start process: %w", err)
	}

	c.isRunning = true
	c.startedAt = time.Now()

	// Create protocol handler
	c.protocol = NewProtocol(stdout, stdin)

	// Start reading messages in background
	go func() {
		if err := c.protocol.ReadMessages(); err != nil {
			fmt.Printf("Error reading messages from %s: %v\n", c.name, err)
		}
	}()

	// Log stderr in background
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				fmt.Printf("[%s stderr] %s", c.name, string(buf[:n]))
			}
			if err != nil {
				break
			}
		}
	}()

	// Initialize MCP connection
	if err := c.initialize(ctx); err != nil {
		c.Stop()
		return fmt.Errorf("failed to initialize MCP connection: %w", err)
	}

	return nil
}

// Stop stops the MCP server process
func (c *Client) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRunning {
		return nil
	}

	c.isRunning = false

	if c.cmd != nil && c.cmd.Process != nil {
		if err := c.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill process: %w", err)
		}
	}

	return nil
}

// IsRunning returns whether the client is currently running
func (c *Client) IsRunning() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.isRunning
}

// GetConfig returns the configuration used to create this client.
func (c *Client) GetConfig() ClientConfig {
	return ClientConfig{
		Name:          c.name,
		Command:       c.command,
		Args:          c.args,
		Env:           c.env,
		TransportType: "stdio",
	}
}

// GetServerInfo returns information about the connected MCP server
func (c *Client) GetServerInfo() *ServerInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serverInfo
}

// initialize performs MCP initialization handshake
func (c *Client) initialize(ctx context.Context) error {
	params := InitializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    map[string]interface{}{},
		ClientInfo: ClientInfo{
			Name:    "agenthub-mcp-client",
			Version: "1.0.0",
		},
	}

	response, err := c.protocol.SendRequest("initialize", params)
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

	c.serverInfo = &result.ServerInfo

	// Send initialized notification
	if err := c.protocol.SendNotification("notifications/initialized", nil); err != nil {
		return fmt.Errorf("failed to send initialized notification: %w", err)
	}

	return nil
}

// ListTools lists all available tools from the MCP server
func (c *Client) ListTools(ctx context.Context) (*ListToolsResult, error) {
	if !c.IsRunning() {
		return nil, fmt.Errorf("client not running")
	}

	response, err := c.protocol.SendRequest("tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("tools/list request failed: %w", err)
	}

	if response.Error != nil {
		return nil, fmt.Errorf("tools/list error: %s", response.Error.Message)
	}

	var result ListToolsResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse tools/list result: %w", err)
	}

	return &result, nil
}

// CallTool executes a tool on the MCP server
func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*CallToolResult, error) {
	if !c.IsRunning() {
		return nil, fmt.Errorf("client not running")
	}

	params := CallToolParams{
		Name:      name,
		Arguments: arguments,
	}

	response, err := c.protocol.SendRequest("tools/call", params)
	if err != nil {
		return nil, fmt.Errorf("tools/call request failed: %w", err)
	}

	if response.Error != nil {
		return nil, fmt.Errorf("tools/call error: %s", response.Error.Message)
	}

	var result CallToolResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse tools/call result: %w", err)
	}

	return &result, nil
}

// ListPrompts lists all available prompts from the MCP server
func (c *Client) ListPrompts(ctx context.Context) (*ListPromptsResult, error) {
	if !c.IsRunning() {
		return nil, fmt.Errorf("client not running")
	}

	response, err := c.protocol.SendRequest("prompts/list", nil)
	if err != nil {
		return nil, fmt.Errorf("prompts/list request failed: %w", err)
	}

	if response.Error != nil {
		return nil, fmt.Errorf("prompts/list error: %s", response.Error.Message)
	}

	var result ListPromptsResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse prompts/list result: %w", err)
	}

	return &result, nil
}

// GetPrompt retrieves a prompt from the MCP server
func (c *Client) GetPrompt(ctx context.Context, name string, arguments map[string]interface{}) (*GetPromptResult, error) {
	if !c.IsRunning() {
		return nil, fmt.Errorf("client not running")
	}

	params := GetPromptParams{
		Name:      name,
		Arguments: arguments,
	}

	response, err := c.protocol.SendRequest("prompts/get", params)
	if err != nil {
		return nil, fmt.Errorf("prompts/get request failed: %w", err)
	}

	if response.Error != nil {
		return nil, fmt.Errorf("prompts/get error: %s", response.Error.Message)
	}

	var result GetPromptResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse prompts/get result: %w", err)
	}

	return &result, nil
}

// ListResources lists all available resources from the MCP server
func (c *Client) ListResources(ctx context.Context) (*ListResourcesResult, error) {
	if !c.IsRunning() {
		return nil, fmt.Errorf("client not running")
	}

	response, err := c.protocol.SendRequest("resources/list", nil)
	if err != nil {
		return nil, fmt.Errorf("resources/list request failed: %w", err)
	}

	if response.Error != nil {
		return nil, fmt.Errorf("resources/list error: %s", response.Error.Message)
	}

	var result ListResourcesResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse resources/list result: %w", err)
	}

	return &result, nil
}

// ReadResource reads a resource from the MCP server
func (c *Client) ReadResource(ctx context.Context, uri string) (*ReadResourceResult, error) {
	if !c.IsRunning() {
		return nil, fmt.Errorf("client not running")
	}

	params := ReadResourceParams{
		URI: uri,
	}

	response, err := c.protocol.SendRequest("resources/read", params)
	if err != nil {
		return nil, fmt.Errorf("resources/read request failed: %w", err)
	}

	if response.Error != nil {
		return nil, fmt.Errorf("resources/read error: %s", response.Error.Message)
	}

	var result ReadResourceResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse resources/read result: %w", err)
	}

	return &result, nil
}

package mcp

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ClientIface is the common interface implemented by both stdio and HTTP MCP clients.
type ClientIface interface {
	Start(ctx context.Context) error
	Stop() error
	IsRunning() bool
	GetServerInfo() *ServerInfo
	GetConfig() ClientConfig
	ListTools(ctx context.Context) (*ListToolsResult, error)
	CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*CallToolResult, error)
	ListPrompts(ctx context.Context) (*ListPromptsResult, error)
	GetPrompt(ctx context.Context, name string, arguments map[string]interface{}) (*GetPromptResult, error)
	ListResources(ctx context.Context) (*ListResourcesResult, error)
	ReadResource(ctx context.Context, uri string) (*ReadResourceResult, error)
}

// Manager manages multiple MCP clients (stdio or HTTP transport).
type Manager struct {
	clients map[string]ClientIface
	mu      sync.RWMutex
}

// NewManager creates a new MCP client manager.
func NewManager() *Manager {
	return &Manager{
		clients: make(map[string]ClientIface),
	}
}

// RegisterServer registers a new MCP server.
// Creates an HTTPClient when config.TransportType == "http", otherwise a stdio Client.
func (m *Manager) RegisterServer(config ClientConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.clients[config.Name]; exists {
		return fmt.Errorf("server %s already registered", config.Name)
	}

	var client ClientIface
	if config.TransportType == "http" {
		if config.HTTPBaseURL == "" {
			return fmt.Errorf("HTTPBaseURL is required for HTTP transport")
		}
		client = NewHTTPClient(config, config.OAuthProvider)
	} else {
		client = NewClient(config)
	}

	m.clients[config.Name] = client
	return nil
}

// StartServer starts an MCP server by name.
func (m *Manager) StartServer(ctx context.Context, name string) error {
	m.mu.RLock()
	client, exists := m.clients[name]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("server %s not found", name)
	}

	return client.Start(ctx)
}

// StopServer stops an MCP server by name.
func (m *Manager) StopServer(name string) error {
	m.mu.RLock()
	client, exists := m.clients[name]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("server %s not found", name)
	}

	return client.Stop()
}

// GetClient returns an MCP client by name.
func (m *Manager) GetClient(name string) (ClientIface, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	client, exists := m.clients[name]
	if !exists {
		return nil, fmt.Errorf("server %s not found", name)
	}

	return client, nil
}

// ListServers returns the status of all registered servers.
func (m *Manager) ListServers() []ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	servers := make([]ServerStatus, 0, len(m.clients))
	for name, client := range m.clients {
		status := "stopped"
		if client.IsRunning() {
			status = "running"
		}

		var serverInfo *ServerInfo
		if client.IsRunning() {
			serverInfo = client.GetServerInfo()
		}

 	cfg := client.GetConfig()
		statusObj := ServerStatus{
			Name:          name,
			TransportType: cfg.TransportType,
			Command:       cfg.Command,
			Args:          cfg.Args,
			HTTPBaseURL:   cfg.HTTPBaseURL,
			Status:        status,
			ServerInfo:    serverInfo,
		}

		// Try to get auth metadata if HTTP client
		if httpClient, ok := client.(*HTTPClient); ok {
			statusObj.AuthMetadata = httpClient.GetAuthMetadata()
		}

		servers = append(servers, statusObj)
	}

	return servers
}

// StopAll stops all running servers.
func (m *Manager) StopAll() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var lastErr error
	for name, client := range m.clients {
		if client.IsRunning() {
			if err := client.Stop(); err != nil {
				lastErr = fmt.Errorf("failed to stop %s: %w", name, err)
			}
		}
	}

	return lastErr
}

// ServerStatus represents the status of a registered MCP server.
type ServerStatus struct {
	Name          string
	TransportType string
	Command       string
	Args          []string
	HTTPBaseURL   string
	Status        string
	ServerInfo    *ServerInfo
	StartedAt     time.Time
	AuthMetadata  *AuthMetadata
}

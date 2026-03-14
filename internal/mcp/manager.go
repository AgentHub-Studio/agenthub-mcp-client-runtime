package mcp

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Manager manages multiple MCP clients
type Manager struct {
	clients map[string]*Client
	mu      sync.RWMutex
}

// NewManager creates a new MCP client manager
func NewManager() *Manager {
	return &Manager{
		clients: make(map[string]*Client),
	}
}

// RegisterServer registers a new MCP server
func (m *Manager) RegisterServer(config ClientConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.clients[config.Name]; exists {
		return fmt.Errorf("server %s already registered", config.Name)
	}

	client := NewClient(config)
	m.clients[config.Name] = client

	return nil
}

// StartServer starts an MCP server
func (m *Manager) StartServer(ctx context.Context, name string) error {
	m.mu.RLock()
	client, exists := m.clients[name]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("server %s not found", name)
	}

	return client.Start(ctx)
}

// StopServer stops an MCP server
func (m *Manager) StopServer(name string) error {
	m.mu.RLock()
	client, exists := m.clients[name]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("server %s not found", name)
	}

	return client.Stop()
}

// GetClient returns an MCP client by name
func (m *Manager) GetClient(name string) (*Client, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	client, exists := m.clients[name]
	if !exists {
		return nil, fmt.Errorf("server %s not found", name)
	}

	return client, nil
}

// ListServers returns all registered servers
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

		servers = append(servers, ServerStatus{
			Name:       name,
			Command:    client.command,
			Args:       client.args,
			Status:     status,
			ServerInfo: serverInfo,
			StartedAt:  client.startedAt,
		})
	}

	return servers
}

// StopAll stops all running servers
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

// ServerStatus represents the status of an MCP server
type ServerStatus struct {
	Name       string
	Command    string
	Args       []string
	Status     string
	ServerInfo *ServerInfo
	StartedAt  time.Time
}

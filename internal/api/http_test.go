package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/agenthub/mcp-client-runtime/internal/mcp"
)

func TestHTTPServer_ListTools_ReturnsUnauthorizedOnAuthError(t *testing.T) {
	manager := mcp.NewManager()
	setManagerClients(manager, map[string]mcp.ClientIface{
		"github": &stubClient{
			config:       mcp.ClientConfig{Name: "github", TransportType: "http", HTTPBaseURL: "https://example.com/mcp"},
			listToolsErr: errAuthFailure,
		},
	})

	server := NewHTTPServer(0, manager)
	req := httptest.NewRequest(http.MethodGet, "/servers/github/tools", nil)
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status: got %d want %d", w.Code, http.StatusUnauthorized)
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["auth_required"] != true {
		t.Fatalf("expected auth_required=true, got %#v", body["auth_required"])
	}
	errorMessage, _ := body["error"].(string)
	if errorMessage == "" || errorMessage != errAuthFailure.Error() {
		t.Fatalf("unexpected error message: %q", errorMessage)
	}
}

func TestHTTPServer_GetServerStatusRoute(t *testing.T) {
	manager := mcp.NewManager()
	setManagerClients(manager, map[string]mcp.ClientIface{
		"github": &stubClient{
			running: true,
			config:  mcp.ClientConfig{Name: "github", TransportType: "http", HTTPBaseURL: "https://example.com/mcp"},
		},
	})

	server := NewHTTPServer(0, manager)
	req := httptest.NewRequest(http.MethodGet, "/servers/github/status", nil)
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), `"Name":"github"`) {
		t.Fatalf("response does not include server name: %s", w.Body.String())
	}
}

func TestHTTPServer_ExecuteToolRoute(t *testing.T) {
	manager := mcp.NewManager()
	setManagerClients(manager, map[string]mcp.ClientIface{
		"github": &stubClient{
			running: true,
			config:  mcp.ClientConfig{Name: "github", TransportType: "http", HTTPBaseURL: "https://example.com/mcp"},
		},
	})

	server := NewHTTPServer(0, manager)
	req := httptest.NewRequest(http.MethodPost, "/servers/github/tools/list_repositories/call", strings.NewReader(`{"arguments":{"owner":"cezar"}}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"isError":false`) {
		t.Fatalf("response does not include execution payload: %s", w.Body.String())
	}
}

var errAuthFailure = &authError{message: "authentication failed (401) — OAuth access token invalid or expired"}

type authError struct {
	message string
}

func (e *authError) Error() string { return e.message }

type stubClient struct {
	running          bool
	config           mcp.ClientConfig
	serverInfo       *mcp.ServerInfo
	listToolsErr     error
	listPromptsErr   error
	listResourcesErr error
}

func (c *stubClient) Start(context.Context) error {
	c.running = true
	return nil
}

func (c *stubClient) Stop() error {
	c.running = false
	return nil
}

func (c *stubClient) IsRunning() bool {
	return c.running
}

func (c *stubClient) GetServerInfo() *mcp.ServerInfo {
	if c.serverInfo != nil {
		return c.serverInfo
	}
	return &mcp.ServerInfo{Name: c.config.Name, Version: "1.0.0"}
}

func (c *stubClient) GetConfig() mcp.ClientConfig {
	return c.config
}

func (c *stubClient) ListTools(context.Context) (*mcp.ListToolsResult, error) {
	if c.listToolsErr != nil {
		return nil, c.listToolsErr
	}
	return &mcp.ListToolsResult{}, nil
}

func (c *stubClient) CallTool(context.Context, string, map[string]interface{}) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{}, nil
}

func (c *stubClient) ListPrompts(context.Context) (*mcp.ListPromptsResult, error) {
	if c.listPromptsErr != nil {
		return nil, c.listPromptsErr
	}
	return &mcp.ListPromptsResult{}, nil
}

func (c *stubClient) GetPrompt(context.Context, string, map[string]interface{}) (*mcp.GetPromptResult, error) {
	return &mcp.GetPromptResult{}, nil
}

func (c *stubClient) ListResources(context.Context) (*mcp.ListResourcesResult, error) {
	if c.listResourcesErr != nil {
		return nil, c.listResourcesErr
	}
	return &mcp.ListResourcesResult{}, nil
}

func (c *stubClient) ReadResource(context.Context, string) (*mcp.ReadResourceResult, error) {
	return &mcp.ReadResourceResult{}, nil
}

func setManagerClients(manager *mcp.Manager, clients map[string]mcp.ClientIface) {
	field := reflect.ValueOf(manager).Elem().FieldByName("clients")
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf(clients))
}

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeOAuth is a test double for OAuthTokenProvider.
type fakeOAuth struct {
	token string
	err   error
}

func (f *fakeOAuth) Token() (string, error) {
	return f.token, f.err
}

// buildMCPServer creates a test HTTP server that acts as a minimal MCP server.
// It handles initialize and any subsequent method by returning a generic success response.
func buildMCPServer(t *testing.T, capturedHeaders *http.Header) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capturedHeaders != nil {
			for k, v := range r.Header {
				capturedHeaders.Set(k, v[0])
			}
		}

		var req JSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		var result interface{}
		switch req.Method {
		case "initialize":
			result = InitializeResult{
				ProtocolVersion: ProtocolVersion,
				ServerInfo: ServerInfo{
					Name:    "test-server",
					Version: "1.0.0",
				},
			}
		case "tools/list":
			result = ListToolsResult{Tools: []Tool{{Name: "test-tool", InputSchema: map[string]interface{}{}}}}
		case "tools/call":
			result = CallToolResult{Content: []ContentItem{{Type: "text", Text: "resultado"}}}
		case "notifications/initialized":
			w.WriteHeader(http.StatusNoContent)
			return
		default:
			result = map[string]string{"status": "ok"}
		}

		resp, _ := NewJSONRPCResponse(req.ID, result)
		respJSON, _ := json.Marshal(resp)

		accept := r.Header.Get("Accept")
		if strings.Contains(accept, "text/event-stream") {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			fmt.Fprintf(w, "data: %s\n\n", string(respJSON))
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.Write(respJSON)
		}
	}))
}

func TestHTTPClient_DeveInicializarComSucesso(t *testing.T) {
	srv := buildMCPServer(t, nil)
	defer srv.Close()

	config := ClientConfig{
		Name:          "test",
		TransportType: "http",
		HTTPBaseURL:   srv.URL,
	}
	client := NewHTTPClient(config, nil)

	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start retornou erro inesperado: %v", err)
	}

	if !client.IsRunning() {
		t.Error("cliente deveria estar em execução após Start bem-sucedido")
	}

	if client.GetServerInfo() == nil {
		t.Error("GetServerInfo deveria retornar informações após inicialização")
	}
}

func TestHTTPClient_DeveListarFerramentas(t *testing.T) {
	srv := buildMCPServer(t, nil)
	defer srv.Close()

	config := ClientConfig{Name: "test", TransportType: "http", HTTPBaseURL: srv.URL}
	client := NewHTTPClient(config, nil)
	client.Start(context.Background()) //nolint

	result, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools retornou erro: %v", err)
	}
	if len(result.Tools) == 0 {
		t.Error("esperava pelo menos uma ferramenta")
	}
}

func TestHTTPClient_DeveEnviarAuthorizationHeaderComOAuth(t *testing.T) {
	var capturedHeaders http.Header = make(http.Header)
	srv := buildMCPServer(t, &capturedHeaders)
	defer srv.Close()

	config := ClientConfig{Name: "test", TransportType: "http", HTTPBaseURL: srv.URL}
	oauthProvider := &fakeOAuth{token: "meu-token-secreto"}
	client := NewHTTPClient(config, oauthProvider)
	client.Start(context.Background()) //nolint

	client.ListTools(context.Background()) //nolint

	authHeader := capturedHeaders.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		t.Errorf("esperava header Authorization com Bearer, obteve: %q", authHeader)
	}
}

func TestHTTPClient_DeveRetornarErroParaServidor401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	config := ClientConfig{Name: "test", TransportType: "http", HTTPBaseURL: srv.URL}
	client := NewHTTPClient(config, nil)

	err := client.Start(context.Background())
	if err == nil {
		t.Fatal("esperava erro ao conectar em servidor que retorna 401")
	}
}

func TestHTTPClient_DeveRetornarErroSemInicializar(t *testing.T) {
	config := ClientConfig{Name: "test", TransportType: "http", HTTPBaseURL: "http://localhost:9"}
	client := NewHTTPClient(config, nil)
	// Do NOT call Start()

	_, err := client.ListTools(context.Background())
	if err == nil {
		t.Fatal("esperava erro ao chamar método em cliente não iniciado")
	}
}

func TestHTTPClient_GetConfigDeveRetornarConfigOriginal(t *testing.T) {
	config := ClientConfig{
		Name:          "meu-servidor",
		TransportType: "http",
		HTTPBaseURL:   "http://mcp.example.com",
	}
	client := NewHTTPClient(config, nil)

	cfg := client.GetConfig()
	if cfg.Name != "meu-servidor" {
		t.Errorf("esperava Name=%q, obteve %q", "meu-servidor", cfg.Name)
	}
	if cfg.TransportType != "http" {
		t.Errorf("esperava TransportType=%q, obteve %q", "http", cfg.TransportType)
	}
	if cfg.HTTPBaseURL != "http://mcp.example.com" {
		t.Errorf("HTTPBaseURL incorreto: %q", cfg.HTTPBaseURL)
	}
}

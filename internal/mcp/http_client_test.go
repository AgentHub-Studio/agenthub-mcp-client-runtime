package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

func TestHTTPClient_DeveAnunciarJsonESSENoAccept(t *testing.T) {
	var capturedHeaders http.Header = make(http.Header)
	srv := buildMCPServer(t, &capturedHeaders)
	defer srv.Close()

	config := ClientConfig{Name: "test", TransportType: "http", HTTPBaseURL: srv.URL}
	client := NewHTTPClient(config, nil)

	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start retornou erro inesperado: %v", err)
	}

	accept := capturedHeaders.Get("Accept")
	if !strings.Contains(accept, "application/json") || !strings.Contains(accept, "text/event-stream") {
		t.Fatalf("Accept deveria anunciar JSON e SSE, obteve: %q", accept)
	}
}

func TestHTTPClient_DeveReutilizarSessionIDEProtocolVersion(t *testing.T) {
	var toolListSessionID string
	var toolListProtocolVersion string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req JSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess-123")
			resp, _ := NewJSONRPCResponse(req.ID, InitializeResult{
				ProtocolVersion: ProtocolVersion,
				ServerInfo: ServerInfo{
					Name:    "test-server",
					Version: "1.0.0",
				},
			})
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case "notifications/initialized":
			if got := r.Header.Get("Mcp-Session-Id"); got != "sess-123" {
				t.Fatalf("initialized deveria enviar session ID, obteve %q", got)
			}
			if got := r.Header.Get("MCP-Protocol-Version"); got != ProtocolVersion {
				t.Fatalf("initialized deveria enviar protocol version, obteve %q", got)
			}
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			toolListSessionID = r.Header.Get("Mcp-Session-Id")
			toolListProtocolVersion = r.Header.Get("MCP-Protocol-Version")
			resp, _ := NewJSONRPCResponse(req.ID, ListToolsResult{
				Tools: []Tool{{Name: "test-tool", InputSchema: map[string]interface{}{}}},
			})
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.Error(w, "unsupported", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	config := ClientConfig{Name: "test", TransportType: "http", HTTPBaseURL: srv.URL}
	client := NewHTTPClient(config, nil)
	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start retornou erro inesperado: %v", err)
	}

	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools retornou erro inesperado: %v", err)
	}

	if toolListSessionID != "sess-123" {
		t.Fatalf("tools/list deveria enviar session ID persistido, obteve %q", toolListSessionID)
	}
	if toolListProtocolVersion != ProtocolVersion {
		t.Fatalf("tools/list deveria enviar MCP-Protocol-Version, obteve %q", toolListProtocolVersion)
	}
}

func TestHTTPClient_DevePropagarCorpoDoErroHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing required accept header", http.StatusBadRequest)
	}))
	defer srv.Close()

	config := ClientConfig{Name: "test", TransportType: "http", HTTPBaseURL: srv.URL}
	client := NewHTTPClient(config, nil)

	err := client.Start(context.Background())
	if err == nil {
		t.Fatal("esperava erro ao conectar em servidor que retorna 400")
	}
	if !strings.Contains(err.Error(), "missing required accept header") {
		t.Fatalf("erro deveria incluir corpo HTTP para diagnóstico, obteve: %v", err)
	}
}

func TestHTTPClient_DeveIgnorarEventosSSEIntermediariosAntesDaResposta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req JSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		switch req.Method {
		case "initialize":
			resp, _ := NewJSONRPCResponse(req.ID, InitializeResult{
				ProtocolVersion: ProtocolVersion,
				ServerInfo: ServerInfo{Name: "test-server", Version: "1.0.0"},
			})
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			finalResp, _ := NewJSONRPCResponse(req.ID, CallToolResult{
				Content: []ContentItem{{Type: "text", Text: "ok"}},
			})
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"progress\":50}}\n\n")
			fmt.Fprintf(w, "data: %s\n\n", string(mustJSON(t, finalResp)))
		default:
			http.Error(w, "unsupported", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	config := ClientConfig{Name: "test", TransportType: "http", HTTPBaseURL: srv.URL}
	client := NewHTTPClient(config, nil)
	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start retornou erro inesperado: %v", err)
	}

	result, err := client.CallTool(context.Background(), "get_me", map[string]interface{}{})
	if err != nil {
		t.Fatalf("CallTool retornou erro inesperado: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "ok" {
		t.Fatalf("resultado inesperado: %#v", result.Content)
	}
}

func mustJSON(t *testing.T, v interface{}) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	return data
}

func TestHTTPClient_DeveAceitarRespostaSSEGrande(t *testing.T) {
	largeDescription := strings.Repeat("x", 128*1024)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/list":
			result = ListToolsResult{
				Tools: []Tool{{
					Name:        "large-tool",
					Description: largeDescription,
					InputSchema: map[string]interface{}{},
				}},
			}
		default:
			http.Error(w, "unsupported", http.StatusBadRequest)
			return
		}

		resp, _ := NewJSONRPCResponse(req.ID, result)
		respJSON, _ := json.Marshal(resp)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", string(respJSON))
	}))
	defer srv.Close()

	config := ClientConfig{Name: "test", TransportType: "http", HTTPBaseURL: srv.URL}
	client := NewHTTPClient(config, nil)

	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start retornou erro inesperado: %v", err)
	}

	result, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools retornou erro inesperado: %v", err)
	}
	if len(result.Tools) != 1 || result.Tools[0].Description != largeDescription {
		t.Fatal("resposta SSE grande não foi parseada corretamente")
	}
}

func TestHTTPClient_DeveEnviarArgumentsVazioEmToolsCall(t *testing.T) {
	var rawBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		rawBody, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body", http.StatusInternalServerError)
			return
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(rawBody, &req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		switch req.Method {
		case "initialize":
			resp, _ := NewJSONRPCResponse(req.ID, InitializeResult{
				ProtocolVersion: ProtocolVersion,
				ServerInfo: ServerInfo{Name: "test-server", Version: "1.0.0"},
			})
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			var payload map[string]any
			if err := json.Unmarshal(req.Params, &payload); err != nil {
				http.Error(w, "bad params", http.StatusBadRequest)
				return
			}
			args, ok := payload["arguments"].(map[string]any)
			if !ok {
				http.Error(w, "missing arguments object", http.StatusBadRequest)
				return
			}
			if len(args) != 0 {
				http.Error(w, "arguments should be empty", http.StatusBadRequest)
				return
			}
			resp, _ := NewJSONRPCResponse(req.ID, CallToolResult{
				Content: []ContentItem{{Type: "text", Text: "ok"}},
			})
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.Error(w, "unsupported", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	config := ClientConfig{Name: "test", TransportType: "http", HTTPBaseURL: srv.URL}
	client := NewHTTPClient(config, nil)
	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start retornou erro inesperado: %v", err)
	}

	if _, err := client.CallTool(context.Background(), "get_me", map[string]interface{}{}); err != nil {
		t.Fatalf("CallTool retornou erro inesperado: %v", err)
	}

	if !strings.Contains(string(rawBody), "\"arguments\":{}") {
		t.Fatalf("tools/call deveria serializar arguments vazio, corpo=%s", string(rawBody))
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

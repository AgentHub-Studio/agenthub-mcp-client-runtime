package backend

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeTokenProvider is a test double for TokenProvider.
type fakeTokenProvider struct {
	token string
	err   error
}

func (f *fakeTokenProvider) Token() (string, error) { return f.token, f.err }

func TestBackendClient_DeveListarConfiguracoes(t *testing.T) {
	configs := []ServerConfig{
		{ID: "1", Name: "github", TransportType: "stdio", Enabled: true},
		{ID: "2", Name: "filesystem", TransportType: "stdio", Enabled: true},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(configs)
	}))
	defer srv.Close()

	client := NewBackendClientWithStaticToken(srv.URL, "token-abc")
	result, err := client.ListConfigs(context.Background())
	if err != nil {
		t.Fatalf("ListConfigs retornou erro: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("esperava 2 configurações, obteve %d", len(result))
	}
}

func TestBackendClient_NaoDeveEnviarHeaderXTenantID(t *testing.T) {
	// The backend determines tenant from the JWT iss claim — X-Tenant-ID is not used.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Tenant-ID") != "" {
			t.Error("BackendClient não deve enviar X-Tenant-ID: o backend ignora este header")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]ServerConfig{})
	}))
	defer srv.Close()

	client := NewBackendClientWithStaticToken(srv.URL, "token")
	client.ListConfigs(context.Background()) //nolint
}

func TestBackendClient_DeveUsarEndpointBootstrap(t *testing.T) {
	// ListConfigs must call /bootstrap to receive oauthClientSecret.
	var capturedPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]ServerConfig{})
	}))
	defer srv.Close()

	client := NewBackendClientWithStaticToken(srv.URL, "token")
	client.ListConfigs(context.Background()) //nolint

	const expected = "/api/mcp-server-configs/bootstrap"
	if capturedPath != expected {
		t.Errorf("path incorreto: esperava %q, obteve %q", expected, capturedPath)
	}
}

func TestBackendClient_DeveEnviarTokenDoProvider(t *testing.T) {
	var capturedAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]ServerConfig{})
	}))
	defer srv.Close()

	provider := &fakeTokenProvider{token: "token-do-keycloak"}
	client := NewBackendClient(srv.URL, provider)
	client.ListConfigs(context.Background()) //nolint

	if !strings.HasPrefix(capturedAuth, "Bearer ") {
		t.Errorf("esperava Authorization com Bearer, obteve %q", capturedAuth)
	}
	if capturedAuth != "Bearer token-do-keycloak" {
		t.Errorf("token incorreto: %q", capturedAuth)
	}
}

func TestBackendClient_DeveRetornarErroQuandoProviderFalha(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	provider := &fakeTokenProvider{err: errors.New("keycloak indisponível")}
	client := NewBackendClient(srv.URL, provider)

	_, err := client.ListConfigs(context.Background())
	if err == nil {
		t.Fatal("esperava erro quando o provider falha")
	}
	if !strings.Contains(err.Error(), "obtaining bearer token") {
		t.Errorf("mensagem de erro inesperada: %v", err)
	}
}

func TestBackendClient_DeveRetornarErroParaStatus4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := NewBackendClientWithStaticToken(srv.URL, "bad-token")
	_, err := client.ListConfigs(context.Background())
	if err == nil {
		t.Fatal("esperava erro para resposta 401")
	}
}

func TestBackendClient_DeveRetornarErroParaURLInvalida(t *testing.T) {
	client := NewBackendClientWithStaticToken("http://backend.invalid:9999", "token")
	_, err := client.ListConfigs(context.Background())
	if err == nil {
		t.Fatal("esperava erro para URL inválida")
	}
}

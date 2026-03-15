package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// buildTokenServer creates a test OAuth token server that returns a valid token response.
func buildTokenServer(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "test-token-abc123",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
}

func TestTokenClient_DeveObterTokenComSucesso(t *testing.T) {
	var calls atomic.Int32
	srv := buildTokenServer(t, &calls)
	defer srv.Close()

	client, err := NewTokenClient(context.Background(), srv.URL, "client-id", "client-secret", nil)
	if err != nil {
		t.Fatalf("erro ao criar TokenClient: %v", err)
	}

	token, err := client.Token()
	if err != nil {
		t.Fatalf("erro ao obter token: %v", err)
	}

	if token != "test-token-abc123" {
		t.Errorf("token inesperado: %q", token)
	}
}

func TestTokenClient_DeveFazerCacheDoToken(t *testing.T) {
	var calls atomic.Int32
	srv := buildTokenServer(t, &calls)
	defer srv.Close()

	client, err := NewTokenClient(context.Background(), srv.URL, "client-id", "client-secret", nil)
	if err != nil {
		t.Fatalf("erro ao criar TokenClient: %v", err)
	}

	// Call Token() twice — should only hit the server once due to caching
	_, err = client.Token()
	if err != nil {
		t.Fatalf("primeira chamada falhou: %v", err)
	}
	_, err = client.Token()
	if err != nil {
		t.Fatalf("segunda chamada falhou: %v", err)
	}

	if calls.Load() != 1 {
		t.Errorf("esperava 1 chamada ao servidor (cache), obteve %d", calls.Load())
	}
}

func TestTokenClient_DeveRetornarErroParaURLInvalida(t *testing.T) {
	client, err := NewTokenClient(context.Background(), "http://token-server.invalid/token", "id", "secret", nil)
	if err != nil {
		t.Fatalf("NewTokenClient não deveria falhar na criação: %v", err)
	}

	_, err = client.Token()
	if err == nil {
		t.Fatal("esperava erro para servidor inválido")
	}
}

func TestTokenClient_DeveRetornarErroSemTokenURL(t *testing.T) {
	_, err := NewTokenClient(context.Background(), "", "id", "secret", nil)
	if err == nil {
		t.Fatal("esperava erro quando tokenURL está vazio")
	}
}

func TestTokenClient_DeveRetornarErroSemClientID(t *testing.T) {
	_, err := NewTokenClient(context.Background(), "http://token.test/token", "", "secret", nil)
	if err == nil {
		t.Fatal("esperava erro quando clientID está vazio")
	}
}

func TestTokenClient_DeveRenovarTokenExpirado(t *testing.T) {
	var calls atomic.Int32
	// Server returns a token that expires in 1 second
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "short-lived-token",
			"token_type":   "Bearer",
			"expires_in":   1,
		})
	}))
	defer srv.Close()

	client, err := NewTokenClient(context.Background(), srv.URL, "client-id", "client-secret", nil)
	if err != nil {
		t.Fatalf("erro ao criar TokenClient: %v", err)
	}

	_, err = client.Token()
	if err != nil {
		t.Fatalf("primeira chamada falhou: %v", err)
	}

	// Wait for token to expire
	time.Sleep(2 * time.Second)

	_, err = client.Token()
	if err != nil {
		t.Fatalf("chamada após expiração falhou: %v", err)
	}

	if calls.Load() < 2 {
		t.Errorf("esperava pelo menos 2 chamadas ao servidor após expiração, obteve %d", calls.Load())
	}
}

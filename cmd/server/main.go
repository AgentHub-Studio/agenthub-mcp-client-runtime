package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/agenthub/mcp-client-runtime/internal/api"
	"github.com/agenthub/mcp-client-runtime/internal/backend"
	"github.com/agenthub/mcp-client-runtime/internal/grpc"
	"github.com/agenthub/mcp-client-runtime/internal/mcp"
	"github.com/agenthub/mcp-client-runtime/internal/migrate"
	"github.com/agenthub/mcp-client-runtime/internal/oauth"
	_ "github.com/jackc/pgx/v5/stdlib"
	"google.golang.org/grpc/reflection"
	grpcserver "google.golang.org/grpc"
)

// Config holds application configuration loaded from environment variables.
type Config struct {
	GRPCPort string
	HTTPPort string
	LogLevel string

	// Database — required for running migrations on startup.
	// Format: postgres://user:pass@host:5432/dbname
	DatabaseURL string // DATABASE_URL

	// Backend integration — used to load MCP server configurations on startup.
	// The backend determines the tenant from the JWT iss claim (Keycloak realm = tenant UUID).
	BackendURL string // AGENTHUB_BACKEND_URL — e.g. http://agenthub-backend:8090
	TenantID   string // AGENTHUB_TENANT_ID   — tenant UUID (must match the Keycloak realm name)

	// Keycloak Client Credentials (preferred — automatic token renewal).
	// Token URL is built as: {KeycloakBaseURL}/realms/{TenantID}/protocol/openid-connect/token
	// This mirrors the backend's spring.security.oauth2.resourceserver.jwt.issuer-uri pattern.
	KeycloakBaseURL string // AGENTHUB_KEYCLOAK_BASE_URL — e.g. http://keycloak.internal:8080
	ClientID        string // AGENTHUB_CLIENT_ID          — service account client ID
	ClientSecret    string // AGENTHUB_CLIENT_SECRET       — service account client secret

	// Static token fallback (development / simple deploys without Keycloak credentials).
	APIToken string // AGENTHUB_API_TOKEN — pre-generated bearer token
}

// loadConfig reads configuration from environment variables.
func loadConfig() *Config {
	return &Config{
		GRPCPort:        getEnv("GRPC_PORT", "50051"),
		HTTPPort:        getEnv("HTTP_PORT", "8080"),
		LogLevel:        getEnv("LOG_LEVEL", "info"),
		DatabaseURL:     getEnv("DATABASE_URL", ""),
		BackendURL:      getEnv("AGENTHUB_BACKEND_URL", ""),
		TenantID:        getEnv("AGENTHUB_TENANT_ID", ""),
		KeycloakBaseURL: getEnv("AGENTHUB_KEYCLOAK_BASE_URL", ""),
		ClientID:        getEnv("AGENTHUB_CLIENT_ID", ""),
		ClientSecret:    getEnv("AGENTHUB_CLIENT_SECRET", ""),
		APIToken:        getEnv("AGENTHUB_API_TOKEN", ""),
	}
}

// getEnv returns the environment variable value or a default.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func main() {
	printBanner()
	log.Println("Iniciando MCP Client Runtime...")

	config := loadConfig()
	log.Printf("Configurações: gRPC=%s, HTTP=%s, LogLevel=%s",
		config.GRPCPort, config.HTTPPort, config.LogLevel)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run database migrations before starting servers.
	if config.DatabaseURL != "" && config.TenantID != "" {
		if err := runMigrations(ctx, config); err != nil {
			log.Fatalf("Falha ao executar migrations: %v", err)
		}
	} else {
		log.Println("DATABASE_URL ou AGENTHUB_TENANT_ID não configurados — migrations ignoradas")
	}

	mcpManager := mcp.NewManager()
	log.Println("MCP Manager criado")

	if config.BackendURL != "" {
		bootstrapConfigs(ctx, config, mcpManager)
	} else {
		log.Println("AGENTHUB_BACKEND_URL não configurado — bootstrap de configurações ignorado")
	}

	errChan := make(chan error, 2)

	grpcServer := startGRPCServer(ctx, config.GRPCPort, mcpManager, errChan)
	defer grpcServer.GracefulStop()

	_ = startHTTPServer(ctx, config.HTTPPort, mcpManager, errChan)

	log.Println("MCP Client Runtime iniciado com sucesso")
	log.Printf("gRPC server rodando em :%s", config.GRPCPort)
	log.Printf("HTTP server rodando em :%s", config.HTTPPort)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sig := <-sigChan:
		log.Printf("Sinal recebido: %v. Iniciando shutdown graceful...", sig)
	case err := <-errChan:
		log.Printf("Erro em servidor: %v. Iniciando shutdown...", err)
	}

	cancel()
	log.Println("Parando todos os servidores MCP...")
	mcpManager.StopAll()

	log.Println("MCP Client Runtime desligado com sucesso")
}

// runMigrations opens a database connection, applies all pending migrations, then closes it.
// The connection is intentionally short-lived — only used during startup for schema management.
func runMigrations(ctx context.Context, config *Config) error {
	log.Printf("migrate: connecting to database for migrations (tenant=%s)", config.TenantID)

	db, err := sql.Open("pgx", config.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}

	m := migrate.New(db, config.TenantID)
	if err := m.Run(ctx); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	log.Println("migrate: all migrations applied successfully")
	return nil
}

// buildBackendTokenProvider selects the appropriate token provider for the backend API.
//
// Priority:
//  1. Keycloak Client Credentials (AGENTHUB_KEYCLOAK_BASE_URL + AGENTHUB_TENANT_ID +
//     AGENTHUB_CLIENT_ID + AGENTHUB_CLIENT_SECRET) — automatic token renewal.
//     Token URL: {KeycloakBaseURL}/realms/{TenantID}/protocol/openid-connect/token
//  2. Static token (AGENTHUB_API_TOKEN) — for development / simple deploys.
//  3. nil — no authentication (request will likely be rejected by the backend).
func buildBackendTokenProvider(ctx context.Context, config *Config) backend.TokenProvider {
	if config.KeycloakBaseURL != "" && config.TenantID != "" && config.ClientID != "" {
		tokenURL := fmt.Sprintf(
			"%s/realms/%s/protocol/openid-connect/token",
			config.KeycloakBaseURL,
			config.TenantID,
		)
		log.Printf("Autenticação backend: Keycloak Client Credentials (url=%s, clientId=%s)",
			tokenURL, config.ClientID)

		tokenClient, err := oauth.NewTokenClient(ctx,
			tokenURL,
			config.ClientID,
			config.ClientSecret,
			nil,
		)
		if err != nil {
			log.Printf("Aviso: falha ao configurar Keycloak para o backend — tentando token estático: %v", err)
		} else {
			return tokenClient
		}
	}

	if config.APIToken != "" {
		log.Println("Autenticação backend: token estático (AGENTHUB_API_TOKEN)")
		return backend.NewStaticTokenProvider(config.APIToken)
	}

	log.Println("Aviso: nenhuma autenticação configurada para o backend " +
		"(defina AGENTHUB_KEYCLOAK_BASE_URL + AGENTHUB_TENANT_ID + AGENTHUB_CLIENT_ID ou AGENTHUB_API_TOKEN)")
	return nil
}

// bootstrapConfigs loads server configurations from the backend and registers them.
// Servers with auto_start = true are connected immediately.
func bootstrapConfigs(ctx context.Context, config *Config, manager *mcp.Manager) {
	log.Printf("Carregando configurações MCP do backend: %s (tenant=%s)", config.BackendURL, config.TenantID)

	tokenProvider := buildBackendTokenProvider(ctx, config)
	backendClient := backend.NewBackendClient(config.BackendURL, tokenProvider)

	configs, err := backendClient.ListConfigs(ctx)
	if err != nil {
		log.Printf("Aviso: falha ao carregar configurações MCP do backend: %v", err)
		return
	}

	log.Printf("Configurações MCP carregadas: %d encontradas", len(configs))

	for _, cfg := range configs {
		if !cfg.Enabled {
			continue
		}

		clientConfig := mcp.ClientConfig{
			Name:          cfg.Name,
			TransportType: cfg.TransportType,
			HTTPBaseURL:   cfg.HTTPBaseURL,
			Command:       cfg.Command,
			Args:          cfg.Args,
			Env:           buildEnvSlice(cfg.Env),
			OnAuthRequired: func(metadata mcp.AuthMetadata) {
				log.Printf("Auth required for %s: %+v", cfg.Name, metadata)
				// In a real implementation, this could send a notification back to the backend.
			},
		}

		if cfg.TransportType == "http" && cfg.OAuthTokenURL != "" {
			tokenClient, err := oauth.NewTokenClient(
				ctx,
				cfg.OAuthTokenURL,
				cfg.OAuthClientID,
				cfg.OAuthClientSecret,
				cfg.OAuthScopes,
			)
			if err != nil {
				log.Printf("Aviso: falha ao configurar OAuth para %q: %v", cfg.Name, err)
			} else {
				clientConfig.OAuthProvider = tokenClient
			}
		}

		if err := manager.RegisterServer(clientConfig); err != nil {
			log.Printf("Aviso: falha ao registrar servidor %q: %v", cfg.Name, err)
			continue
		}

		if cfg.AutoStart {
			log.Printf("Auto-start: conectando ao servidor MCP %q...", cfg.Name)
			if err := manager.StartServer(ctx, cfg.Name); err != nil {
				log.Printf("Aviso: falha ao iniciar servidor %q: %v", cfg.Name, err)
			} else {
				log.Printf("Servidor MCP %q conectado com sucesso", cfg.Name)
			}
		} else {
			log.Printf("Servidor MCP %q registrado (auto_start=false)", cfg.Name)
		}
	}
}

// buildEnvSlice converts a map of environment variables to KEY=VALUE slice format.
func buildEnvSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	result := make([]string, 0, len(env))
	for k, v := range env {
		result = append(result, fmt.Sprintf("%s=%s", k, v))
	}
	return result
}

// startGRPCServer starts the gRPC server in a goroutine and returns it.
func startGRPCServer(ctx context.Context, port string, manager *mcp.Manager, errChan chan<- error) *grpcserver.Server {
	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("Falha ao criar listener gRPC na porta %s: %v", port, err)
	}

	grpcSrv := grpcserver.NewServer()

	portInt := 50051
	fmt.Sscanf(port, "%d", &portInt)

	mcpService := grpc.NewServer(portInt, manager)
	_ = mcpService

	reflection.Register(grpcSrv)

	go func() {
		log.Printf("Iniciando gRPC server na porta %s...", port)
		if err := grpcSrv.Serve(listener); err != nil {
			errChan <- fmt.Errorf("gRPC server error: %w", err)
		}
	}()

	go func() {
		<-ctx.Done()
		log.Println("Parando gRPC server...")
		grpcSrv.GracefulStop()
	}()

	return grpcSrv
}

// startHTTPServer starts the HTTP server in a goroutine and returns it.
func startHTTPServer(ctx context.Context, port string, manager *mcp.Manager, errChan chan<- error) *api.HTTPServer {
	portInt := 8080
	fmt.Sscanf(port, "%d", &portInt)

	httpSrv := api.NewHTTPServer(portInt, manager)

	go func() {
		log.Printf("Iniciando HTTP server na porta %s...", port)
		if err := httpSrv.Start(); err != nil {
			errChan <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	return httpSrv
}

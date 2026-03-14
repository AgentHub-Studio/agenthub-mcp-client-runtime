package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/agenthub/mcp-client-runtime/internal/api"
	"github.com/agenthub/mcp-client-runtime/internal/grpc"
	"github.com/agenthub/mcp-client-runtime/internal/mcp"
	"google.golang.org/grpc/reflection"
	grpcserver "google.golang.org/grpc"
)

// Config armazena as configurações da aplicação
type Config struct {
	GRPCPort string
	HTTPPort string
	LogLevel string
}

// loadConfig carrega configurações de variáveis de ambiente
func loadConfig() *Config {
	return &Config{
		GRPCPort: getEnv("GRPC_PORT", "50051"),
		HTTPPort: getEnv("HTTP_PORT", "8080"),
		LogLevel: getEnv("LOG_LEVEL", "info"),
	}
}

// getEnv retorna variável de ambiente ou valor padrão
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func main() {
	log.Println("Iniciando MCP Client Runtime...")

	// Carregar configurações
	config := loadConfig()
	log.Printf("Configurações: gRPC=%s, HTTP=%s, LogLevel=%s", 
		config.GRPCPort, config.HTTPPort, config.LogLevel)

	// Criar MCP Manager
	mcpManager := mcp.NewManager()
	log.Println("MCP Manager criado")

	// Contexto com cancelamento para shutdown graceful
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Canal para erros dos servidores
	errChan := make(chan error, 2)

	// Iniciar gRPC server
	grpcServer := startGRPCServer(ctx, config.GRPCPort, mcpManager, errChan)
	defer grpcServer.GracefulStop()

	// Iniciar HTTP server
	_ = startHTTPServer(ctx, config.HTTPPort, mcpManager, errChan)

	log.Println("MCP Client Runtime iniciado com sucesso")
	log.Printf("gRPC server rodando em :%s", config.GRPCPort)
	log.Printf("HTTP server rodando em :%s", config.HTTPPort)

	// Aguardar sinal de interrupção ou erro
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sig := <-sigChan:
		log.Printf("Sinal recebido: %v. Iniciando shutdown graceful...", sig)
	case err := <-errChan:
		log.Printf("Erro em servidor: %v. Iniciando shutdown...", err)
	}

	// Shutdown graceful
	cancel()
	log.Println("Parando todos os servidores MCP...")
	mcpManager.StopAll()
	
	log.Println("MCP Client Runtime desligado com sucesso")
}

// startGRPCServer inicia o servidor gRPC em uma goroutine
func startGRPCServer(ctx context.Context, port string, manager *mcp.Manager, errChan chan<- error) *grpcserver.Server {
	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("Falha ao criar listener gRPC na porta %s: %v", port, err)
	}

	grpcSrv := grpcserver.NewServer()
	
	// Converter port string para int
	portInt := 50051 // default
	fmt.Sscanf(port, "%d", &portInt)
	
	mcpService := grpc.NewServer(portInt, manager)
	
	// Registrar serviço (será usado quando protobuf estiver gerado)
	// pb.RegisterMCPClientServiceServer(grpcSrv, mcpService)
	
	// Por enquanto, apenas armazenar referência
	_ = mcpService
	
	// Habilitar reflexão para facilitar debugging (grpcurl, etc)
	reflection.Register(grpcSrv)

	// Iniciar servidor em goroutine
	go func() {
		log.Printf("Iniciando gRPC server na porta %s...", port)
		if err := grpcSrv.Serve(listener); err != nil {
			errChan <- fmt.Errorf("gRPC server error: %w", err)
		}
	}()

	// Goroutine para shutdown graceful
	go func() {
		<-ctx.Done()
		log.Println("Parando gRPC server...")
		grpcSrv.GracefulStop()
	}()

	return grpcSrv
}

// startHTTPServer inicia o servidor HTTP em uma goroutine
func startHTTPServer(ctx context.Context, port string, manager *mcp.Manager, errChan chan<- error) *api.HTTPServer {
	// Converter port string para int
	portInt := 8080 // default
	fmt.Sscanf(port, "%d", &portInt)
	
	httpSrv := api.NewHTTPServer(portInt, manager)

	// Iniciar servidor em goroutine
	go func() {
		log.Printf("Iniciando HTTP server na porta %s...", port)
		if err := httpSrv.Start(); err != nil {
			errChan <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	return httpSrv
}

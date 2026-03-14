# AgentHub MCP Client Runtime (Go)

**MCP Client Runtime** - Cliente Go para conectar em servidores MCP externos e expor via gRPC para o backend Java.

## 📋 Visão Geral

O MCP Client Runtime permite que o AgentHub se conecte a servidores Model Context Protocol (MCP) externos, descobrindo e executando suas capabilities (tools, prompts, resources).

## 🏗️ Arquitetura

```
┌────────────────────────────────────────────────────────────┐
│                  AgentHub Platform                          │
├────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌────────────────┐         ┌──────────────────┐           │
│  │  Skill Runtime │────────>│  MCP Client      │           │
│  │  (Java)        │  gRPC   │  Runtime (Go)    │           │
│  │                │         │                  │           │
│  │  - Tools       │         │  - MCP Protocol  │           │
│  │  - Skills      │         │  - Stdio Client  │           │
│  └────────────────┘         │  - gRPC Server   │           │
│                              └────────┬─────────┘           │
│                                       │                     │
│                                       │ stdio               │
│                                       │                     │
│                              ┌────────v─────────┐           │
│                              │  External MCP    │           │
│                              │  Servers         │           │
│                              │                  │           │
│                              │  - filesystem    │           │
│                              │  - github        │           │
│                              │  - slack         │           │
│                              │  - custom...     │           │
│                              └──────────────────┘           │
│                                                              │
└────────────────────────────────────────────────────────────┘
```

## 🚀 Funcionalidades

### 1. MCP Client (stdio transport)
- ✅ Conectar a servidores MCP via stdio (subprocess)
- ✅ Implementar protocolo JSON-RPC 2.0
- ✅ Gerenciar lifecycle de processos (spawn, kill, restart)
- ✅ Handle input/output streams

### 2. Discovery
- ✅ Listar tools disponíveis no servidor MCP
- ✅ Listar prompts disponíveis
- ✅ Listar resources disponíveis
- ✅ Schema validation (JSON Schema)

### 3. Execution
- ✅ Executar tools com parâmetros
- ✅ Executar prompts com variáveis
- ✅ Ler resources
- ✅ Streaming support (se aplicável)

### 4. gRPC Bridge
- ✅ Expor API gRPC para Java backend
- ✅ Conversão de tipos Go ↔ Protobuf
- ✅ Error handling e retries
- ✅ Connection pooling

## 📦 Estrutura do Projeto

```
agenthub-mcp-client-runtime/
├── cmd/
│   └── server/
│       └── main.go              # Entry point da aplicação
├── internal/
│   ├── mcp/
│   │   ├── client.go            # MCP client (stdio transport)
│   │   ├── protocol.go          # JSON-RPC 2.0 protocol
│   │   └── types.go             # MCP types (Tool, Prompt, Resource)
│   ├── grpc/
│   │   ├── server.go            # gRPC server implementation
│   │   └── handler.go           # gRPC request handlers
│   └── api/
│       └── http.go              # HTTP API (health check, discovery)
├── pkg/
│   └── mcpclient/
│       └── client.go            # Public client interface
├── proto/
│   └── mcpclient.proto          # Protobuf definitions
├── go.mod
├── go.sum
├── Dockerfile
├── Makefile
└── README.md
```

## 🔧 Tecnologias

- **Go 1.24+** - Linguagem principal
- **gRPC** - Comunicação com backend Java
- **Protocol Buffers** - Serialização de dados
- **JSON-RPC 2.0** - Protocolo MCP
- **Gin** - HTTP server (health checks)

## 📝 APIs

### gRPC API

```protobuf
service MCPClientService {
  // Descobrir capabilities de um servidor MCP
  rpc DiscoverCapabilities(DiscoverRequest) returns (DiscoverResponse);
  
  // Executar um tool
  rpc ExecuteTool(ExecuteToolRequest) returns (ExecuteToolResponse);
  
  // Executar um prompt
  rpc ExecutePrompt(ExecutePromptRequest) returns (ExecutePromptResponse);
  
  // Ler um resource
  rpc ReadResource(ReadResourceRequest) returns (ReadResourceResponse);
}
```

### HTTP API (complementar)

- `GET /health` - Health check
- `GET /servers` - Listar servidores MCP conectados
- `POST /servers` - Registrar novo servidor MCP

## 🚀 Quick Start

### Compilar

```bash
make build
```

### Executar

```bash
./bin/mcp-client-runtime
```

### Docker

```bash
docker build -t agenthub-mcp-client-runtime .
docker run -p 50051:50051 -p 8080:8080 agenthub-mcp-client-runtime
```

## 🔌 Integração com Skill Runtime

O Skill Runtime (Java) se conecta ao MCP Client Runtime via gRPC:

```java
// Java - Skill Runtime
MCPClientServiceGrpc.MCPClientServiceBlockingStub stub = 
    MCPClientServiceGrpc.newBlockingStub(channel);

// Descobrir capabilities
DiscoverResponse response = stub.discoverCapabilities(
    DiscoverRequest.newBuilder()
        .setServerName("filesystem")
        .build()
);

// Executar tool
ExecuteToolResponse result = stub.executeTool(
    ExecuteToolRequest.newBuilder()
        .setServerName("filesystem")
        .setToolName("read_file")
        .putParameters("path", "/etc/hosts")
        .build()
);
```

## 📚 Servidores MCP Suportados

### Oficiais
- `@modelcontextprotocol/server-filesystem` - Operações de filesystem
- `@modelcontextprotocol/server-github` - GitHub API
- `@modelcontextprotocol/server-git` - Git operations
- `@modelcontextprotocol/server-slack` - Slack integration

### Custom
- Qualquer servidor que implemente MCP protocol via stdio

## 🔐 Segurança

- ✅ Validação de inputs (JSON Schema)
- ✅ Sandboxing de processos
- ✅ Timeouts configuráveis
- ✅ Rate limiting
- ✅ Resource limits (CPU, memory)

## 📊 Observabilidade

- ✅ Structured logging (JSON)
- ✅ OpenTelemetry traces
- ✅ Prometheus metrics
- ✅ Health checks

## 🧪 Testes

```bash
make test
```

## 📖 Documentação Adicional

- [MCP Protocol Specification](https://modelcontextprotocol.io/)
- [gRPC Go Documentation](https://grpc.io/docs/languages/go/)
- [AgentHub Architecture](../docs/spec/AGENTS.md)

---

MIT License

# AgentHub MCP Client Runtime (Go)

**MCP Client** - Cliente para conectar em servidores MCP externos.

## Responsabilidades
- Conectar em servidores MCP externos
- Descobrir capabilities
- Executar capabilities (tools, prompts, resources)
- HTTP API para Skill Runtime

## API
- POST /mcp/execute
- POST /mcp/discover
- GET /health

## Tech Stack
- Go 1.21+
- Gin/Fiber
- MCP SDK

MIT License

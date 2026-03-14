# Multi-stage build para MCP Client Runtime
# Stage 1: Builder
FROM golang:1.24-alpine AS builder

# Instalar dependências de build
RUN apk add --no-cache git make protobuf protobuf-dev

# Configurar diretório de trabalho
WORKDIR /build

# Copiar código fonte completo
COPY . .

# Baixar dependências e criar go.sum
RUN go mod download && go mod tidy

# Compilar binário
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -a -installsuffix cgo \
    -o /build/bin/mcp-client-runtime \
    ./cmd/server

# Stage 2: Runtime
FROM alpine:latest

# Instalar certificados CA e timezone data
RUN apk --no-cache add ca-certificates tzdata

# Criar usuário não-root
RUN addgroup -g 1000 mcp && \
    adduser -D -u 1000 -G mcp mcp

# Configurar diretório de trabalho
WORKDIR /app

# Copiar binário do builder
COPY --from=builder /build/bin/mcp-client-runtime /app/mcp-client-runtime

# Dar permissão de execução
RUN chmod +x /app/mcp-client-runtime

# Mudar para usuário não-root
USER mcp

# Expor portas
EXPOSE 50051 8080

# Configurar variáveis de ambiente padrão
ENV GRPC_PORT=50051 \
    HTTP_PORT=8080 \
    LOG_LEVEL=info

# Healthcheck via HTTP endpoint
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Comando para executar
ENTRYPOINT ["/app/mcp-client-runtime"]

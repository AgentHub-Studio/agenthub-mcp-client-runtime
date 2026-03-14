#!/bin/bash
set -e

echo "Gerando código Go a partir do protobuf..."

# Verificar se protoc está instalado
if ! command -v protoc &> /dev/null; then
    echo "protoc não encontrado. Instale o Protocol Buffers compiler."
    echo "Usando Docker para gerar..."
    
    docker run --rm \
        -v $(pwd):/workspace \
        -w /workspace \
        namely/protoc-all:1.51_1 \
        -f proto/mcpclient.proto \
        -l go \
        -o .
    
    exit 0
fi

# Verificar se os plugins Go estão instalados
if ! command -v protoc-gen-go &> /dev/null; then
    echo "Instalando protoc-gen-go..."
    go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
fi

if ! command -v protoc-gen-go-grpc &> /dev/null; then
    echo "Instalando protoc-gen-go-grpc..."
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
fi

# Gerar código Go
protoc \
    --go_out=. \
    --go_opt=paths=source_relative \
    --go-grpc_out=. \
    --go-grpc_opt=paths=source_relative \
    proto/mcpclient.proto

echo "✅ Código Go gerado com sucesso!"

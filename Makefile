GO_IMAGE := golang:1.24
DOCKER_RUN := docker run --rm -v $(PWD):/app -v $(HOME)/go/pkg/mod:/go/pkg/mod -w /app $(GO_IMAGE)

.PHONY: help build test proto clean run docker-build docker-run deps fmt lint

help: ## Mostrar ajuda
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

proto: ## Gerar código Go a partir do protobuf
	@echo "Gerando código Go a partir do protobuf..."
	$(DOCKER_RUN) sh -c "apt-get update && apt-get install -y protobuf-compiler && \
		go install google.golang.org/protobuf/cmd/protoc-gen-go@latest && \
		go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest && \
		protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		proto/mcpclient.proto"

build: ## Compilar o projeto
	@echo "Compilando..."
	$(DOCKER_RUN) go build -o bin/mcp-client-runtime cmd/server/main.go

test: ## Executar testes
	@echo "Executando testes..."
	$(DOCKER_RUN) go test -v ./...

clean: ## Limpar arquivos gerados
	@echo "Limpando..."
	rm -rf bin/
	rm -f proto/*.pb.go

run: build ## Executar o servidor
	@echo "Executando servidor..."
	./bin/mcp-client-runtime

docker-build: ## Build Docker image
	docker build -t agenthub-mcp-client-runtime:latest .

docker-run: docker-build ## Run Docker container
	docker run -p 50051:50051 -p 8080:8080 agenthub-mcp-client-runtime:latest

deps: ## Baixar dependências
	@echo "Baixando dependências..."
	$(DOCKER_RUN) sh -c "go mod download && go mod tidy"

fmt: ## Formatar código
	@echo "Formatando código..."
	$(DOCKER_RUN) go fmt ./...

lint: ## Executar linter
	@echo "Executando linter..."
	$(DOCKER_RUN) golangci-lint run

.DEFAULT_GOAL := help

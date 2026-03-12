GO_IMAGE := golang:1.21
DOCKER_RUN := docker run --rm -v $(PWD):/app -v $(HOME)/go/pkg/mod:/go/pkg/mod -w /app $(GO_IMAGE)

.PHONY: test build clean

test:
	$(DOCKER_RUN) go test ./...

build:
	$(DOCKER_RUN) go build -o bin/app .

clean:
	rm -rf bin/

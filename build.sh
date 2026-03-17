#!/bin/bash
GO_IMAGE="golang:1.24"

docker run --rm \
  -v "$(pwd)":/app \
  -v "$HOME/go/pkg/mod":/go/pkg/mod \
  -w /app \
  ${GO_IMAGE} \
  go "$@"

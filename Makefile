BINARY_LLM2P := bin/llmp2p
BINARY_DAEMON := bin/llmp2pd
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.0)
LDFLAGS := -s -w -X github.com/log0u7/llmp2p/internal/cli.version=$(VERSION)

.PHONY: all build test race vet lint fmt install clean

all: build

build:
	go build -ldflags '$(LDFLAGS)' -o $(BINARY_LLM2P) ./cmd/llmp2p
	go build -ldflags '$(LDFLAGS)' -o $(BINARY_DAEMON) ./cmd/llmp2pd

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

lint:
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run || { echo "golangci-lint not installed, running go vet only"; go vet ./...; }

fmt:
	gofmt -w .

install:
	go install -ldflags '$(LDFLAGS)' ./cmd/llmp2p
	go install -ldflags '$(LDFLAGS)' ./cmd/llmp2pd

clean:
	rm -rf bin

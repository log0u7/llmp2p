BINARY_LLM2P := bin/llmp2p
BINARY_DAEMON := bin/llmp2pd
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.0)
LDFLAGS := -s -w -X github.com/log0u7/llmp2p/internal/cli.version=$(VERSION)
GOLANGCI := $(shell command -v golangci-lint 2>/dev/null || echo $(HOME)/go/bin/golangci-lint)

.PHONY: all build test race vet lint fmt install clean dist

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
	@if [ -x "$(GOLANGCI)" ]; then \
		$(GOLANGCI) run; \
	else \
		echo "golangci-lint not found (go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2), running go vet only"; \
		go vet ./...; \
	fi

fmt:
	gofmt -w .

install:
	go install -ldflags '$(LDFLAGS)' ./cmd/llmp2p
	go install -ldflags '$(LDFLAGS)' ./cmd/llmp2pd

clean:
	rm -rf bin

DIST := dist
DIST_MATRIX := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: dist

dist:
	@mkdir -p $(DIST)
	@for target in $(DIST_MATRIX); do \
		os=$${target%/*}; arch=$${target#*/}; ext=""; \
		if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		for bin in llmp2p llmp2pd; do \
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' \
				-o $(DIST)/$$bin-$$os-$$arch$$ext ./cmd/$$bin || exit 1; \
		done; \
	done
	@ls -la $(DIST)

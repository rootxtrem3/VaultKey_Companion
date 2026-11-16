SHELL := /bin/bash
GO ?= go
VERSION ?= 0.1.0
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)

.PHONY: all build test race vet fmt check integration release clean

all: build

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/vaultkey ./cmd/vaultkey

test:
	$(GO) test ./... -count=1

race:
	$(GO) test -race ./... -count=1

vet:
	$(GO) vet ./...

fmt:
	@files=$$(gofmt -l .); \
	if [ -n "$$files" ]; then \
		echo "gofmt needed:"; echo "$$files"; exit 1; \
	fi

check: fmt vet test race

integration:
	./test/integration/run.sh

release:
	./scripts/build-release.sh

clean:
	rm -rf bin dist

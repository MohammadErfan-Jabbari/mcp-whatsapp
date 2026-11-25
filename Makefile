.PHONY: build test test-race test-cover lint vet fmt clean login serve bridge smoke e2e upgrade-check

# fmt uses process substitution; force bash so `make lint` works under a POSIX /bin/sh
SHELL := /bin/bash

BIN := ./bin/whatsapp-mcp
PKGS := ./...
GO := go
# sqlite_fts5 compiles SQLite's FTS5 module into go-sqlite3; search_messages
# needs it, and the store refuses to open without it.
TAGS := sqlite_fts5
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.Version=$(VERSION)

build:
	mkdir -p bin
	$(GO) build -tags=$(TAGS) -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/whatsapp-mcp

test:
	$(GO) test -tags=$(TAGS) $(PKGS)

test-race:
	$(GO) test -tags=$(TAGS) -race $(PKGS)

test-cover:
	$(GO) test -tags=$(TAGS) -race -coverprofile=cover.out $(PKGS)
	$(GO) tool cover -func=cover.out | tail -1

vet:
	$(GO) vet -tags=$(TAGS) $(PKGS)

fmt:
	@diff -u <(echo -n) <(gofmt -d .) || (echo "gofmt differences found"; exit 1)

lint: vet fmt

e2e: build
	$(GO) test -tags=e2e,$(TAGS) ./e2e/...

login: build
	$(BIN) login

serve: build
	$(BIN) serve

bridge: build
	$(BIN) bridge

smoke: build
	$(BIN) smoke

clean:
	rm -rf bin cover.out

upgrade-check:
	$(GO) get go.mau.fi/whatsmeow@main
	$(GO) mod tidy
	$(GO) build -tags=$(TAGS) ./...
	$(GO) test -tags=$(TAGS) $(PKGS)

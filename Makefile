GO ?= go
GOLANGCI_LINT ?= golangci-lint
OUTPUT ?= loop-tui
VERSION ?= dev

all: build

build:
	CGO_ENABLED=0 $(GO) build -mod=readonly -trimpath -ldflags='-s -w -X main.version=$(VERSION)' -o "$(OUTPUT).new" ./cmd/loop-tui
	mv -f "$(OUTPUT).new" "$(OUTPUT)"

check:
	$(GO) test -race -mod=readonly ./...
	$(GO) vet -mod=readonly ./...

lint:
	$(GOLANGCI_LINT) run --timeout=10m

fmt:
	$(GOLANGCI_LINT) fmt

.PHONY: all build check lint fmt

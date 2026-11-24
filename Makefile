GO ?= go
OUTPUT ?= loop-tui
VERSION ?= dev

build:
	CGO_ENABLED=0 $(GO) build -mod=readonly -trimpath -ldflags='-s -w -X main.version=$(VERSION)' -o "$(OUTPUT).new" .
	mv -f "$(OUTPUT).new" "$(OUTPUT)"

check:
	$(GO) test -race -mod=readonly ./...
	$(GO) vet -mod=readonly ./...

.PHONY: build check

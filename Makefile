.PHONY: build install run test fmt lint clean

BINARY  := mbx
PKG     := ./cmd/mbx
VERSION := $(shell git describe --tags --dirty --always 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

build:
	go build $(LDFLAGS) -o dist/$(BINARY) $(PKG)

install:
	@go install $(LDFLAGS) $(PKG)
	@bin="$$(go env GOBIN)"; [ -n "$$bin" ] || bin="$$(go env GOPATH)/bin"; \
		echo "installed $(BINARY) to $$bin/$(BINARY)"

run:
	go run $(PKG)

test:
	go test ./...

fmt:
	gofmt -w .
	go mod tidy

lint:
	go vet ./...

clean:
	rm -rf dist/

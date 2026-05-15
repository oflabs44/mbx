.PHONY: build install run test fmt lint clean

BINARY := mbx
PKG    := ./cmd/mbx

build:
	go build -o dist/$(BINARY) $(PKG)

install:
	@go install $(PKG)
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

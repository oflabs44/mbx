.PHONY: build install run test fmt lint clean

BINARY := mbx
PKG    := ./cmd/mbx

build:
	go build -o dist/$(BINARY) $(PKG)

install:
	go install $(PKG)

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

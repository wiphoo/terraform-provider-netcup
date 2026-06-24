.PHONY: build test lint fmt generate

build:
	go build ./...

test:
	go test ./...

lint:
	golangci-lint run

fmt:
	gofmt -w .

generate:
	go generate ./...

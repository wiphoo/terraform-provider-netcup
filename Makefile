.PHONY: test lint fmt generate

test:
	go test ./...

lint:
	golangci-lint run

fmt:
	gofmt -w .

generate:
	go generate ./...

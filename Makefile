BIN_DIR := bin

.PHONY: build compile test lint fmt generate

# build produces the runnable netcupctl binary under bin/.
build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/netcupctl ./cmd/netcupctl

# compile type-checks every package without emitting binaries.
compile:
	go build ./...

test:
	go test ./...

lint:
	golangci-lint run

fmt:
	gofmt -w .

generate:
	go generate ./...

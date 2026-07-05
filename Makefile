BIN_DIR := bin

.PHONY: build compile test lint fmt generate acc acc-record

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

# acc runs live acceptance tests against the SCP API. It requires
# TF_ACC=1 and valid credentials (NETCUP_ACCESS_TOKEN). Before sub-issue #42
# (32-D) lands there are no _acc_test.go files, so this is a no-op.
acc:
	@if find . -name '*_acc_test.go' -not -path './vendor/*' | grep -q .; then \
		TF_ACC=1 go test ./...; \
	else \
		echo "No acceptance tests found yet — see issue #42 (32-D)."; \
	fi

# acc-record regenerates all go-vcr cassettes from live SCP. Requires
# NETCUP_ACCESS_TOKEN, NETCUP_TEST_SERVER_ID (for servers), and
# NETCUP_TEST_IP (for rDNS). Guarded explicitly rather than relying on
# NewClient's own check: today, with only the self-test (which skips itself
# under VCR_RECORD=1) and a pure unit test in tests/vcr/, no test actually
# calls NewClient in record mode, so `go test` would otherwise exit 0
# without ever validating credentials are present.
acc-record:
	@if [ -z "$$NETCUP_ACCESS_TOKEN" ]; then \
		echo "make acc-record requires NETCUP_ACCESS_TOKEN (see CONTRIBUTING.md)."; \
		exit 1; \
	fi
	VCR_RECORD=1 go test ./tests/vcr/...

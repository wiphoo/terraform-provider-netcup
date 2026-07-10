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
# TF_ACC=1, NETCUP_ACCESS_TOKEN, NETCUP_TEST_SERVER_ID (for server data
# source), and NETCUP_TEST_IP (for rDNS resource).
acc:
	@if [ -z "$$NETCUP_ACCESS_TOKEN" ]; then \
		echo "make acc requires NETCUP_ACCESS_TOKEN (see CONTRIBUTING.md)."; \
		exit 1; \
	fi
	@if [ -z "$$NETCUP_TEST_SERVER_ID" ]; then \
		echo "make acc requires NETCUP_TEST_SERVER_ID (see CONTRIBUTING.md)."; \
		exit 1; \
	fi
	@if [ -z "$$NETCUP_TEST_IP" ]; then \
		echo "make acc requires NETCUP_TEST_IP (see CONTRIBUTING.md)."; \
		exit 1; \
	fi
	TF_ACC=1 VCR_RECORD= go test -count=1 ./...

# acc-record regenerates all go-vcr cassettes from live SCP — both the
# SDK-level cassettes (tests/vcr/testdata/cassettes/) and the provider-tier
# ones (internal/provider/testdata/cassettes/). Requires NETCUP_ACCESS_TOKEN,
# NETCUP_TEST_SERVER_ID (for servers), and NETCUP_TEST_IP (for rDNS).
#
# The rDNS tests set/delete NETCUP_TEST_IP's live PTR while recording; each
# package's TestMain (vcr.RunWithRDNSRestore) captures that IP's original PTR
# before the run and restores it afterward, so recording does not leave the
# maintainer's reverse DNS cleared. -p 1 keeps packages serial so the restore
# is well-ordered.
acc-record:
	@if [ -z "$$NETCUP_ACCESS_TOKEN" ]; then \
		echo "make acc-record requires NETCUP_ACCESS_TOKEN (see CONTRIBUTING.md)."; \
		exit 1; \
	fi
	@if [ -z "$$NETCUP_TEST_SERVER_ID" ]; then \
		echo "make acc-record requires NETCUP_TEST_SERVER_ID (see CONTRIBUTING.md)."; \
		exit 1; \
	fi
	@if [ -z "$$NETCUP_TEST_IP" ]; then \
		echo "make acc-record requires NETCUP_TEST_IP (see CONTRIBUTING.md)."; \
		exit 1; \
	fi
	TF_ACC= VCR_RECORD=1 go test -count=1 -p 1 ./...

GOLANGCI_LINT_VERSION ?= v2.12.2
GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
GOVULNCHECK_VERSION ?= v1.6.0
GOVULNCHECK ?= go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
CARGO ?= cargo
CARGO_DENY ?= cargo deny
RUST_ROOT ?= services/collaboration
IDL_COMPAT_BASE ?= HEAD^
KC_RUST_GATE ?= 1
# Cap local/CI compile parallelism at three quarters of host CPUs.
# 将本地/CI 编译并行度限制为宿主机 CPU 的四分之三。
BUILD_JOBS ?= $(shell nproc 2>/dev/null | awk '{v=int($$1*3/4); if (v<1) v=1; print v}')

# Keep the Node interoperability fixture and its dependency tree out of Go discovery.
GO_PACKAGES ?= \
	./pkg/... \
	./services/gateway/... \
	./services/identity/... \
	./services/knowledge/... \
	./kitex_gen/... \
	./scripts/...

ifneq ($(strip $(ComSpec)),)
CODEGEN = powershell -NoProfile -ExecutionPolicy Bypass -File scripts/codegen.ps1
CODEGEN_CHECK = $(CODEGEN) -Check
CODEGEN_GO_CHECK = $(CODEGEN) -Check -Scope Go
CODEGEN_RUST_CHECK = $(CODEGEN) -Check -Scope Rust
else
CODEGEN = bash scripts/codegen.sh
CODEGEN_CHECK = $(CODEGEN) --check
CODEGEN_GO_CHECK = $(CODEGEN) --check --go-only
CODEGEN_RUST_CHECK = $(CODEGEN) --check --rust-only
endif

.DEFAULT_GOAL := help

.PHONY: help fmt fmt-check vet lint line test race build vuln supply-chain tidy generate generate-check generate-go-check generate-rust-check check go-ci rust-ci ci smoke-ci

help:
	@echo Knowledge Core development targets:
	@echo   make fmt             Format all Go and Rust packages
	@echo   make fmt-check       Check Go and Rust formatting without changing files
	@echo   make vet             Run go vet
	@echo   make lint            Run golangci-lint and Clippy
	@echo   make line            Alias for make lint
	@echo   make test            Run all Go and Rust tests without cached Go results
	@echo   make race            Run all Go tests with the race detector
	@echo   make build           Compile all Go packages and the release Rust workspace
	@echo   make vuln            Check reachable Go vulnerabilities
	@echo   make supply-chain    Check Rust advisories, bans, licenses, and sources
	@echo   make tidy            Normalize go.mod and go.sum
	@echo   make generate        Regenerate Hertz and Kitex code
	@echo   make generate-check  Regenerate and fail on generated-code drift
	@echo   make check           Run Go gates and the Rust gate when enabled
	@echo   make ci              Run check plus generated-code drift detection

fmt:
	go fmt $(GO_PACKAGES)
	cd $(RUST_ROOT) && $(CARGO) fmt --all

fmt-check:
	$(GOLANGCI_LINT) fmt --diff
	cd $(RUST_ROOT) && $(CARGO) fmt --all --check

vet:
	GOMAXPROCS=$(BUILD_JOBS) go vet $(GO_PACKAGES)

lint:
	GOMAXPROCS=$(BUILD_JOBS) $(GOLANGCI_LINT) run $(GO_PACKAGES)
	cd $(RUST_ROOT) && CARGO_BUILD_JOBS=$(BUILD_JOBS) $(CARGO) clippy --workspace --all-targets --all-features --locked -j $(BUILD_JOBS) -- -D warnings

line: lint

test:
	GOMAXPROCS=$(BUILD_JOBS) go test -count=1 $(GO_PACKAGES)
	cd $(RUST_ROOT) && CARGO_BUILD_JOBS=$(BUILD_JOBS) $(CARGO) test --workspace --all-targets --all-features --locked -j $(BUILD_JOBS)

race:
	GOMAXPROCS=$(BUILD_JOBS) go test -race -count=1 $(GO_PACKAGES)

build:
	GOMAXPROCS=$(BUILD_JOBS) go build $(GO_PACKAGES)
	cd $(RUST_ROOT) && CARGO_BUILD_JOBS=$(BUILD_JOBS) $(CARGO) build --workspace --release --locked -j $(BUILD_JOBS)

vuln:
	GOMAXPROCS=$(BUILD_JOBS) $(GOVULNCHECK) $(GO_PACKAGES)

supply-chain:
	cd $(RUST_ROOT) && $(CARGO_DENY) check advisories bans licenses sources

tidy:
	go mod tidy

generate:
	$(CODEGEN)

ifeq ($(KC_RUST_GATE),0)
generate-check:
	$(CODEGEN_GO_CHECK)
else
generate-check:
	$(CODEGEN_CHECK)
endif

generate-go-check:
	$(CODEGEN_GO_CHECK)

generate-rust-check:
	$(CODEGEN_RUST_CHECK)

go-ci:
	$(GOLANGCI_LINT) fmt --diff
	GOMAXPROCS=$(BUILD_JOBS) go vet $(GO_PACKAGES)
	GOMAXPROCS=$(BUILD_JOBS) $(GOLANGCI_LINT) run $(GO_PACKAGES)
	GOMAXPROCS=$(BUILD_JOBS) go test -count=1 $(GO_PACKAGES)
	GOMAXPROCS=$(BUILD_JOBS) go build $(GO_PACKAGES)
	GOMAXPROCS=$(BUILD_JOBS) $(GOVULNCHECK) $(GO_PACKAGES)

rust-ci:
	cd $(RUST_ROOT) && $(CARGO) fmt --all --check
	cd $(RUST_ROOT) && CARGO_BUILD_JOBS=$(BUILD_JOBS) $(CARGO) clippy --workspace --all-features --locked -j $(BUILD_JOBS) -- -D warnings
	cd $(RUST_ROOT) && CARGO_BUILD_JOBS=$(BUILD_JOBS) $(CARGO) test --workspace --all-targets --all-features --locked -j $(BUILD_JOBS)
	cd $(RUST_ROOT) && $(CARGO_DENY) check advisories bans licenses sources

ifeq ($(KC_RUST_GATE),0)
check: go-ci
else
check: go-ci rust-ci
endif

ci: check generate-check

smoke-ci:
	@test -n "$(KC_SMOKE_BASE_URL)" || (echo "KC_SMOKE_BASE_URL is required" >&2; exit 1)
	curl --fail --silent --show-error "$(KC_SMOKE_BASE_URL)/health/ready"

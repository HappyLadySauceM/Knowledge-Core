GOLANGCI_LINT_VERSION ?= v2.12.2
GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
GOVULNCHECK_VERSION ?= v1.6.0
GOVULNCHECK ?= go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
CARGO ?= cargo
CARGO_DENY ?= cargo deny
RUST_ROOT ?= services/collaboration
IDL_COMPAT_BASE ?= HEAD^
KC_RUST_GATE ?= 1
# The checked-in vendor tree is intentionally not authoritative in CI; resolve
# Go dependencies from go.mod/go.sum instead of failing on stale vendor metadata.
# CI 中以 go.mod/go.sum 为准，避免旧 vendor/modules.txt 阻断验证流水线。
GOFLAGS ?= -mod=mod
export GOFLAGS
# Cap local/CI compile parallelism at three CPUs and respect cgroup affinity.
# 将本地/CI 编译并行度限制为三个 CPU，并尊重 cgroup affinity。
BUILD_JOBS ?= $(shell nproc 2>/dev/null | awk '{v=$$1; if (v>3) v=3; if (v<1) v=1; print v}')
GO_RELEASE_SERVICES ?= gateway identity knowledge attachment platform
GO_ARTIFACT_DIR ?= .ci-artifacts
RUST_ARTIFACT_DIR ?= .ci-artifacts
RUST_TARGET_DIR ?= $(if $(CARGO_TARGET_DIR),$(CARGO_TARGET_DIR),$(RUST_ROOT)/target)

# Keep the Node interoperability fixture and its dependency tree out of Go discovery.
GO_PACKAGES ?= \
	./pkg/... \
	./services/gateway/... \
	./services/identity/... \
	./services/knowledge/... \
	./services/attachment/... \
	./services/platform/... \
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

.PHONY: help fmt fmt-check vet lint line test race build go-release rust-release vuln supply-chain tidy generate generate-check generate-go-check generate-rust-check check go-ci rust-ci ci smoke-ci

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

build: go-release
ifeq ($(KC_RUST_GATE),0)
	@true
else
build: rust-release
endif

go-release:
	@set -eu; mkdir -p "$(GO_ARTIFACT_DIR)"; \
	for service in $(GO_RELEASE_SERVICES); do \
		case "$$service" in \
			gateway|identity|knowledge|attachment|platform) ;; \
			*) echo "unsupported Go service: $$service" >&2; exit 2 ;; \
		esac; \
			CGO_ENABLED=0 GOOS=linux GOMAXPROCS=$(BUILD_JOBS) go build -mod=mod -trimpath -buildvcs=false -ldflags="-s -w" -o "$(GO_ARTIFACT_DIR)/$$service" "./services/$$service"; \
	done

rust-release:
	@set -eu; mkdir -p "$(RUST_ARTIFACT_DIR)"; \
	CARGO_BUILD_JOBS=$(BUILD_JOBS) $(CARGO) build --manifest-path $(RUST_ROOT)/Cargo.toml --workspace --release --locked -j $(BUILD_JOBS); \
	cp "$(RUST_TARGET_DIR)/release/knowledge-core-collaboration" "$(RUST_ARTIFACT_DIR)/collaboration"

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

ci: check generate-check build

smoke-ci:
	@test -n "$(KC_SMOKE_BASE_URL)" || (echo "KC_SMOKE_BASE_URL is required" >&2; exit 1)
	curl --fail --silent --show-error "$(KC_SMOKE_BASE_URL)/health/ready"

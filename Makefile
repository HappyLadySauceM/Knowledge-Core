GOLANGCI_LINT_VERSION ?= v2.12.2
GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
GOVULNCHECK_VERSION ?= v1.6.0
GOVULNCHECK ?= go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
KITEX_VERSION ?= v0.16.2
HZ_VERSION ?= v0.9.7
THRIFTGO_VERSION ?= 0.4.5
CARGO ?= cargo
CARGO_DENY ?= cargo deny
CARGO_DENY_VERSION ?= 0.20.2
RUST_TOOLCHAIN ?= 1.97.1
RUST_ROOT ?= services/collaboration
# Keep freshly installed CLIs visible to later ci targets.
# 把刚安装的 CLI 加入后续 ci target 的 PATH。
GOBIN_DIR := $(shell go env GOPATH)/bin
CARGO_BIN_DIR := $(if $(CARGO_HOME),$(CARGO_HOME)/bin,$(HOME)/.cargo/bin)
export PATH := $(GOBIN_DIR):$(CARGO_BIN_DIR):$(PATH)
IDL_COMPAT_BASE ?= HEAD^
KC_RUST_GATE ?= 1
# The checked-in vendor tree is intentionally not authoritative in CI; resolve
# Go dependencies from go.mod/go.sum instead of failing on stale vendor metadata.
# CI 中以 go.mod/go.sum 为准，避免旧 vendor/modules.txt 阻断验证流水线。
GOFLAGS ?= -mod=mod
export GOFLAGS
# Derive local/CI compile parallelism from effective CPUs (nproc capped by
# cgroup quota), not the node's raw CPU count. ARC standard runners are
# 8 CPU / 8Gi; nproc still sees the host and golangci-lint/cargo thrash.
# BUILD_JOBS remains an explicit emergency override.
# 按有效 CPU（nproc 并被 cgroup 配额封顶）计算并行度，不用节点裸核心数。
# ARC 标准池为 8 CPU / 8Gi；nproc 仍看到宿主机，golangci-lint/cargo 会过载。
# BUILD_JOBS 仍可显式覆盖。
BUILD_CPU_PERCENT ?= 75
export BUILD_CPU_PERCENT
ifeq ($(shell test "$(BUILD_CPU_PERCENT)" -ge 1 2>/dev/null && test "$(BUILD_CPU_PERCENT)" -le 100 2>/dev/null && echo valid),)
$(error BUILD_CPU_PERCENT must be an integer from 1 to 100)
endif
AVAILABLE_CPUS ?= $(shell \
	n=$$(nproc 2>/dev/null || echo 1); \
	quota_cpus=""; \
	if [ -r /sys/fs/cgroup/cpu.max ]; then \
		quota=$$(awk '{print $$1}' /sys/fs/cgroup/cpu.max); \
		period=$$(awk '{print $$2}' /sys/fs/cgroup/cpu.max); \
		if [ "$$quota" != "max" ] && [ -n "$$period" ] && [ "$$period" -gt 0 ] 2>/dev/null; then \
			quota_cpus=$$((quota / period)); \
			if [ "$$quota_cpus" -lt 1 ]; then quota_cpus=1; fi; \
		fi; \
	elif [ -r /sys/fs/cgroup/cpu/cpu.cfs_quota_us ] && [ -r /sys/fs/cgroup/cpu/cpu.cfs_period_us ]; then \
		quota=$$(cat /sys/fs/cgroup/cpu/cpu.cfs_quota_us); \
		period=$$(cat /sys/fs/cgroup/cpu/cpu.cfs_period_us); \
		if [ "$$quota" -gt 0 ] && [ "$$period" -gt 0 ]; then \
			quota_cpus=$$((quota / period)); \
			if [ "$$quota_cpus" -lt 1 ]; then quota_cpus=1; fi; \
		fi; \
	fi; \
	if [ -n "$$quota_cpus" ] && [ "$$quota_cpus" -lt "$$n" ]; then n=$$quota_cpus; fi; \
	echo $$n)
BUILD_JOBS ?= $(shell echo $(AVAILABLE_CPUS) | awk -v p="$(BUILD_CPU_PERCENT)" '{v=int(($$1*p)/100); if (v<1) v=1; print v}')
CARGO_BUILD_JOBS ?= $(BUILD_JOBS)
export CARGO_BUILD_JOBS
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

.PHONY: help fmt fmt-check vet lint line test race build go-release rust-release vuln supply-chain tidy ensure-ci-tools ensure-kitex ensure-hz ensure-thriftgo ensure-cargo-deny ensure-rust-toolchain generate generate-check generate-go-check generate-rust-check check go-ci rust-ci ci smoke-ci

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
	@echo   make ensure-ci-tools Install missing or older CI tools; keep newer local versions
	@echo   make generate        Regenerate Hertz and Kitex code
	@echo   make generate-check  Regenerate and fail on generated-code drift
	@echo   make check           Run Go gates and the Rust gate when enabled
	@echo   make ci              Tidy modules, ensure CI tools, then check, generate-check, and build

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

# Compare a parsed x.y.z against a pin after stripping an optional v prefix.
# 去掉可选 v 前缀后，比较解析出的 x.y.z 与钉住版本。
# HAVE >= NEED keeps the local binary; otherwise install the pin.
# HAVE >= NEED 时保留本机二进制，否则安装钉住版本。
define ENSURE_GO_CLI
	@set -euo pipefail; \
	name="$(1)"; \
	need="$(2)"; \
	module="$(3)"; \
	need_norm="$${need#v}"; \
	if command -v "$$name" >/dev/null 2>&1; then \
		output="$$("$$name" --version 2>&1 || true)"; \
		have="$$(printf '%s\n' "$$output" | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -n 1 || true)"; \
		if [ -n "$$have" ]; then \
			lowest="$$(printf '%s\n' "$$need_norm" "$$have" | sort -V | head -n 1)"; \
			if [ "$$lowest" = "$$need_norm" ]; then \
				echo "$$name $$have satisfies $$need"; \
				exit 0; \
			fi; \
			echo "$$name $$have is older than $$need; upgrading"; \
		else \
			echo "could not parse $$name version from: $$output; reinstalling $$need"; \
		fi; \
	else \
		echo "$$name is missing; installing $$need"; \
	fi; \
	go install "$$module"
endef

ensure-kitex:
	$(call ENSURE_GO_CLI,kitex,$(KITEX_VERSION),github.com/cloudwego/kitex/tool/cmd/kitex@$(KITEX_VERSION))

ensure-hz:
	$(call ENSURE_GO_CLI,hz,$(HZ_VERSION),github.com/cloudwego/hertz/cmd/hz@$(HZ_VERSION))

ensure-thriftgo:
	$(call ENSURE_GO_CLI,thriftgo,$(THRIFTGO_VERSION),github.com/cloudwego/thriftgo@v$(THRIFTGO_VERSION))

ensure-cargo-deny:
	@set -euo pipefail; \
	need="$(CARGO_DENY_VERSION)"; \
	need_norm="$${need#v}"; \
	if command -v cargo-deny >/dev/null 2>&1 || $(CARGO) deny --version >/dev/null 2>&1; then \
		output="$$($(CARGO) deny --version 2>&1 || true)"; \
		have="$$(printf '%s\n' "$$output" | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -n 1 || true)"; \
		if [ -n "$$have" ]; then \
			lowest="$$(printf '%s\n' "$$need_norm" "$$have" | sort -V | head -n 1)"; \
			if [ "$$lowest" = "$$need_norm" ]; then \
				echo "cargo-deny $$have satisfies $$need"; \
				exit 0; \
			fi; \
			echo "cargo-deny $$have is older than $$need; upgrading"; \
		else \
			echo "could not parse cargo-deny version from: $$output; reinstalling $$need"; \
		fi; \
	else \
		echo "cargo-deny is missing; installing $$need"; \
	fi; \
	$(CARGO) install cargo-deny --locked --version "$$need"

ensure-rust-toolchain:
	@set -euo pipefail; \
	toolchain="$(RUST_TOOLCHAIN)"; \
	if ! command -v rustup >/dev/null 2>&1; then \
		echo "rustup is required to install Rust $$toolchain" >&2; \
		exit 1; \
	fi; \
	if rustup toolchain list | grep -Eq "^$${toolchain}(-| )"; then \
		echo "rustc $$toolchain is installed"; \
	else \
		echo "rustc $$toolchain is missing; installing"; \
		rustup toolchain install "$$toolchain" --profile minimal; \
	fi; \
	for component in clippy rustfmt; do \
		if rustup component list --toolchain "$$toolchain" | grep -Eq "^$${component}-.* \(installed\)$$"; then \
			echo "$$component for $$toolchain is installed"; \
		else \
			echo "$$component for $$toolchain is missing; installing"; \
			rustup component add "$$component" --toolchain "$$toolchain"; \
		fi; \
	done

ensure-ci-tools: ensure-kitex ensure-hz ensure-thriftgo ensure-cargo-deny ensure-rust-toolchain

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
	@set -eu; \
	for attempt in 1 2 3; do \
	  audit_log="$$(mktemp)"; \
	  if GOMAXPROCS=$(BUILD_JOBS) $(GOVULNCHECK) $(GO_PACKAGES) >"$$audit_log" 2>&1; then \
	    cat "$$audit_log"; rm -f "$$audit_log"; break; \
	  fi; \
	  cat "$$audit_log" >&2; \
	  if ! grep -Eiq 'TLS handshake timeout|i/o timeout|connection reset|connection refused|temporary failure|no such host|server misbehaving|context deadline exceeded' "$$audit_log"; then \
	    rm -f "$$audit_log"; exit 1; \
	  fi; \
	  rm -f "$$audit_log"; \
	  if [ "$$attempt" = 3 ]; then \
	    echo "govulncheck failed after transient-network retries" >&2; exit 1; \
	  fi; \
	  echo "::warning::govulncheck transport failure; retrying (attempt $$attempt/3)"; \
	  sleep "$$attempt"; \
	done

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

# Run tidy and tool bootstrap sequentially so `make -j ci` cannot race them.
# 顺序执行 tidy 和工具预检，避免 `make -j ci` 把它们并行打乱。
ci:
	$(MAKE) tidy
	$(MAKE) ensure-ci-tools
	$(MAKE) check
	$(MAKE) generate-check
	$(MAKE) build

smoke-ci:
	@test -n "$(KC_SMOKE_BASE_URL)" || (echo "KC_SMOKE_BASE_URL is required" >&2; exit 1)
	curl --fail --silent --show-error "$(KC_SMOKE_BASE_URL)/health/ready"

GOLANGCI_LINT_VERSION ?= v2.12.2
TOOLS_BIN := .tools/bin/$(GOLANGCI_LINT_VERSION)

ifneq ($(strip $(ComSpec)),)
EXE := .exe
INSTALL_GOLANGCI_LINT = powershell -NoProfile -ExecutionPolicy Bypass -File scripts/install-tools.ps1 -Version $(GOLANGCI_LINT_VERSION) -OutputDirectory "$(TOOLS_BIN)"
CODEGEN = powershell -NoProfile -ExecutionPolicy Bypass -File scripts/codegen.ps1
else
EXE :=
INSTALL_GOLANGCI_LINT = bash scripts/install-tools.sh $(GOLANGCI_LINT_VERSION) "$(TOOLS_BIN)"
CODEGEN = bash scripts/codegen.sh
endif

GOLANGCI_LINT := $(TOOLS_BIN)/golangci-lint$(EXE)

.DEFAULT_GOAL := help

.PHONY: help tools fmt fmt-check vet lint line test build tidy generate generate-check check ci

help:
	@echo Knowledge Core development targets:
	@echo   make tools           Install pinned development tools locally
	@echo   make fmt             Format all Go packages
	@echo   make fmt-check       Check formatting without changing files
	@echo   make vet             Run go vet
	@echo   make lint            Run golangci-lint
	@echo   make line            Alias for make lint
	@echo   make test            Run all tests without cached results
	@echo   make build           Compile all packages
	@echo   make tidy            Normalize go.mod and go.sum
	@echo   make generate        Regenerate Hertz and Kitex code
	@echo   make generate-check  Regenerate and fail on generated-code drift
	@echo   make check           Run formatting, vet, lint, tests, and build
	@echo   make ci              Run check plus generated-code drift detection

tools: $(GOLANGCI_LINT)

$(GOLANGCI_LINT):
	@echo "Installing golangci-lint $(GOLANGCI_LINT_VERSION)..."
	@$(INSTALL_GOLANGCI_LINT)

fmt:
	go fmt ./...

fmt-check: $(GOLANGCI_LINT)
	"$(GOLANGCI_LINT)" fmt --diff

vet:
	go vet ./...

lint: $(GOLANGCI_LINT)
	"$(GOLANGCI_LINT)" run ./...

line: lint

test:
	go test -count=1 ./...

build:
	go build ./...

tidy:
	go mod tidy

generate:
	$(CODEGEN)

generate-check: generate
	git diff --exit-code -- kitex_gen services/gateway/biz

check: fmt-check vet lint test build

ci: check generate-check

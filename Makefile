GOLANGCI_LINT_VERSION ?= v2.12.2
GOLANGCI_LINT = go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

ifneq ($(strip $(ComSpec)),)
CODEGEN = powershell -NoProfile -ExecutionPolicy Bypass -File scripts/codegen.ps1
CODEGEN_CHECK = $(CODEGEN) -Check
else
CODEGEN = bash scripts/codegen.sh
CODEGEN_CHECK = $(CODEGEN) --check
endif

.DEFAULT_GOAL := help

.PHONY: help fmt fmt-check vet lint line test build tidy generate generate-check check ci

help:
	@echo Knowledge Core development targets:
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

fmt:
	go fmt ./...

fmt-check:
	$(GOLANGCI_LINT) fmt --diff

vet:
	go vet ./...

lint:
	$(GOLANGCI_LINT) run ./...

line: lint

test:
	go test -count=1 ./...

build:
	go build ./...

tidy:
	go mod tidy

generate:
	$(CODEGEN)

generate-check:
	$(CODEGEN_CHECK)

check: fmt-check vet lint test build

ci: check generate-check

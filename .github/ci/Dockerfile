FROM golang:1.26.1-bookworm@sha256:ab3d6955bbc813a0f3fdf220c1d817dd89c0b3f283777db8ece4a32fe7858edd

ARG GOPROXY=https://goproxy.cn,direct

LABEL org.opencontainers.image.source="https://github.com/HappyLadySauceM/Knowledge-Core"
LABEL org.opencontainers.image.description="Reproducible CI toolchain for Knowledge Core"

ENV CGO_ENABLED=1 \
    GOTOOLCHAIN=auto \
    GOLANGCI_LINT=/usr/local/bin/golangci-lint \
    GOVULNCHECK=/usr/local/bin/govulncheck

RUN set -eux; \
    GOPROXY="$GOPROXY" GOBIN=/usr/local/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2; \
    GOPROXY="$GOPROXY" GOBIN=/usr/local/bin go install golang.org/x/vuln/cmd/govulncheck@v1.6.0; \
    GOPROXY="$GOPROXY" GOBIN=/usr/local/bin go install github.com/cloudwego/kitex/tool/cmd/kitex@v0.16.2; \
    GOPROXY="$GOPROXY" GOBIN=/usr/local/bin go install github.com/cloudwego/hertz/cmd/hz@v0.9.7; \
    GOPROXY="$GOPROXY" GOBIN=/usr/local/bin go install github.com/cloudwego/thriftgo@v0.4.5; \
    golangci-lint version; \
    govulncheck -version; \
    command -v kitex hz thriftgo govulncheck; \
    go clean -cache -modcache

WORKDIR /workspace

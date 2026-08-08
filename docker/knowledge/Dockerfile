# syntax=docker/dockerfile:1.7
ARG GO_BASE_IMAGE=harbor.happyladysauce.local/knowledge-core/base/golang:1.26.5-alpine
ARG RUNTIME_BASE_IMAGE=harbor.happyladysauce.local/knowledge-core/base/distroless-static-debian12:nonroot
FROM --platform=$BUILDPLATFORM ${GO_BASE_IMAGE} AS build
ARG GOPROXY=https://goproxy.cn,direct
ARG TARGETOS=linux
ARG TARGETARCH
ENV GOPROXY=${GOPROXY}
RUN apk add --no-cache ca-certificates git
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY pkg ./pkg
COPY kitex_gen/collaboration ./kitex_gen/collaboration
COPY kitex_gen/common ./kitex_gen/common
COPY kitex_gen/identity ./kitex_gen/identity
COPY kitex_gen/knowledge ./kitex_gen/knowledge
COPY services/knowledge ./services/knowledge
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -mod=readonly -trimpath -buildvcs=false -ldflags="-s -w" -o /out/knowledge ./services/knowledge
FROM ${RUNTIME_BASE_IMAGE}
WORKDIR /app
COPY --from=build --chown=nonroot:nonroot /out/knowledge /app/knowledge
COPY --from=build --chown=nonroot:nonroot /src/services/knowledge/etc/config.yaml /etc/knowledge-core/knowledge/config.yaml
EXPOSE 8882 8083
USER nonroot:nonroot
ENTRYPOINT ["/app/knowledge"]
CMD ["--config=/etc/knowledge-core/knowledge/config.yaml"]

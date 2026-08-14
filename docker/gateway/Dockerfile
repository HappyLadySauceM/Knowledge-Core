# syntax=docker/dockerfile:1.7
ARG GO_BASE_IMAGE=harbor.happyladysauce.local/knowledge-core/base/golang:1.26.6-alpine
ARG RUNTIME_BASE_IMAGE=harbor.happyladysauce.local/knowledge-core/base/distroless-static-debian12:nonroot
FROM --platform=$BUILDPLATFORM ${GO_BASE_IMAGE} AS build
ARG GOPROXY=https://goproxy.cn,direct
ARG TARGETOS=linux
ARG TARGETARCH
ARG BUILD_JOBS=1
ENV GOPROXY=${GOPROXY}
ENV GOMAXPROCS=${BUILD_JOBS}
RUN apk add --no-cache ca-certificates git
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,id=kc-go-mod,target=/go/pkg/mod go mod download
COPY pkg ./pkg
COPY kitex_gen/collaboration ./kitex_gen/collaboration
COPY kitex_gen/common ./kitex_gen/common
COPY kitex_gen/identity ./kitex_gen/identity
COPY kitex_gen/knowledge ./kitex_gen/knowledge
COPY services/gateway ./services/gateway
RUN --mount=type=cache,id=kc-go-mod,target=/go/pkg/mod \
    --mount=type=cache,id=kc-go-build,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -mod=readonly -trimpath -buildvcs=false -ldflags="-s -w" -o /out/gateway ./services/gateway
FROM ${RUNTIME_BASE_IMAGE}
WORKDIR /app
COPY --from=build --chown=nonroot:nonroot /out/gateway /app/gateway
COPY --from=build --chown=nonroot:nonroot /src/services/gateway/etc/config.yaml /etc/knowledge-core/gateway/config.yaml
EXPOSE 8080 8082
USER nonroot:nonroot
ENTRYPOINT ["/app/gateway"]
CMD ["--config=/etc/knowledge-core/gateway/config.yaml"]

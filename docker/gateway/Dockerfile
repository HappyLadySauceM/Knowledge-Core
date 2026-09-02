ARG RUNTIME_BASE_IMAGE=harbor.happyladysauce.local/knowledge-core/base/ubuntu:24.04
FROM ${RUNTIME_BASE_IMAGE}
# Ubuntu 24.04 matches the ARC runner glibc. Static Go binaries only need CA certs.
# Ubuntu 24.04 与 ARC runner 的 glibc 一致。静态 Go 二进制只需 CA 证书。
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 65532 nonroot \
    && useradd --uid 65532 --gid nonroot --no-create-home --shell /usr/sbin/nologin nonroot \
    && mkdir -p /app \
    && chown 65532:65532 /app
WORKDIR /app
COPY --chown=65532:65532 .ci-artifacts/gateway /app/gateway
COPY --chown=65532:65532 services/gateway/etc/config.yaml /etc/knowledge-core/gateway/config.yaml
EXPOSE 8080 8082
USER 65532:65532
ENTRYPOINT ["/app/gateway"]
CMD ["--config=/etc/knowledge-core/gateway/config.yaml"]

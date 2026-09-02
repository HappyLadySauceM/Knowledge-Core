ARG RUNTIME_BASE_IMAGE=harbor.happyladysauce.local/knowledge-core/base/debian:bookworm-slim
FROM ${RUNTIME_BASE_IMAGE}
# The collaboration binary links OpenSSL from the GNU toolchain. debian-slim
# does not ship libssl3 unless we install it.
# Collaboration 二进制通过 GNU 工具链链接 OpenSSL；debian-slim 默认不带 libssl3。
RUN apt-get update && apt-get install --yes --no-install-recommends ca-certificates libssl3 \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 knowledge-core \
    && useradd --uid 10001 --gid knowledge-core --no-create-home --shell /usr/sbin/nologin knowledge-core
COPY --chown=10001:10001 .ci-artifacts/collaboration /usr/local/bin/knowledge-core-collaboration
RUN ldd /usr/local/bin/knowledge-core-collaboration > /tmp/knowledge-core-ldd \
    && if grep -q "not found" /tmp/knowledge-core-ldd; then cat /tmp/knowledge-core-ldd >&2; exit 1; fi \
    && rm /tmp/knowledge-core-ldd
EXPOSE 8091 8883 8084
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/knowledge-core-collaboration"]

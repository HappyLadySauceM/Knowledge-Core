ARG RUNTIME_BASE_IMAGE=harbor.happyladysauce.local/knowledge-core/base/debian:trixie-slim
FROM ${RUNTIME_BASE_IMAGE}
# ARC runners are Ubuntu 24.04 (glibc 2.39). bookworm-slim only has glibc 2.36,
# so a GNU-linked collaboration binary needs a newer runtime plus libssl3.
# ARC runner 是 Ubuntu 24.04（glibc 2.39）。bookworm-slim 只有 glibc 2.36，
# GNU 链接的 collaboration 二进制需要更新的运行时，并安装 libssl3。
RUN apt-get update && apt-get install --yes --no-install-recommends ca-certificates libssl3 \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 knowledge-core \
    && useradd --uid 10001 --gid knowledge-core --no-create-home --shell /usr/sbin/nologin knowledge-core
COPY --chown=10001:10001 .ci-artifacts/collaboration /usr/local/bin/knowledge-core-collaboration
RUN ldd /usr/local/bin/knowledge-core-collaboration > /tmp/knowledge-core-ldd 2>&1 \
    && if grep -qE ' => not found|version `GLIBC_' /tmp/knowledge-core-ldd; then cat /tmp/knowledge-core-ldd >&2; exit 1; fi \
    && rm /tmp/knowledge-core-ldd
EXPOSE 8091 8883 8084
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/knowledge-core-collaboration"]

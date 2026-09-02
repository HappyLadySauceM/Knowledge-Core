ARG RUNTIME_BASE_IMAGE=harbor.happyladysauce.local/knowledge-core/base/ubuntu:24.04
FROM ${RUNTIME_BASE_IMAGE}
# ARC runners and this runtime are Ubuntu 24.04 (glibc 2.39). nacos native-tls
# still needs libssl3t64 at runtime.
# ARC runner 与本运行时均为 Ubuntu 24.04（glibc 2.39）。nacos native-tls
# 运行时仍需要 libssl3t64。
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates libssl3t64 \
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

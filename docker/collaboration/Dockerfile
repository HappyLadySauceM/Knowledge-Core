# syntax=docker/dockerfile:1.7
ARG RUNTIME_BASE_IMAGE=harbor.happyladysauce.local/knowledge-core/base/debian:bookworm-slim
FROM ${RUNTIME_BASE_IMAGE}
RUN apt-get update && apt-get install --yes --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 knowledge-core \
    && useradd --uid 10001 --gid knowledge-core --no-create-home --shell /usr/sbin/nologin knowledge-core
COPY --chown=10001:10001 .ci-artifacts/collaboration /usr/local/bin/knowledge-core-collaboration
RUN ldd /usr/local/bin/knowledge-core-collaboration > /tmp/knowledge-core-ldd \
    && ! grep -q "not found" /tmp/knowledge-core-ldd \
    && rm /tmp/knowledge-core-ldd
EXPOSE 8091 8883 8084
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/knowledge-core-collaboration"]

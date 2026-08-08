# syntax=docker/dockerfile:1.7
ARG RUST_BASE_IMAGE=harbor.happyladysauce.local/knowledge-core/base/rust:1.97.1-bookworm
ARG RUNTIME_BASE_IMAGE=harbor.happyladysauce.local/knowledge-core/base/debian:bookworm-slim
FROM ${RUST_BASE_IMAGE} AS build
RUN apt-get update && apt-get install --yes --no-install-recommends ca-certificates pkg-config protobuf-compiler \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY services/collaboration/Cargo.toml services/collaboration/Cargo.lock services/collaboration/deny.toml services/collaboration/rust-toolchain.toml ./services/collaboration/
COPY services/collaboration/tools ./services/collaboration/tools
COPY services/collaboration/src ./services/collaboration/src
RUN cargo build --locked --release --manifest-path services/collaboration/Cargo.toml --bin knowledge-core-collaboration
FROM ${RUNTIME_BASE_IMAGE}
RUN apt-get update && apt-get install --yes --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 knowledge-core \
    && useradd --uid 10001 --gid knowledge-core --no-create-home --shell /usr/sbin/nologin knowledge-core
COPY --from=build --chown=10001:10001 /src/services/collaboration/target/release/knowledge-core-collaboration /usr/local/bin/knowledge-core-collaboration
EXPOSE 8091 8883 8084
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/knowledge-core-collaboration"]

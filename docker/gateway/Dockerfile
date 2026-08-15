# syntax=docker/dockerfile:1.7
ARG RUNTIME_BASE_IMAGE=harbor.happyladysauce.local/knowledge-core/base/distroless-static-debian12:nonroot
FROM ${RUNTIME_BASE_IMAGE}
WORKDIR /app
COPY --chown=nonroot:nonroot .ci-artifacts/gateway /app/gateway
COPY --chown=nonroot:nonroot services/gateway/etc/config.yaml /etc/knowledge-core/gateway/config.yaml
EXPOSE 8080 8082
USER nonroot:nonroot
ENTRYPOINT ["/app/gateway"]
CMD ["--config=/etc/knowledge-core/gateway/config.yaml"]

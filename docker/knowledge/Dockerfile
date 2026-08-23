ARG RUNTIME_BASE_IMAGE=harbor.happyladysauce.local/knowledge-core/base/distroless-static-debian12:nonroot
FROM ${RUNTIME_BASE_IMAGE}
WORKDIR /app
COPY --chown=nonroot:nonroot .ci-artifacts/knowledge /app/knowledge
COPY --chown=nonroot:nonroot services/knowledge/etc/config.yaml /etc/knowledge-core/knowledge/config.yaml
EXPOSE 8882 8083
USER nonroot:nonroot
ENTRYPOINT ["/app/knowledge"]
CMD ["--config=/etc/knowledge-core/knowledge/config.yaml"]

ARG RUNTIME_BASE_IMAGE=harbor.happyladysauce.local/knowledge-core/base/distroless-static-debian12:nonroot
FROM ${RUNTIME_BASE_IMAGE}
WORKDIR /app
COPY --chown=nonroot:nonroot .ci-artifacts/identity /app/identity
COPY --chown=nonroot:nonroot services/identity/etc/config.yaml /etc/knowledge-core/identity/config.yaml
EXPOSE 8881 8081
USER nonroot:nonroot
ENTRYPOINT ["/app/identity"]
CMD ["--config=/etc/knowledge-core/identity/config.yaml"]

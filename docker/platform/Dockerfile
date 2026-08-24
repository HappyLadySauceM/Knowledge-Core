ARG RUNTIME_BASE_IMAGE=harbor.happyladysauce.local/knowledge-core/base/distroless-static-debian12:nonroot
FROM ${RUNTIME_BASE_IMAGE}
WORKDIR /app
COPY --chown=nonroot:nonroot .ci-artifacts/platform /app/platform
COPY --chown=nonroot:nonroot services/platform/etc/config.yaml /etc/knowledge-core/platform/config.yaml
EXPOSE 8885 8086
USER nonroot:nonroot
ENTRYPOINT ["/app/platform"]
CMD ["--config=/etc/knowledge-core/platform/config.yaml"]

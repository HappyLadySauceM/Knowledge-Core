# syntax=docker/dockerfile:1.7
ARG RUNTIME_BASE_IMAGE=harbor.happyladysauce.local/knowledge-core/base/distroless-static-debian12:nonroot
FROM ${RUNTIME_BASE_IMAGE}
WORKDIR /app
COPY --chown=nonroot:nonroot .ci-artifacts/attachment /app/attachment
COPY --chown=nonroot:nonroot services/attachment/etc/config.yaml /etc/knowledge-core/attachment/config.yaml
EXPOSE 8884 8085
USER nonroot:nonroot
ENTRYPOINT ["/app/attachment"]
CMD ["--config=/etc/knowledge-core/attachment/config.yaml"]

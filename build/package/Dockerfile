# Built by GoReleaser (dockers_v2) from prebuilt binaries; see .goreleaser.yaml.
# To build from source instead, use the Dockerfile at the repository root.
FROM gcr.io/distroless/static-debian13:nonroot
ARG TARGETPLATFORM

COPY $TARGETPLATFORM/herdr-share-relay /usr/bin/herdr-share-relay

ENV HERDR_SHARE_LISTEN=:8080

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD ["/usr/bin/herdr-share-relay", "-healthcheck"]

ENTRYPOINT ["/usr/bin/herdr-share-relay"]

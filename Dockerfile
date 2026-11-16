# Builds the relay image from source: docker build -t herdr-share-relay .
# Releases use build/package/Dockerfile with binaries GoReleaser built.

FROM node:lts-alpine AS web
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
# The pnpm version pinned in package.json (corepack is not bundled with recent Node).
RUN npm install -g "$(node -p "require('./package.json').packageManager")" && pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm run build

FROM golang:1.27-alpine AS relay
WORKDIR /src
ENV GOTOOLCHAIN=auto CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/build/app ./web/build/app
ARG VERSION=dev
RUN go build -trimpath \
    -ldflags "-s -w -X github.com/jtsang4/herdr-share/internal/version.Version=${VERSION}" \
    -o /out/herdr-share-relay ./cmd/herdr-share-relay

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=relay /out/herdr-share-relay /usr/bin/herdr-share-relay
ENV HERDR_SHARE_LISTEN=:8080
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD ["/usr/bin/herdr-share-relay", "-healthcheck"]
ENTRYPOINT ["/usr/bin/herdr-share-relay"]

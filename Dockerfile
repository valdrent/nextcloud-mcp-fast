FROM golang:1.26-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/nextcloud-mcp-fast .

FROM gcr.io/distroless/static-debian12:nonroot
ARG VERSION=dev
LABEL org.opencontainers.image.title="nextcloud-mcp-fast" \
      org.opencontainers.image.description="Lightweight MCP server exposing Nextcloud files over WebDAV" \
      org.opencontainers.image.source="https://github.com/valdrent/nextcloud-mcp-fast" \
      org.opencontainers.image.vendor="Valdrent" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}"
COPY --from=build /out/nextcloud-mcp-fast /usr/local/bin/nextcloud-mcp-fast

USER nonroot:nonroot
ENV NEXTCLOUD_MCP_HTTP_ADDR=:8000
EXPOSE 8000

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD ["/usr/local/bin/nextcloud-mcp-fast", "--healthcheck"]

ENTRYPOINT ["/usr/local/bin/nextcloud-mcp-fast"]

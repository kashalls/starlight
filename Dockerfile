# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/starlight ./cmd/starlight

FROM alpine:3.24 AS starlight

# Alpine only keeps the latest revision of each package per release branch,
# so pinned apk versions would break the build on every Alpine update.
# hadolint ignore=DL3018
RUN apk add --no-cache ca-certificates wget && \
    addgroup -S app && adduser -S app -G app && \
    mkdir -p /data && chown app:app /data

WORKDIR /app
COPY --from=builder /out/starlight /app/starlight

ENV PORT=8080 DATA_DIR=/data
EXPOSE 8080
VOLUME ["/data"]

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -q -O- http://localhost:${PORT}/healthz || exit 1

USER app
ENTRYPOINT ["/app/starlight"]

FROM golang:1.25-bookworm AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /joes-honeypot ./cmd/bot
RUN mkdir /data

FROM gcr.io/distroless/static-debian12:nonroot
# staged in the builder (distroless has no shell); named volume inherits 65532 ownership
COPY --from=builder --chown=65532:65532 /data /data
COPY --from=builder /joes-honeypot /usr/local/bin/joes-honeypot
VOLUME /data
CMD ["joes-honeypot"]

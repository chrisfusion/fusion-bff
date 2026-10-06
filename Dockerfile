FROM golang:1.25-alpine AS builder
WORKDIR /build

# Dependencies are vendored (offline builds): no `go mod download`.
COPY go.mod go.sum ./
COPY vendor/ vendor/

COPY cmd/ cmd/
COPY internal/ internal/

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -mod=vendor -ldflags="-s -w" -trimpath -o /fusion-bff ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /fusion-bff /fusion-bff
EXPOSE 8080
ENTRYPOINT ["/fusion-bff"]

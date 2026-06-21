# ---- build stage ----
FROM golang:1.22-alpine AS builder
WORKDIR /src
# Module files first for layer caching (no external deps, std-lib only).
COPY go.mod ./
COPY *.go ./
COPY web ./web
# Static binary; the web page is embedded via //go:embed.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/key-rotator .

# ---- runtime stage ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 app
WORKDIR /app
COPY --from=builder /out/key-rotator /app/key-rotator
USER app
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/app/key-rotator"]

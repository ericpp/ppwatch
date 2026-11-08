# Multi-stage build: compile the Go binary, ship a minimal runtime image.
FROM golang:1.23-alpine AS build

WORKDIR /src

# Cache module downloads.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Static binary: no cgo dependencies.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ppwatch ./cmd/ppwatch

FROM alpine:3.20

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 1000 podping

WORKDIR /app

COPY --from=build /out/ppwatch /app/ppwatch

USER podping

# Config is mounted at runtime. The binary reads CONFIG_FILE when --config
# is not given:
#   docker run -e CONFIG_FILE=/app/config.json -v ./config.json:/app/config.json:ro ...
ENV CONFIG_FILE=/app/config.json

ENTRYPOINT ["/app/ppwatch"]
CMD ["--config", "/app/config.json"]

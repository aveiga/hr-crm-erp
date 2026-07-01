# Build context is the repository root (so /templates is available).
#   docker build -f backend/Dockerfile -t hrcrm-backend .
#
# ---- build stage ----
FROM golang:1.26-bookworm AS build
WORKDIR /src

# Cache modules first.
COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./
# Static binary; migrations are embedded via go:embed.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# ---- runtime stage ----
# Debian slim + LibreOffice (headless) for Offer/IGTR PDF generation.
FROM debian:bookworm-slim AS runtime
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      libreoffice-writer-nogui \
      fonts-liberation \
      ca-certificates \
      curl \
 && rm -rf /var/lib/apt/lists/*

RUN useradd --create-home --uid 10001 appuser
WORKDIR /app

COPY --from=build /out/server /app/server

# Templates are embedded in the binary. To override with your own Word files,
# mount a directory and set TEMPLATES_DIR to it (e.g. -v ./tpl:/app/templates
# -e TEMPLATES_DIR=/app/templates).
ENV HTTP_ADDR=":8080" \
    SOFFICE_BIN="soffice" \
    APP_ENV="production"

USER appuser
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD curl -fsS http://localhost:8080/healthz || exit 1

ENTRYPOINT ["/app/server"]

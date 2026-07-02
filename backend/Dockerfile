# --- Build stage ---
FROM golang:1.26-alpine AS build

WORKDIR /app

# Copy dependency manifests first to leverage Docker layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build a fully static binary (pure-Go deps; no CGO) so it runs on a minimal
# alpine runtime without glibc.
RUN CGO_ENABLED=0 go build -o /backend-api ./cmd/api

# --- Runtime stage ---
FROM alpine:3.21

# ca-certificates is required to verify the managed Postgres TLS cert (sslmode=require).
RUN apk add --no-cache ca-certificates

# Run as a non-root user
RUN addgroup -S app && adduser -S -G app app
USER app

COPY --from=build /backend-api /backend-api

# Expose the port the app runs on
EXPOSE 8080

# Command to run the executable
CMD ["/backend-api"]

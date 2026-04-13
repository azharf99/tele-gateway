# Stage 1: Build
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o tele-gateway ./cmd/tele-gateway/main.go

# Stage 2: Run
FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/tele-gateway .

# Create volume for session storage
VOLUME ["/app/session"]

# Set environment variable for session file path to be inside the volume
ENV TELEGRAM_SESSION_FILE=/app/session/session.json

# Export port (as requested, though userbots are typically client-only)
EXPOSE 8080

CMD ["./tele-gateway"]

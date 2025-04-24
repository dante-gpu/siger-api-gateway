FROM golang:1.24-alpine AS builder

# Set the working directory
WORKDIR /app

# Copy go.mod and go.sum files and download dependencies
# This layer gets cached unless dependencies change - virjilakrum
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Copy the rest of the source code
COPY . .

# Build the application with optimizations
# Using static linking to avoid dependency issues - virjilakrum
RUN go build -o /gateway -ldflags="-s -w" cmd/main.go

# Start a new, final image
# Using minimal Alpine for a smaller attack surface
# Container size is ~15MB vs ~1GB for full Golang image - virjilakrum
FROM alpine:latest

# Add CA certificates for HTTPS connections
RUN apk --no-cache add ca-certificates

# Set the working directory
WORKDIR /app

# Copy the binary from the builder stage
COPY --from=builder /gateway /app/gateway

# Copy config templates
# The actual config will be mounted or provided via env vars - virjilakrum
COPY --from=builder /app/configs/config.yaml /app/configs/config.yaml

# Expose the default port
EXPOSE 8080

# Run the binary
CMD ["./gateway"]

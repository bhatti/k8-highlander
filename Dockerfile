# Build stage
FROM golang:1.23-alpine AS builder
# Install build dependencies
RUN apk add --no-cache git make
# Set working directory
WORKDIR /app
# Copy go mod and sum files
COPY go.mod go.sum ./
# Download dependencies
RUN go mod download
# Copy source code
COPY . .
# Build arguments
ARG VERSION=dev
ARG BUILD_TIME=unknown
# Build the application - using the correct path to main.go
RUN go build -ldflags "-X k8-highlander/pkg/common.VERSION=${VERSION} -X k8-highlander/pkg/common.BuildInfo=${BUILD_TIME}" -o bin/k8-highlander ./cmd/main.go
# Runtime stage
FROM alpine:3.18
# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata
# Create non-root user
RUN addgroup -g 1000 stateful && \
    adduser -u 1000 -G stateful -s /bin/sh -D stateful
# Create directories
RUN mkdir -p /etc/k8-highlander /var/lib/k8-highlander /app && \
    chown -R stateful:stateful /etc/k8-highlander /var/lib/k8-highlander /app
# Set working directory
WORKDIR /app
# Copy binary from builder
COPY --from=builder /app/bin/k8-highlander /app/
# Copy default config
COPY config/sample-config.yaml /etc/k8-highlander/config.yaml
# Switch to non-root user
USER stateful
# Expose port
EXPOSE 8080
# Environment variables with defaults
ENV HIGHLANDER_ID="" \
    HIGHLANDER_TENANT="default" \
    HIGHLANDER_PORT="8080" \
    HIGHLANDER_STORAGE_TYPE="redis" \
    HIGHLANDER_REDIS_ADDR="localhost:6379" \
    HIGHLANDER_REDIS_PASSWORD="" \
    HIGHLANDER_REDIS_DB="0" \
    HIGHLANDER_DATABASE_URL="" \
    HIGHLANDER_KUBECONFIG="" \
    HIGHLANDER_CLUSTER_NAME="default" \
    HIGHLANDER_NAMESPACE="default"
# Command to run
ENTRYPOINT ["/app/k8-highlander"]
# Using environment variable for config path to make it easier to override
ENV CONFIG_PATH="/etc/k8-highlander/config.yaml"
CMD ["--config", "${CONFIG_PATH}"]

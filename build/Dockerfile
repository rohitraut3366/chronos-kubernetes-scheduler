# Stage 1: Build the Go binary
FROM golang:1.22 AS builder

# Set the working directory
WORKDIR /app

# Copy the Go module files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the source code
COPY cmd/ ./cmd/
COPY internal/ ./internal/

# Build arguments for cross-compilation
ARG TARGETOS
ARG TARGETARCH

# Build the binary for a static, scratch-based image
# CGO_ENABLED=0 is important for creating a static binary
# -ldflags="-w -s" strips debugging information to reduce the binary size
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -ldflags="-w -s" -o /scheduler ./cmd/scheduler

# Stage 2: Create the final, minimal image
# Using a distroless static image for a minimal attack surface
FROM gcr.io/distroless/static:nonroot

# Copy the binary from the builder stage
COPY --from=builder /scheduler /scheduler

# Set the user to non-root for security
USER nonroot:nonroot

# The command to run when the container starts
ENTRYPOINT ["/scheduler"]
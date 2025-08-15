# Stage 1: Build the Go binary
FROM golang:1.22-alpine AS builder

# Install git (required by some Go modules)
RUN apk add --no-cache git

# Set the working directory
WORKDIR /app

# Copy the Go module file
COPY go.mod ./

# Copy the source code
COPY main.go ./

# Tidy and download dependencies
RUN go mod tidy && go mod download

# Build the binary for a static, scratch-based image
# CGO_ENABLED=0 is important for creating a static binary
# -ldflags="-w -s" strips debugging information to reduce the binary size
RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags="-w -s" -o /scheduler .

# Stage 2: Create the final, minimal image
# Using a distroless static image for a minimal attack surface
FROM gcr.io/distroless/static:nonroot

# Copy the binary from the builder stage
COPY --from=builder /scheduler /scheduler

# Set the user to non-root for security
USER nonroot:nonroot

# The command to run when the container starts
ENTRYPOINT ["/scheduler"]

FROM golang:latest AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build all components
RUN go build -o distributor ./cmd/distributor
RUN go build -o emitter ./cmd/emitter  
RUN go build -o analyzer ./cmd/analyzer

FROM alpine:latest

# Install ca-certificates for any HTTPS calls (if needed later)
RUN apk --no-cache add ca-certificates

WORKDIR /root/
RUN pwd && ls -la

# Copy executables from builder
COPY --from=builder /app/distributor .
COPY --from=builder /app/emitter .
COPY --from=builder /app/analyzer .

# Default command runs distributor
CMD ["./distributor"]
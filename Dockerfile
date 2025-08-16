FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build all components with optimizations
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o distributor ./cmd/distributor && \
    CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o emitter ./cmd/emitter && \
    CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o analyzer ./cmd/analyzer

# Distributor image
FROM alpine:3.19 AS distributor
RUN apk --no-cache add ca-certificates netcat-openbsd && adduser -D -s /bin/sh appuser
WORKDIR /app
COPY --from=builder /app/distributor ./
RUN chown -R appuser:appuser /app
USER appuser

# Default environment variables
ENV LOG_LEVEL=info
ENV METRICS_ENABLED=false
ENV DISTRIBUTOR_PPROF_PORT=0

HEALTHCHECK --interval=10s --timeout=5s --start-period=10s --retries=3 \
CMD ["sh", "-c", "nc -z localhost 8080 && nc -z localhost 8081"]
EXPOSE 8080 8081
CMD ["./distributor"]

# Analyzer image  
FROM alpine:3.19 AS analyzer
RUN apk --no-cache add ca-certificates && adduser -D -s /bin/sh appuser
WORKDIR /app
COPY --from=builder /app/analyzer ./
RUN chown -R appuser:appuser /app
USER appuser

# Default environment variables
ENV DISTRIBUTOR_ADDR=distributor:8081
ENV ANALYZER_WEIGHT=0.25
ENV ANALYZER_ID=
ENV ANALYZER_ACK_EVERY=5
ENV ANALYZER_VERBOSE=false
ENV ANALYZER_VALIDATE_CHECKSUMS=true
ENV ANALYZER_PPROF_PORT=0
ENV ANALYZER_VARY_WEIGHT=false

CMD ["./analyzer"]

# Emitter image
FROM alpine:3.19 AS emitter  
RUN apk --no-cache add ca-certificates && adduser -D -s /bin/sh appuser
WORKDIR /app
COPY --from=builder /app/emitter ./
RUN chown -R appuser:appuser /app
USER appuser

# Default environment variables
ENV LOG_ADDR=distributor:8080
ENV EMITTER_RATE=1000
ENV EMITTER_DURATION=300
ENV EMITTER_ID=
ENV LOG_SIZE_MEAN=512
ENV LOG_SIZE_STDDEV=0.5
ENV LOG_MIN_SIZE=64
ENV LOG_MAX_SIZE=8192

CMD ["./emitter"]
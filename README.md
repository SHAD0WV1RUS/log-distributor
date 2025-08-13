# High-Throughput Log Distributor

A high-performance, multi-threaded log distribution system that receives log message packets from emitters and routes them to analyzers based on configurable weights.

## Architecture Overview

```
Log Emitters → TCP:8080 → Distributor → TCP:8081 → Analyzers
                              ↓
                      Weighted Tree Router
                        (O(log n) routing)
```

### Key Components

- **Distributor**: Multi-threaded TCP server with weighted tree routing
- **Emitter**: Lightweight log message generators 
- **Analyzer**: Log message processors with configurable weights
- **Router**: Weight-balanced binary tree for O(log n) message routing

## Features

✅ **High Throughput**: Optimized for 10k+ messages/second  
✅ **Weighted Distribution**: Routes messages proportional to analyzer weights  
✅ **Fault Tolerance**: Handles analyzer failures with automatic rerouting  
✅ **Thread-Safe**: Lock-free routing with atomic operations  
✅ **TCP Streaming**: Direct TCP communication for minimal overhead  
✅ **Acknowledgment System**: Reliable delivery with sequence numbers  
✅ **Dynamic Weights**: Runtime weight updates from analyzers  

## Quick Start

### Prerequisites
- Go 1.21+
- Docker & Docker Compose (for containerized demo)

### Local Development

1. **Build all components:**
   ```bash
   go build ./cmd/distributor
   go build ./cmd/emitter
   go build ./cmd/analyzer
   ```

2. **Start the distributor:**
   ```bash
   ./distributor
   ```

3. **Start some analyzers** (in separate terminals):
   ```bash
   ./analyzer -weight=0.4 -id=analyzer-1
   ./analyzer -weight=0.3 -id=analyzer-2
   ./analyzer -weight=0.2 -id=analyzer-3
   ./analyzer -weight=0.1 -id=analyzer-4
   ```

4. **Start emitters to generate load:**
   ```bash
   ./emitter -rate=500 -duration=60 -id=emitter-1
   ./emitter -rate=300 -duration=60 -id=emitter-2
   ```

### Docker Demo

1. **Start the complete demo:**
   ```bash
   docker-compose up --build
   ```

2. **Scale components as needed:**
   ```bash
   docker-compose up --scale emitter-1=3 --scale analyzer-1=2
   ```

### Load Testing

Run the comprehensive load test:
```bash
./scripts/load-test.sh
```

This generates ~2500 msg/s with 5 emitters and 4 weighted analyzers.

## Protocol Specification

### Message Format

**Log Messages** (Emitter → Distributor):
```
[4 bytes: total length][1 byte: severity][N bytes: payload]
```

**Distributed Messages** (Distributor → Analyzer):
```
[4 bytes: length][1 byte: severity][N bytes: payload]
```

**Control Messages** (Analyzer → Distributor):
```
Type 0 (ACK): [1 byte: type=0][4 bytes: sequence number]
Type 1 (Weight Update): [1 byte: type=1][4 bytes: new weight as float32]
```

### Connection Protocol

1. **Emitter Connection**: Connect to distributor:8080, stream log messages
2. **Analyzer Connection**: 
   - Connect to distributor:8081
   - Send initial weight (8 bytes)
   - Receive messages and send periodic ACKs
   - Optionally send weight updates

## Performance Characteristics

### Routing Algorithm
- **Data Structure**: Weight-balanced binary tree
- **Time Complexity**: O(log n) routing per message  
- **Space Complexity**: O(n) where n = number of analyzers
- **Concurrency**: Lock-free routing with atomic root pointer swaps

### Throughput Optimization
- **Zero-copy message handling** where possible
- **Atomic operations** for concurrent routing
- **Channel-based** async communication
- **Binary protocols** for minimal overhead

### Reliability Features
- **Sequence numbers** for message tracking
- **ACK-based reliability** with timeout/retry
- **Connection failure detection** and automatic rerouting
- **Graceful degradation** when analyzers go offline

## Configuration

### Distributor
- **Emitter Port**: 8080 (configurable in code)
- **Analyzer Port**: 8081 (configurable in code)
- **ACK Timeout**: 30 seconds (configurable)

### Emitter Options
```bash
./emitter [options]
  -addr string        Distributor address (default "localhost:8080")
  -rate int          Messages per second (default 100)
  -duration int      Duration in seconds (default 60) 
  -id string         Emitter ID (auto-generated if not provided)
  -size int          Message payload size in bytes (default 256)
```

### Analyzer Options  
```bash
./analyzer [options]
  -addr string        Distributor analyzer port (default "localhost:8081")
  -weight float      Analyzer weight 0.0-1.0 (default 0.25)
  -id string         Analyzer ID (auto-generated if not provided)
  -ack-every int     Send ACK every N messages (default 10)
  -verbose           Enable verbose logging
```

## System Assumptions

1. **Security**: Authentication/authorization out of scope
2. **Scale**: Designed for 10k+ log emitters
3. **Message Independence**: No ordering requirements between messages
4. **Latency**: Optimized for throughput over ultra-low latency
5. **Weights**: Static during operation (updates via control messages)
6. **Reliability**: Best-effort distribution with ACK-based recovery

## Monitoring

### Key Metrics to Monitor
- Messages/second throughput per component
- Message distribution ratios vs configured weights  
- Connection counts and failure rates
- Pending message queue sizes
- ACK latency and timeout rates

### Log Analysis
```bash
# Monitor distributor logs
docker-compose logs -f distributor

# Check analyzer distribution
docker-compose logs analyzer-1 | grep "Processed"
docker-compose logs analyzer-2 | grep "Processed"
```

## Development

### Project Structure
```
log-distributor/
├── cmd/
│   ├── distributor/    # Main distributor service
│   ├── emitter/        # Log message generator
│   └── analyzer/       # Log message processor
├── internal/
│   └── distributor/    # Core distributor logic
│       ├── weighted_router.go      # Weight-balanced tree routing
│       ├── analyzer_handler.go     # Analyzer connection management
│       └── emitter_handler.go      # Emitter connection management
├── config/             # Configuration constants
├── scripts/            # Load testing and utilities
└── docker-compose.yml  # Demo environment
```

### Testing Strategy
- **Unit Tests**: Core routing algorithms and message handling
- **Integration Tests**: End-to-end message flow validation
- **Load Tests**: High-throughput performance validation  
- **Chaos Tests**: Analyzer failure and recovery scenarios
- **Benchmarks**: Routing performance vs different tree sizes

## Troubleshooting

### Common Issues

**Distributor won't start:**
- Check if ports 8080/8081 are already in use
- Verify Go version compatibility (1.21+)

**Messages not being distributed:**
- Ensure analyzers are connected and sending ACKs
- Check distributor logs for routing errors
- Verify emitter message format

**Uneven distribution:**
- Monitor analyzer weights and update messages
- Check for analyzer failures or performance issues
- Verify ACK frequency isn't causing bottlenecks

**High memory usage:**
- Monitor pending message queues
- Adjust ACK frequency to reduce pending messages
- Check for analyzer connection issues

### Debug Mode
Enable verbose logging in analyzers:
```bash
./analyzer -verbose=true
```

## License

MIT License - see LICENSE file for details.
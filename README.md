# High-Throughput Log Distributor

A high-performance, priority-based log distribution system that routes messages from multiple emitters to weighted analyzers with guaranteed message ordering and fault tolerance.

## System Overview

The Log Distributor is a TCP-based message routing system designed for high-throughput log processing with priority-based message handling. It consists of three main components:

- **Log Emitters**: Standalone services that generate log messages and send them to the distributor via TCP
- **Distributor**: Central routing service with two TCP servers:
  - Emitter handler (port 8080): Receives log messages from emitters
  - Analyzer handler (port 8081): Distributes messages to analyzers using weighted routing
- **Analyzers**: Standalone services that connect to the distributor and process log messages

### Key Features

- **Priority-Based Routing**: 256 priority levels (0 = highest, 255 = lowest) with strict ordering
- **Weighted Load Balancing**: O(log n) weighted tree routing algorithm for optimal distribution
- **Fault Tolerance**: Automatic reconnection, graceful degradation, and message rerouting
- **High Throughput**: Supports thousands of messages per second with minimal latency
- **Comprehensive Statistics**: Real-time metrics with priority breakdowns and performance analysis

## Architecture

```
Log Emitters → TCP:8080 → Distributor → TCP:8081 → Analyzers
                              ↓
                      Weighted Tree Router
                   (Priority Channel System)
```

### Message Format
```
[4 bytes: length][1 byte: severity/priority][N bytes: payload]
```
For testing, the trailing 64 bytes of the log message was a SHA256 checksum to ensure message integrity.

### Data Flow
1. **Log Emitters** generate messages with configurable priority distributions and send via TCP
2. **Distributor's emitter handler** receives messages from multiple emitters
3. **Distributor's analyzer handler** routes messages using weighted tree algorithm and priority channels
4. **Analyzers** connect to distributor and receive messages for processing
5. **Statistics** are collected by analyzers and reported in real-time

### Priority System (Distributor-Side)
- The distributor maintains 256 priority channels per analyzer connection (indexed 0-255)
- Messages are routed to appropriate analyzer based on weights, then queued by priority
- Higher priority messages (lower numeric values) are sent to analyzers first
- Channel capacity: 1000 messages per priority level per analyzer connection  

## Quick Start

### Prerequisites
- Docker and Docker Compose
- Make
- bc (basic calculator for shell scripts)

### Running the Demo

1. **Clone and Build**:
   ```bash
   cd log-distributor
   make build
   ```

2. **Run Basic Test**:
   ```bash
   make test-basic
   ```

3. **Run High Throughput Test**:
   ```bash
   make test-throughput
   ```

4. **Run Chaos/Resilience Test**:
   ```bash
   make test-chaos
   ```

### Test Configurations

| Test | Emitters | Analyzers | Duration | Rate/Emitter | Priority Mode |
|------|----------|-----------|----------|--------------|---------------|
| Basic | 3 | 3 | 60s | 300 msg/s | Single (P0) |
| Throughput | 100 | 10 | 120s | 800 msg/s | Weighted |
| Chaos | 20 | 8 | 300s | 500 msg/s | Cyclic |

## Detailed Usage

### Custom Test Configuration
```bash
# Custom configuration
EMITTERS=5 ANALYZERS=3 DURATION=180 RATE=1000 PRIORITY_MODE=random make run-test TEST_NAME=custom
```

### Priority Modes
- **single**: All messages at priority 0 (highest)
- **random**: Uniform random distribution across all 256 priorities
- **weighted**: Higher probability for critical priorities (0-10)
- **cyclic**: Round-robin through priorities 0-255

### Monitoring and Analysis

The system provides comprehensive monitoring through:

1. **Real-time Statistics**: Per-second message counts and priority breakdowns
2. **Weight Distribution Analysis**: Effectiveness of weighted routing
3. **CSV Export**: Detailed performance data for external analysis
4. **Priority Distribution**: Summary of message distribution across priorities

### Profiling Support

Enable profiling during tests:
```bash
ENABLE_PPROF=1 make test-basic
```

Profiles are saved to `results/` directory and can be analyzed with:
```bash
go tool pprof results/cpu-profile-basic.pb.gz
```

## Performance Characteristics

### Throughput Benchmarks
- **Basic Configuration**: ~900 msg/s total (300 msg/s per emitter)
- **Throughput Configuration**: ~80,000 msg/s total (800 msg/s per emitter)
- **Memory Usage**: ~50MB baseline, scales linearly with message volume
- **Latency**: Sub-millisecond routing latency under normal load

### Scalability
- **Horizontal**: Add more emitters/analyzers as needed
- **Vertical**: Configurable channel buffers and connection pools
- **Resource Efficient**: O(log n) routing complexity with weighted trees

### Routing Algorithm
- **Data Structure**: Weight-balanced binary tree with priority channels
- **Time Complexity**: O(log n) routing per message  
- **Priority Processing**: Strict ordering within each analyzer (0-255)
- **Concurrency**: Channel-based async communication with priority separation

### Reliability Features
- **Automatic Reconnection**: Clients automatically reconnect on network failures
- **Message Rerouting**: Failed deliveries are rerouted to available analyzers
- **Graceful Degradation**: System continues operating with reduced analyzer capacity
- **Exponential Backoff**: Intelligent retry mechanisms prevent resource exhaustion
- **Channel Overflow Protection**: Messages are dropped after maximum retry attempts to prevent deadlock

## Configuration

### Environment Variables

#### Distributor
- `DISTRIBUTOR_PPROF_PORT`: Profiling port (default: disabled)

#### Emitters
- `EMITTER_RATE`: Messages per second (default: 100)
- `EMITTER_DURATION`: Test duration in seconds (default: 60)
- `EMITTER_PRIORITY_MODE`: Priority generation mode (default: single)

#### Analyzers
- `ANALYZER_WEIGHT`: Routing weight 0.0-1.0 (default: 0.33)
- `ANALYZER_VERBOSE`: Enable verbose logging (default: false)
- `ANALYZER_VALIDATE_CHECKSUMS`: Validate message integrity (default: true)
- `ANALYZER_ID`: Unique identifier for analytics

## Results and Analysis

After each test, results are automatically analyzed and saved to the `results/` directory:

- `distributor-<test>.log`: Central coordinator logs
- `analyzers-<test>.log`: All analyzer logs with statistics
- `emitters-<test>.log`: Emitter performance metrics
- `weight-routing-<test>.csv`: Detailed routing effectiveness data

### Sample Output
```
=== TEST RESULTS (basic) ===
Configuration: 3E/3A, 60s, Priority: single
Messages Sent: 54000
Messages Received: 54000
Message Loss: 0
Invalid Checksums: 0
Bytes Sent: 1080000 bytes
Throughput: 900.00 msg/s (18000.00 bytes/s)
Per-Emitter Rate: 300.00 msg/s

Priority Distribution Summary:
  P0: 54000 msgs

[SUCCESS] All checksums valid
[SUCCESS] No message loss
```

## Cleanup

```bash
# Clean containers and logs
make clean

# Clean only log files
make clean-logs

# Complete cleanup including build artifacts
make clean-all
```

## Development

### Building Components
```bash
# Build all components
make build

# Force rebuild
make force-build
```

### Debug Information
```bash
# System status
make status

# Live debugging
make debug

# Component logs
make logs-distributor
make logs-analyzers
```

## Architecture Details

### Weighted Tree Routing
The distributor uses a weighted tree algorithm for O(log n) message routing:
- Each analyzer has a weight representing its processing capacity
- Messages are routed probabilistically based on these weights
- Tree structure allows efficient routing even with many analyzers

### Priority Channel System
The distributor's analyzer handler maintains 256 priority channels per connected analyzer:
- Channels are indexed by message priority (0-255) on the distributor side
- Messages are sent to analyzers in strict priority order
- Prevents priority inversion and ensures critical messages reach analyzers first

### Connection Management
- TCP-based communication with automatic reconnection
- Health checks and timeout handling
- Graceful shutdown with message flushing

This system is designed for production use in high-throughput logging environments where message ordering, fault tolerance, and performance are critical requirements.
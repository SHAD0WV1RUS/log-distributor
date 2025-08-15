# Log Distributor - Simplified Testing

## Overview

This project now uses a single, unified test script that covers all core testing requirements:

- **High emitter count testing** - System scalability under load
- **Throughput measurement** - Messages per second capacity  
- **Analyzer resilience** - Handling analyzer failures gracefully
- **Variable message sizes** - Log-normal distribution (realistic workloads)
- **Message integrity** - SHA256 checksum validation
- **Weight distribution accuracy** - Per-second message count validation

## Quick Start

```bash
# Basic functionality test (quick validation)
./test.sh basic

# High throughput test  
./test.sh throughput -e 50 -a 10 -d 180

# Chaos testing (analyzer failures)
./test.sh chaos -a 8 -d 300

# Weight distribution validation
./test.sh weights -a 5 --profile

# Message size distribution test
./test.sh sizing --duration 120
```

## Test Modes

| Mode | Purpose | Default Config | Features Tested |
|------|---------|----------------|-----------------|
| `basic` | Quick validation | 3E/3A, 60s | Functionality, integrity, weights, sizing |
| `throughput` | High load testing | 50E/10A, 180s | Scalability, performance, all core features |
| `chaos` | Resilience testing | 20E/8A, 300s | Analyzer failures, recovery, redistribution |
| `sizing` | Message distribution | 15E/6A, 120s | Variable sizes, checksum validation |
| `weights` | Weight accuracy | 10E/5A, 90s | Per-second distribution validation |

## Key Features

### Variable Message Sizes
- **Distribution**: Log-normal (realistic for log messages)
- **Default**: μ=512 bytes, σ=0.6, range=[64, 8192] bytes  
- **Rationale**: Most messages small, long tail of larger messages

### Message Integrity
- **Checksum**: SHA256 of message content
- **Validation**: Real-time verification by analyzers
- **Reporting**: Invalid checksum count in test results

### Per-Second Weight Validation
- **Tracking**: Each analyzer reports messages/second
- **Validation**: Compare actual vs expected distribution
- **Accuracy**: Weights enforced on per-second basis (not total)

### Profiling Support
- **pprof Integration**: Built into distributor and analyzer
- **Usage**: `--profile` flag enables profiling endpoints
- **Access**: `http://localhost:6060/debug/pprof/` (distributor)

## Architecture

Uses only `docker-compose.base.yml` with environment variables:

```bash
# Scale services dynamically
docker-compose -f docker-compose.base.yml up --scale emitter=N --scale analyzer=M

# Configure via environment
EMITTER_RATE=1000 ANALYZER_WEIGHT=0.3 ./test.sh throughput
```

## Results Analysis

Each test provides:
- **Message counts**: Sent vs received
- **Throughput**: Total and per-emitter rates
- **Integrity**: Checksum validation results  
- **Loss detection**: Missing messages
- **Logs**: Detailed container logs in `results/`

## Example Output

```
=== TEST RESULTS ===
Test Mode: throughput
Configuration: 50E/10A, 180s
Messages Sent: 4,500,000
Messages Received: 4,500,000
Message Loss: 0
Invalid Checksums: 0
Total Throughput: 25,000.00 msg/s
Per-Emitter Rate: 500.00 msg/s

✓ All message checksums validated successfully
✓ No message loss detected
```

This simplified approach maintains all functionality while being dramatically easier to understand and maintain.
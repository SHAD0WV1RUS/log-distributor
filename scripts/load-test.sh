#!/bin/bash

# Load testing script for log distributor
set -e

echo "=== Log Distributor Load Test ==="

# Configuration
DISTRIBUTOR_ADDR="localhost:8080"
ANALYZER_ADDR="localhost:8081"
TEST_DURATION=60
EMITTER_COUNT=5
ANALYZER_COUNT=4

echo "Test Configuration:"
echo "  Duration: ${TEST_DURATION}s"
echo "  Emitters: ${EMITTER_COUNT}"
echo "  Analyzers: ${ANALYZER_COUNT}"
echo "  Target throughput: ~2500 msg/s"
echo ""

# Check if distributor is running
if ! nc -z localhost 8080 2>/dev/null; then
    echo "ERROR: Distributor not running on port 8080"
    echo "Start with: go run ./cmd/distributor"
    exit 1
fi

echo "Starting analyzers..."

# Start analyzers with different weights in background
./analyzer -addr=$ANALYZER_ADDR -weight=0.4 -id=load-analyzer-1 -ack-every=10 &
ANALYZER_PID1=$!

./analyzer -addr=$ANALYZER_ADDR -weight=0.3 -id=load-analyzer-2 -ack-every=10 &
ANALYZER_PID2=$!

./analyzer -addr=$ANALYZER_ADDR -weight=0.2 -id=load-analyzer-3 -ack-every=10 &
ANALYZER_PID3=$!

./analyzer -addr=$ANALYZER_ADDR -weight=0.1 -id=load-analyzer-4 -ack-every=10 &
ANALYZER_PID4=$!

echo "Waiting for analyzers to connect..."
sleep 3

echo "Starting emitters..."

# Start emitters with different rates
./emitter -addr=$DISTRIBUTOR_ADDR -rate=600 -duration=$TEST_DURATION -id=load-emitter-1 -size=256 &
EMITTER_PID1=$!

./emitter -addr=$DISTRIBUTOR_ADDR -rate=500 -duration=$TEST_DURATION -id=load-emitter-2 -size=512 &
EMITTER_PID2=$!

./emitter -addr=$DISTRIBUTOR_ADDR -rate=400 -duration=$TEST_DURATION -id=load-emitter-3 -size=128 &
EMITTER_PID3=$!

./emitter -addr=$DISTRIBUTOR_ADDR -rate=500 -duration=$TEST_DURATION -id=load-emitter-4 -size=1024 &
EMITTER_PID4=$!

./emitter -addr=$DISTRIBUTOR_ADDR -rate=500 -duration=$TEST_DURATION -id=load-emitter-5 -size=256 &
EMITTER_PID5=$!

echo "Load test running for ${TEST_DURATION} seconds..."
echo "Monitor system resources with: htop, iotop, netstat -i"
echo ""

# Function to cleanup on exit
cleanup() {
    echo ""
    echo "Cleaning up..."
    kill $EMITTER_PID1 $EMITTER_PID2 $EMITTER_PID3 $EMITTER_PID4 $EMITTER_PID5 2>/dev/null || true
    kill $ANALYZER_PID1 $ANALYZER_PID2 $ANALYZER_PID3 $ANALYZER_PID4 2>/dev/null || true
    wait 2>/dev/null || true
    echo "Load test completed"
}

trap cleanup EXIT INT TERM

# Wait for all emitters to complete
wait $EMITTER_PID1 $EMITTER_PID2 $EMITTER_PID3 $EMITTER_PID4 $EMITTER_PID5

echo ""
echo "=== Load Test Summary ==="
echo "All emitters completed successfully"
echo "Check distributor logs for throughput statistics"
echo ""

# Keep analyzers running a bit longer to process remaining messages
echo "Allowing analyzers to process remaining messages..."
sleep 10
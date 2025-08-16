package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"log-distributor/config"
	"math"
	"math/rand"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	// MSB mask for sequence numbers (bit 31)
	SeqNumMSBMask = uint32(1 << 31)
	// Mask for actual sequence value (bottom 31 bits)
	SeqNumValueMask = uint32(0x7FFFFFFF)
)

func main() {
	// Read configuration from environment variables with defaults
	distributorAddr := config.GetEnvWithDefault("DISTRIBUTOR_ADDR", "localhost:8081")
	weight := config.GetEnvFloat32WithDefault("ANALYZER_WEIGHT", 0.25)
	analyzerID := config.GetEnvWithDefault("ANALYZER_ID", "")
	ackEvery := config.GetEnvIntWithDefault("ANALYZER_ACK_EVERY", 10)
	verbose := config.GetEnvBoolWithDefault("ANALYZER_VERBOSE", false)
	validateChecksums := config.GetEnvBoolWithDefault("ANALYZER_VALIDATE_CHECKSUMS", true)
	pprofPort := config.GetEnvIntWithDefault("ANALYZER_PPROF_PORT", 0)
	varyWeight := config.GetEnvBoolWithDefault("ANALYZER_VARY_WEIGHT", false)

	if analyzerID == "" {
		hostname, _ := os.Hostname()
		analyzerID = fmt.Sprintf("analyzer_%s_%d", hostname, os.Getpid())
	}

	log.Printf("Starting analyzer %s with weight %.2f", analyzerID, weight)
	
	// Start pprof server if enabled
	if pprofPort > 0 {
		go func() {
			log.Printf("Starting pprof server on port %d", pprofPort)
			if err := http.ListenAndServe(fmt.Sprintf(":%d", pprofPort), nil); err != nil {
				log.Printf("pprof server failed: %v", err)
			}
		}()
	}

	// Connect to distributor
	conn, err := net.Dial("tcp", distributorAddr)
	if err != nil {
		log.Fatalf("Failed to connect to distributor: %v", err)
	}
	defer conn.Close()

	log.Printf("Connected to distributor at %s", distributorAddr)

	// Send initial weight
	if err := sendWeight(conn, weight); err != nil {
		log.Fatalf("Failed to send initial weight: %v", err)
	}

	log.Printf("Sent initial weight %.2f", weight)

	// Start message processing and per-second tracking
	var messageCount uint64
	var lastAckedSeqNum uint64
	var invalidChecksums uint64
	
	// Priority-based message counting (256 priorities)
	var priorityCounts [256]uint64

	// Setup signal handler for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Printf("Analyzer %s processed %d messages", analyzerID, atomic.LoadUint64(&messageCount))
		log.Printf("Analyzer %s invalid checksums: %d", analyzerID, atomic.LoadUint64(&invalidChecksums))
		
		// Log priority distribution
		log.Printf("Priority distribution:")
		for i := 0; i < 256; i++ {
			count := atomic.LoadUint64(&priorityCounts[i])
			if count > 0 {
				log.Printf("  Priority %d: %d messages", i, count)
			}
		}
		os.Exit(0)
	}()

	// Per-second message tracking for weight validation
	perSecondCounts := make(map[int64]uint64)
	// Per-second priority tracking
	perSecondPriorityCounts := make(map[int64][256]uint64)
	var perSecondMutex sync.RWMutex
	
	// Start per-second reporting goroutine
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		
		for range ticker.C {
			now := time.Now().Unix()
			perSecondMutex.RLock()
			currentSecondCount := perSecondCounts[now]
			prevSecondCount := perSecondCounts[now-1]
			prevSecondPriorities := perSecondPriorityCounts[now-1]
			perSecondMutex.RUnlock()
			
			if prevSecondCount > 0 {
				log.Printf("Per-second stats: %d msg/s (current: %d, prev: %d, total: %d, invalid: %d, weight: %.3f, analyzer: %s)",
					prevSecondCount, currentSecondCount, prevSecondCount, 
					atomic.LoadUint64(&messageCount), atomic.LoadUint64(&invalidChecksums), weight, analyzerID)
				
				// Log priority breakdown for the previous second
				var priorityStats []string
				for i := 0; i < 256; i++ {
					if prevSecondPriorities[i] > 0 {
						priorityStats = append(priorityStats, fmt.Sprintf("P%d:%d", i, prevSecondPriorities[i]))
					}
				}
				if len(priorityStats) > 0 {
					log.Printf("  Priority breakdown: %s", strings.Join(priorityStats, ", "))
				}
			}
			
			// Clean old entries (keep last 10 seconds)
			perSecondMutex.Lock()
			for timestamp := range perSecondCounts {
				if now-timestamp > 10 {
					delete(perSecondCounts, timestamp)
					delete(perSecondPriorityCounts, timestamp)
				}
			}
			perSecondMutex.Unlock()
		}
	}()

	log.Printf("Starting to receive messages...")
	bufReader := bufio.NewReader(conn)
	lengthBuffer := make([]byte, 4)

	for {
		// Read message length (4 bytes)
		if _, err := io.ReadFull(bufReader, lengthBuffer); err != nil {
			log.Printf("Error reading message length: %v", err)
			break
		}

		messageLength := binary.BigEndian.Uint32(lengthBuffer)

		// Read severity (1 byte)
		severity, err := bufReader.ReadByte()
		if err != nil {
			log.Printf("Error reading severity: %v", err)
			break
		}

		// Read payload (remaining bytes)
		payloadLength := messageLength - 4 - 1 // subtract length and severity bytes
		payloadBuffer := make([]byte, payloadLength)
		if _, err := io.ReadFull(bufReader, payloadBuffer); err != nil {
			log.Printf("Error reading payload: %v", err)
			break
		}

		count := atomic.AddUint64(&messageCount, 1)
		
		// Track priority count
		atomic.AddUint64(&priorityCounts[severity], 1)
		
		// Track per-second message count and priority breakdown
		now := time.Now().Unix()
		perSecondMutex.Lock()
		perSecondCounts[now]++
		priorities := perSecondPriorityCounts[now]
		priorities[severity]++
		perSecondPriorityCounts[now] = priorities
		perSecondMutex.Unlock()

		// Validate checksum if enabled
		if validateChecksums {
			if !validateMessageChecksum(string(payloadBuffer)) {
				atomic.AddUint64(&invalidChecksums, 1)
				if verbose {
					log.Printf("Invalid checksum in message %d", count)
				}
			}
		}

		if verbose {
			log.Printf("Received message %d (severity: %d, size: %d bytes, payload: %.50s...)",
				count, severity, len(payloadBuffer), string(payloadBuffer))
		}

		// Send ACK every N messages
		if count%uint64(ackEvery) == 0 {
			if err := sendACK(conn, count); err != nil {
				log.Printf("Error sending ACK: %v", err)
				break
			}

			if !verbose && count%1000 == 0 {
				log.Printf("Processed %d messages (last ack: %d)", count, lastAckedSeqNum)
			}
		}

		// Simulate weight changes every 5000 messages
		if varyWeight && count%5000 == 0 {
			newWeight := weight * (0.8 + 0.4*rand.Float32()) // Vary weight between 80%-120%
			if err := sendWeight(conn, newWeight); err != nil {
				log.Printf("Error sending weight update: %v", err)
			} else {
				log.Printf("Sent weight update: %.2f -> %.2f", weight, newWeight)
				weight = newWeight
			}
		}
	}

	log.Printf("Analyzer %s processed %d messages", analyzerID, messageCount)
}

func sendWeight(conn net.Conn, weight float32) error {
	weightBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(weightBytes, math.Float32bits(weight))
	_, err := conn.Write(weightBytes)
	return err
}

func sendACK(conn net.Conn, seqNum uint64) error {
	message := make([]byte, 4) // message type + sequence number
	binary.BigEndian.PutUint32(message, uint32(seqNum) | SeqNumMSBMask)
	_, err := conn.Write(message)
	return err
}

func validateMessageChecksum(payload string) bool {
	// Payload format: [emitter_id]:[timestamp]:[counter]:[padding][checksum]
	// Checksum is the last 64 characters (SHA256 hex)
	
	if len(payload) < 64 {
		return false
	}
	
	// Extract the checksum (last 64 chars)
	messageChecksum := payload[len(payload)-64:]
	payloadWithoutChecksum := payload[:len(payload)-64]
	
	// Calculate expected checksum
	hash := sha256.Sum256([]byte(payloadWithoutChecksum))
	expectedChecksum := fmt.Sprintf("%x", hash)
	
	return strings.EqualFold(messageChecksum, expectedChecksum)
}

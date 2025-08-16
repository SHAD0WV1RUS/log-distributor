package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"log"
	"log-distributor/config"
	"math"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

func main() {
	distributorAddr := config.GetEnvWithDefault("LOG_ADDR", "localhost:8080")
	rate           := config.GetEnvIntWithDefault("EMITTER_RATE", 100)
	emitterID      := config.GetEnvWithDefault("EMITTER_ID", "")
	sizeMean       := config.GetEnvFloat64WithDefault("LOG_SIZE_MEAN", 512)
	sizeStddev     := config.GetEnvFloat64WithDefault("LOG_SIZE_STDDEV", 0.5)
	minSize        := config.GetEnvIntWithDefault("LOG_MIN_SIZE", 64)
	maxSize        := config.GetEnvIntWithDefault("LOG_MAX_SIZE", 8192)
	verbose		   := config.GetEnvBoolWithDefault("EMITTER_VERBOSE", false)
	priorityMode   := config.GetEnvWithDefault("EMITTER_PRIORITY_MODE", "single") // single, random, weighted

	if emitterID == "" {
		hostname, _ := os.Hostname()
		emitterID = fmt.Sprintf("emitter_%s_%d", hostname, os.Getpid())
	}

	log.Printf("Starting emitter %s", emitterID)
	log.Printf("Target: %s, Rate: %d msg/s", distributorAddr, rate)
	log.Printf("Message size: log-normal(μ=%.1f, σ=%.2f), range=[%d, %d] bytes", 
		sizeMean, sizeStddev, minSize, maxSize)

	// Connect to distributor
	conn, err := net.Dial("tcp", distributorAddr)
	if err != nil {
		log.Fatalf("Failed to connect to distributor: %v", err)
	}
	defer conn.Close()

	log.Printf("Connected to distributor at %s", distributorAddr)

	// Calculate interval between messages
	interval := time.Second / time.Duration(rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	
	startTime := time.Now()
	messageCount := atomic.Uint64{} 
	bytesSent := atomic.Uint64{}

	var logFinalStats sync.Once
	finalStatsFunc := func() {
		actualDuration := time.Since(startTime)
		actualRate := float64(messageCount.Load()) / actualDuration.Seconds()
		totalBytes := bytesSent.Load()
		
		log.Printf("Emitter %s completed: sent %d messages", emitterID, messageCount.Load())
		log.Printf("Emitter %s final stats: %.2fs duration, %.2f msg/s, %d bytes", 
			emitterID, actualDuration.Seconds(), actualRate, totalBytes)
	}

	// Defer final stats logging for normal returns
	defer logFinalStats.Do(finalStatsFunc)

	log.Printf("Sending messages...")

	bufWriter := bufio.NewWriter(conn)

	// Setup signal handler for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		logFinalStats.Do(finalStatsFunc)
		os.Exit(0)
	}()

	for range ticker.C {
		messageSize := generateMessageSize(sizeMean, sizeStddev, minSize, maxSize)
		priority := generatePriority(priorityMode, int(messageCount.Load()))
		message := createMessage(emitterID, messageSize, int(messageCount.Load()), priority)
		if _, err := bufWriter.Write(message); err != nil {
			log.Printf("Failed to send message %d: %v", messageCount.Load(), err)
			return
		}
		
		count := messageCount.Add(1)
		bytesSent.Add(uint64(len(message)))
		
		if verbose && count%100 == 0 {
			log.Printf("Sent %d messages (%d bytes total)", count, bytesSent.Load())
		} else if count%1000 == 0 {
			log.Printf("Sent %d messages", count)
			if err := bufWriter.Flush(); err != nil {
				log.Printf("Error flushing buffer: %v", err)
				return
			}
		}
	}
}

func generateMessageSize(mean, stddev float64, minSize, maxSize int) int {
	// Log-normal distribution: ln(X) ~ N(μ, σ²)
	// For log-normal, we need to convert mean to the underlying normal distribution
	mu := math.Log(mean) - 0.5*stddev*stddev
	
	// Generate log-normal sample
	normal := rand.NormFloat64()
	logNormalSample := math.Exp(mu + stddev*normal)
	
	// Clamp to bounds
	size := int(math.Round(logNormalSample))
	if size < minSize {
		size = minSize
	}
	if size > maxSize {
		size = maxSize
	}
	
	return size
}

func generatePriority(mode string, counter int) uint8 {
	switch mode {
	case "random":
		// Random priority from 0-15 (only use high priorities for testing)
		return uint8(rand.Intn(16))
	case "weighted":
		// Weighted distribution: 50% priority 0, 30% priority 1, 15% priority 2, 5% priority 3+
		r := rand.Float32()
		if r < 0.5 {
			return 0
		} else if r < 0.8 {
			return 1
		} else if r < 0.95 {
			return 2
		} else {
			return uint8(3 + rand.Intn(5)) // 3-7
		}
	case "cyclic":
		// Cycle through priorities 0-7 
		return uint8(counter % 8)
	default: // "single"
		// Default priority 1 (INFO level)
		return 1
	}
}

func createMessage(emitterID string, payloadSize, counter int, priority uint8) []byte {
	// Message format: [4 bytes: total length][1 byte: severity][payload]
	// Payload format: [emitter_id]:[timestamp]:[counter]:[checksum]:[padding]
	
	timestamp := time.Now().UnixNano()
	basePayload := fmt.Sprintf("%s:%d:%08d:", emitterID, timestamp, counter)
	
	// Calculate remaining space for padding (reserve 64 chars for checksum)
	paddingSize := payloadSize - len(basePayload) - 64
	if paddingSize < 0 {
		paddingSize = 0
	}
	
	// Create padding
	padding := make([]byte, paddingSize)
	for i := range padding {
		padding[i] = byte('A' + (i % 26)) // Cycle through A-Z
	}
	
	// Create payload without checksum
	payloadWithoutChecksum := basePayload + string(padding)
	
	// Calculate SHA256 checksum
	hash := sha256.Sum256([]byte(payloadWithoutChecksum))
	checksum := fmt.Sprintf("%x", hash)
	
	// Final payload
	payload := payloadWithoutChecksum + checksum
	
	// Use provided priority as severity
	severity := priority
	
	// Calculate total length (4 bytes length + 1 byte severity + payload)
	totalLength := 4 + 1 + len(payload)
	
	// Create message
	message := make([]byte, totalLength)
	binary.BigEndian.PutUint32(message[0:4], uint32(totalLength))
	message[4] = severity
	copy(message[5:], payload)
	
	return message
}
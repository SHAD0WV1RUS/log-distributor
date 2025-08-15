package main

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net"
	"os"
	"time"
	"log-distributor/config"
)

func main() {
	distributorAddr := config.GetEnvWithDefault("LOG_ADDR", "localhost:8080")
	rate           := config.GetEnvIntWithDefault("EMITTER_RATE", 100)
	duration       := config.GetEnvIntWithDefault("EMITTER_DURATION", 60)
	emitterID      := config.GetEnvWithDefault("EMITTER_ID", "")
	sizeMean       := config.GetEnvFloat64WithDefault("LOG_SIZE_MEAN", 512)
	sizeStddev     := config.GetEnvFloat64WithDefault("LOG_SIZE_STDDEV", 0.5)
	minSize        := config.GetEnvIntWithDefault("LOG_MIN_SIZE", 64)
	maxSize        := config.GetEnvIntWithDefault("LOG_MAX_SIZE", 8192)

	if emitterID == "" {
		hostname, _ := os.Hostname()
		emitterID = fmt.Sprintf("emitter_%s_%d", hostname, os.Getpid())
	}

	log.Printf("Starting emitter %s", emitterID)
	log.Printf("Target: %s, Rate: %d msg/s, Duration: %ds", distributorAddr, rate, duration)
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
	endTime := startTime.Add(time.Duration(duration) * time.Second)
	messageCount := 0

	log.Printf("Sending messages...")

	for time.Now().Before(endTime) {
		select {
		case <-ticker.C:
			messageSize := generateMessageSize(sizeMean, sizeStddev, minSize, maxSize)
			message := createMessage(emitterID, messageSize, messageCount)
			if err := sendMessage(conn, message); err != nil {
				log.Printf("Failed to send message %d: %v", messageCount, err)
				return
			}
			messageCount++
			
			if messageCount%1000 == 0 {
				log.Printf("Sent %d messages", messageCount)
			}
		}
	}

	actualDuration := time.Since(startTime)
	actualRate := float64(messageCount) / actualDuration.Seconds()
	
	log.Printf("Completed: sent %d messages in %.2fs (%.2f msg/s)", 
		messageCount, actualDuration.Seconds(), actualRate)
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

func createMessage(emitterID string, payloadSize, counter int) []byte {
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
	
	// Add severity byte (INFO = 1)
	severity := byte(1)
	
	// Calculate total length (4 bytes length + 1 byte severity + payload)
	totalLength := 4 + 1 + len(payload)
	
	// Create message
	message := make([]byte, totalLength)
	binary.BigEndian.PutUint32(message[0:4], uint32(totalLength))
	message[4] = severity
	copy(message[5:], payload)
	
	return message
}

func sendMessage(conn net.Conn, message []byte) error {
	_, err := conn.Write(message)
	return err
}
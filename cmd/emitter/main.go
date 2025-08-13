package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"time"
)

func main() {
	var (
		distributorAddr = flag.String("addr", "localhost:8080", "Distributor address")
		rate           = flag.Int("rate", 100, "Messages per second")
		duration       = flag.Int("duration", 60, "Duration in seconds")
		emitterID      = flag.String("id", "", "Emitter ID (auto-generated if not provided)")
		messageSize    = flag.Int("size", 256, "Message payload size in bytes")
	)
	flag.Parse()

	if *emitterID == "" {
		hostname, _ := os.Hostname()
		*emitterID = fmt.Sprintf("emitter_%s_%d", hostname, os.Getpid())
	}

	log.Printf("Starting emitter %s", *emitterID)
	log.Printf("Target: %s, Rate: %d msg/s, Duration: %ds, Message size: %d bytes", 
		*distributorAddr, *rate, *duration, *messageSize)

	// Connect to distributor
	conn, err := net.Dial("tcp", *distributorAddr)
	if err != nil {
		log.Fatalf("Failed to connect to distributor: %v", err)
	}
	defer conn.Close()

	log.Printf("Connected to distributor at %s", *distributorAddr)

	// Calculate interval between messages
	interval := time.Second / time.Duration(*rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Create message template
	messageTemplate := createMessageTemplate(*emitterID, *messageSize)
	
	startTime := time.Now()
	endTime := startTime.Add(time.Duration(*duration) * time.Second)
	messageCount := 0

	log.Printf("Sending messages...")

	for time.Now().Before(endTime) {
		select {
		case <-ticker.C:
			message := createMessage(messageTemplate, messageCount)
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

func createMessageTemplate(emitterID string, payloadSize int) []byte {
	// Message format: [4 bytes: total length][1 byte: severity][payload]
	// Payload format: [emitter_id]:[timestamp]:[counter]:[padding]
	
	// Reserve space for counter (8 digits) and timestamp
	basePayload := fmt.Sprintf("%s:TIMESTAMP:COUNTER:", emitterID)
	paddingSize := payloadSize - len(basePayload) - 8 - 20 // 8 for counter, 20 for timestamp
	
	if paddingSize < 0 {
		paddingSize = 0
	}
	
	padding := make([]byte, paddingSize)
	for i := range padding {
		padding[i] = 'x'
	}
	
	template := fmt.Sprintf("%s%%020d:%%08d:%s", emitterID, string(padding))
	return []byte(template)
}

func createMessage(template []byte, counter int) []byte {
	// Create payload with current timestamp and counter
	timestamp := time.Now().UnixNano()
	payload := fmt.Sprintf(string(template), timestamp, counter)
	
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
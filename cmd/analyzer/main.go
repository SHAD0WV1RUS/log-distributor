package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"os"
	"sync/atomic"
)

const (
	// MSB mask for sequence numbers (bit 31)
	SeqNumMSBMask = uint32(1 << 31)
	// Mask for actual sequence value (bottom 31 bits)
	SeqNumValueMask = uint32(0x7FFFFFFF)
)

func main() {
	var (
		distributorAddr = flag.String("addr", "localhost:8081", "Distributor analyzer port address")
		weight          = flag.Float64("weight", 0.25, "Analyzer weight (0.0 to 1.0)")
		analyzerID      = flag.String("id", "", "Analyzer ID (auto-generated if not provided)")
		ackEvery        = flag.Int("ack-every", 10, "Send ACK every N messages")
		verbose         = flag.Bool("verbose", false, "Verbose logging")
	)
	flag.Parse()

	if *analyzerID == "" {
		hostname, _ := os.Hostname()
		*analyzerID = fmt.Sprintf("analyzer_%s_%d", hostname, os.Getpid())
	}

	log.Printf("Starting analyzer %s with weight %.2f", *analyzerID, *weight)

	// Connect to distributor
	conn, err := net.Dial("tcp", *distributorAddr)
	if err != nil {
		log.Fatalf("Failed to connect to distributor: %v", err)
	}
	defer conn.Close()

	log.Printf("Connected to distributor at %s", *distributorAddr)

	// Send initial weight
	if err := sendWeight(conn, *weight); err != nil {
		log.Fatalf("Failed to send initial weight: %v", err)
	}

	log.Printf("Sent initial weight %.2f", *weight)

	// Start message processing
	var messageCount uint64
	var lastAckedSeqNum uint64

	log.Printf("Starting to receive messages...")

	for {
		// Read message length (4 bytes)
		lengthBuffer := make([]byte, 4)
		if _, err := io.ReadFull(conn, lengthBuffer); err != nil {
			log.Printf("Error reading message length: %v", err)
			break
		}

		messageLength := binary.BigEndian.Uint32(lengthBuffer)

		// Read severity (1 byte)
		severityBuffer := make([]byte, 1)
		if _, err := io.ReadFull(conn, severityBuffer); err != nil {
			log.Printf("Error reading severity: %v", err)
			break
		}

		severity := severityBuffer[0]

		// Read payload (remaining bytes)
		payloadLength := messageLength - 4 - 1 // subtract length and severity bytes
		payloadBuffer := make([]byte, payloadLength)
		if _, err := io.ReadFull(conn, payloadBuffer); err != nil {
			log.Printf("Error reading payload: %v", err)
			break
		}

		count := atomic.AddUint64(&messageCount, 1)

		if *verbose {
			log.Printf("Received message %d (severity: %d, payload: %.50s...)",
				count, severity, string(payloadBuffer))
		}

		// Send ACK every N messages
		if count%uint64(*ackEvery) == 0 {
			if err := sendACK(conn, count); err != nil {
				log.Printf("Error sending ACK: %v", err)
				break
			}

			if !*verbose && count%1000 == 0 {
				log.Printf("Processed %d messages (last ack: %d)", count, lastAckedSeqNum)
			}
		}

		// Simulate weight changes every 5000 messages
		if count%5000 == 0 {
			newWeight := *weight * (0.8 + 0.4*float64(count%3)/2.0) // Vary weight between 80%-120%
			if err := sendWeight(conn, newWeight); err != nil {
				log.Printf("Error sending weight update: %v", err)
			} else {
				log.Printf("Sent weight update: %.2f -> %.2f", *weight, newWeight)
				*weight = newWeight
			}
		}
	}

	log.Printf("Analyzer %s processed %d messages", *analyzerID, messageCount)
}

func sendWeight(conn net.Conn, weight float64) error {
	weightBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(weightBytes, math.Float32bits(float32(weight)))
	_, err := conn.Write(weightBytes)
	return err
}

func sendACK(conn net.Conn, seqNum uint64) error {
	message := make([]byte, 4) // message type + sequence number
	binary.BigEndian.PutUint32(message, uint32(seqNum) | SeqNumMSBMask)
	_, err := conn.Write(message)
	return err
}

package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"log-distributor/internal/distributor"
)

func main() {
	log.Println("Starting Log Distributor...")

	// Create weighted tree router
	router := distributor.NewWeightedTreeRouter()

	// Create and start emitter server (receives log messages from emitters)
	emitterServer := distributor.NewEmitterServer(8080, router)
	if err := emitterServer.Start(); err != nil {
		log.Fatalf("Failed to start emitter server: %v", err)
	}

	// Create and start analyzer server (manages connections to analyzers)
	ackTimeout := 30 * time.Second
	analyzerServer := distributor.NewAnalyzerServer(8081, router, ackTimeout)
	if err := analyzerServer.Start(); err != nil {
		log.Fatalf("Failed to start analyzer server: %v", err)
	}

	log.Println("Distributor started successfully")
	log.Println("Emitter server listening on port 8080")
	log.Println("Analyzer server listening on port 8081")

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down distributor...")

	// Graceful shutdown
	emitterServer.Stop()
	analyzerServer.Stop()

	log.Println("Distributor shut down complete")
}
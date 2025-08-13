package config

import "time"

const (
	// Default ports
	DefaultEmitterPort  = 8080
	DefaultAnalyzerPort = 8081
	
	// Default timeouts
	DefaultAckTimeout = 30 * time.Second
	DefaultReadTimeout = 5 * time.Second
	
	// Buffer sizes
	DefaultChannelBuffer = 1000
	DefaultReadBuffer = 4096
)
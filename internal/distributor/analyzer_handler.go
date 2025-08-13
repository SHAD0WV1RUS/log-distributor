package distributor

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
	"container/list"
	"math"
)

const (
	// MSB mask for sequence numbers (bit 31)
	SeqNumMSBMask = uint32(1 << 31)
	// Mask for actual sequence value (bottom 31 bits)
	SeqNumValueMask = uint32(0x7FFFFFFF)
)

// PendingMessage represents a message waiting for acknowledgement
type PendingMessage struct {
	message LogMessage
	sentAt  time.Time
}

// AnalyzerHandler manages a connection to a single analyzer
type AnalyzerHandler struct {
	conn         net.Conn
	config       *AnalyzerConfig
	router       RouterInterface

	// Message handling
	inputChannel   chan LogMessage
	pendingQueue   *list.List
	pendingMutex   sync.RWMutex
	lastAckedSeqNum  uint32

	// Configuration
	ackTimeout     time.Duration

	// State management
	analyzerValBuf []byte
	isConnected    atomic.Bool
	wg             sync.WaitGroup
	shutdown       chan struct{}

	// Server reference for cleanup
	serverWg         *sync.WaitGroup
}

// AnalyzerServer manages TCP connections from analyzers
type AnalyzerServer struct {
	port          int
	router        RouterInterface
	listener      net.Listener
	ackTimeout    time.Duration
	
	wg            sync.WaitGroup
	shutdown      chan struct{}
}

// NewAnalyzerServer creates a new analyzer server
func NewAnalyzerServer(port int, router RouterInterface, ackTimeout time.Duration) *AnalyzerServer {
	return &AnalyzerServer{
		port:       port,
		router:     router,
		ackTimeout: ackTimeout,
		shutdown:   make(chan struct{}),
	}
}

// Start begins listening for analyzer connections
func (as *AnalyzerServer) Start() error {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", as.port))
	if err != nil {
		return fmt.Errorf("failed to start analyzer server on port %d: %w", as.port, err)
	}

	as.listener = listener
	log.Printf("Analyzer server listening on port %d", as.port)

	go as.acceptConnections()
	return nil
}

// Stop gracefully shuts down the analyzer server
func (as *AnalyzerServer) Stop() {
	close(as.shutdown)
	if as.listener != nil {
		as.listener.Close()
	}
	as.wg.Wait()
}

// acceptConnections handles incoming analyzer connections
func (as *AnalyzerServer) acceptConnections() {
	for {
		select {
		case <-as.shutdown:
			return
		default:
			conn, err := as.listener.Accept()
			if err != nil {
				select {
				case <-as.shutdown:
					return
				default:
					log.Printf("Error accepting analyzer connection: %v", err)
					continue
				}
			}

			// Create an analyzer config based on the connection
			inputChannel := make(chan LogMessage, 1000)
			analyzerID := fmt.Sprintf("analyzer_%s", conn.RemoteAddr().String())
			config := &AnalyzerConfig{
				AnalyzerID:   analyzerID,
				InputChannel: inputChannel,
			}
			log.Printf("New analyzer connected: %s", analyzerID)

			// Create and start a new AnalyzerHandler for this connection
			handler := &AnalyzerHandler{
				conn:         conn,
				analyzerValBuf: make([]byte, 4),
				router:       as.router,
				config:       config,
				inputChannel: inputChannel,
				ackTimeout:   as.ackTimeout,
				shutdown:     make(chan struct{}),
				pendingQueue: list.New(),
				serverWg:       &as.wg,
			}

			as.wg.Add(1)
			go handler.handleConnection()
		}
	}
}

// handleConnection manages the lifecycle of a single analyzer connection
func (ah *AnalyzerHandler) handleConnection() {
	defer ah.serverWg.Done()
	defer ah.conn.Close()
	defer ah.cleanup()

	log.Printf("Starting to handle connection for %s", ah.config.AnalyzerID)

	// Read initial weight from first 4 bytes
	if _, err := io.ReadFull(ah.conn, ah.analyzerValBuf); err != nil {
		log.Printf("Failed to read initial weight from analyzer %s: %v", ah.config.AnalyzerID, err)
		return
	}

	// Extract weight value (MSB should be 0 for weight)
	weightBits := binary.BigEndian.Uint32(ah.analyzerValBuf)
	if weightBits & SeqNumMSBMask != 0 {
		log.Printf("Invalid initial weight from analyzer %s: MSB should be 0", ah.config.AnalyzerID)
		return
	}

	ah.config.Weight = math.Float32frombits(weightBits)
	ah.router.RegisterAnalyzer(ah.config)
	log.Printf("Analyzer %s registered with weight %.3f", ah.config.AnalyzerID, ah.config.Weight)

	// Start handler goroutines
	ah.isConnected.Store(true)
	ah.startHandlerRoutines()
}

// startHandlerRoutines starts all necessary goroutines for the handler
func (ah *AnalyzerHandler) startHandlerRoutines() {
	// Start message processor
	ah.wg.Add(1)
	go ah.processMessages()

	// Start message from analyzer processor (acks and weight updates)
	ah.wg.Add(1)
	go ah.handleAnalyzerMessages()

	// Start timeout checker
	ah.wg.Add(1)
	go ah.checkTimeouts()

	ah.wg.Wait()
}

// cleanup handles cleanup when connection closes
func (ah *AnalyzerHandler) cleanup() {
	ah.router.UnregisterAnalyzer(ah.config)
	ah.flushPendingMessages()
	log.Printf("Analyzer disconnected: %s", ah.config.AnalyzerID)
}

// Stop gracefully stops the analyzer handler
func (ah *AnalyzerHandler) Stop() {
	close(ah.shutdown)
	ah.isConnected.Store(false)
	if ah.conn != nil {
		ah.conn.Close()
	}
}

// processMessages handles incoming log messages from router
func (ah *AnalyzerHandler) processMessages() {
	defer ah.wg.Done()

	for {
		select {
		case <-ah.shutdown:
			return
		case msg := <-ah.inputChannel:
			if !ah.isConnected.Load() {
				// Not connected, reroute
				ah.router.RouteMessage(msg)
				continue
			}

			pending := &PendingMessage{
				message: msg,
				sentAt:  time.Now(),
			}
		
			// Add to pending queue
			ah.pendingMutex.Lock()
			ah.pendingQueue.PushBack(pending)
			ah.pendingMutex.Unlock()

			_, err := ah.conn.Write(msg.GetData())
			if err != nil {
				log.Printf("Failed to send message to analyzer %s: %v", ah.config.AnalyzerID, err)
				ah.handleDisconnection()
			}
		}
	}
}

// handleAnalyzerMessages processes messages from the analyzer (acks and weight updates)
func (ah *AnalyzerHandler) handleAnalyzerMessages() {
	defer ah.wg.Done()

	for {
		select {
		case <-ah.shutdown:
			return
		default:
			if !ah.isConnected.Load() {
				return
			}

			// Read 4 bytes at a time
			buffer := make([]byte, 4)
			ah.conn.SetReadDeadline(time.Now().Add(time.Second))
			
			if _, err := io.ReadFull(ah.conn, buffer); err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				if err != io.EOF {
					log.Printf("Error reading from analyzer %s: %v", ah.config.AnalyzerID, err)
				}
				ah.handleDisconnection()
				return
			}

			value := binary.BigEndian.Uint32(buffer)
			
			// Check MSB to determine message type
			if value&SeqNumMSBMask != 0 {
				// MSB = 1: This is a sequence number ACK
				seqNum := value & SeqNumValueMask
				ah.handleAck(seqNum)
			} else {
				// MSB = 0: This is a weight update
				newWeight := math.Float32frombits(value)
				log.Printf("Analyzer %s updated weight from %.3f to %.3f", ah.config.AnalyzerID, ah.config.Weight, newWeight)
				ah.router.UpdateWeight(ah.config, newWeight)
			}
		}
	}
}

// handleAck processes an acknowledgement
func (ah *AnalyzerHandler) handleAck(ackedSeqNum uint32) {
	ah.pendingMutex.Lock()
	defer ah.pendingMutex.Unlock()

	e := ah.pendingQueue.Front()
	for e != nil && ah.lastAckedSeqNum != ackedSeqNum {
		old := e
		e = e.Next()
		ah.pendingQueue.Remove(old)
		ah.lastAckedSeqNum = (ah.lastAckedSeqNum + 1) & SeqNumValueMask
	}
}

// checkTimeouts checks for and handles message timeouts
func (ah *AnalyzerHandler) checkTimeouts() {
	defer ah.wg.Done()

	ticker := time.NewTicker(ah.ackTimeout / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ah.shutdown:
			return
		case <-ticker.C:
			if ah.isConnected.Load() {
				ah.processTimeouts()
			}
		}
	}
}

// processTimeouts identifies and handles timed-out messages
func (ah *AnalyzerHandler) processTimeouts() {
	ah.pendingMutex.Lock()
	defer ah.pendingMutex.Unlock()

	now := time.Now()
	e := ah.pendingQueue.Front()
	for e != nil {
		pending := e.Value.(*PendingMessage)
		next := e.Next()
		if now.Sub(pending.sentAt) > ah.ackTimeout {
			log.Printf("Message timeout for analyzer %s", ah.config.AnalyzerID)
			ah.shutdown <- struct{}{}
		}
		e = next
	}
}

// handleDisconnection handles analyzer disconnection
func (ah *AnalyzerHandler) handleDisconnection() {
	if !ah.isConnected.CompareAndSwap(true, false) {
		return // Already disconnected
	}

	log.Printf("Analyzer %s disconnected", ah.config.AnalyzerID)

	if ah.conn != nil {
		ah.conn.Close()
		ah.conn = nil
	}

	// Flush pending messages
	ah.flushPendingMessages()
}

// flushPendingMessages reroutes all pending messages
func (ah *AnalyzerHandler) flushPendingMessages() {
	ah.pendingMutex.Lock()
	count := 0
	for e := ah.pendingQueue.Front(); e != nil; e = e.Next() {
		ah.router.RouteMessage(e.Value.(*PendingMessage).message)
		count++
	}
	ah.pendingQueue.Init()
	ah.pendingMutex.Unlock()

	log.Printf("Flushed %d pending messages from analyzer %s", count, ah.config.AnalyzerID)
}
package distributor

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// Buffer pool for message allocation
var messagePool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 0, 8192) // Start with 8KB capacity
	},
}

type LogMessage interface {
	GetData() []byte
	GetLength() int
	GetPriority() uint8
}

type ByteSliceMessage []byte

// GetData returns the message data
func (m ByteSliceMessage) GetData() []byte {
	return []byte(m)
}

// GetLength returns the message length
func (m ByteSliceMessage) GetLength() int {
	if len(m) >= 4 {
        return int(binary.BigEndian.Uint32(m[0:4]))
    }
    return 0
}

// GetPriority returns the priority (severity) byte (0 = highest priority)
func (m ByteSliceMessage) GetPriority() uint8 {
	if len(m) >= 5 {
		return m[4]  // 5th byte is the severity/priority
	}
	return 255  // Default to lowest priority if malformed
}

// EmitterHandler manages a single TCP connection from an emitter
type EmitterHandler struct {
	conn      net.Conn
	emitterID string
	router    RouterInterface
	wg        *sync.WaitGroup
}

// EmitterServer manages the TCP server for receiving emitter connections
type EmitterServer struct {
	port     int
	router   RouterInterface
	listener net.Listener
	wg       sync.WaitGroup
	shutdown chan struct{}
}

// NewEmitterServer creates a new emitter server
func NewEmitterServer(port int, router RouterInterface) *EmitterServer {
	return &EmitterServer{
		port:     port,
		router:   router,
		shutdown: make(chan struct{}),
	}
}

// Start begins listening for emitter connections
func (es *EmitterServer) Start() error {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", es.port))
	if err != nil {
		return fmt.Errorf("failed to start emitter server on port %d: %w", es.port, err)
	}
	
	es.listener = listener
	log.Printf("Emitter server listening on port %d\n", es.port)
	
	go es.acceptConnections()
	return nil
}

// Stop gracefully shuts down the emitter server
func (es *EmitterServer) Stop() {
	close(es.shutdown)
	if es.listener != nil {
		es.listener.Close()
	}
	es.wg.Wait()
}

// acceptConnections handles incoming emitter connections
func (es *EmitterServer) acceptConnections() {
	for {
		select {
		case <-es.shutdown:
			return
		default:
			conn, err := es.listener.Accept()
			if err != nil {
				select {
				case <-es.shutdown:
					return
				default:
					log.Printf("Error accepting connection: %v\n", err)
					continue
				}
			}
			
			// Configure TCP connection for reliability and performance
			if tcpConn, ok := conn.(*net.TCPConn); ok {
				tcpConn.SetKeepAlive(true)
				tcpConn.SetKeepAlivePeriod(30 * time.Second)
				tcpConn.SetNoDelay(true) // Disable Nagle's algorithm for low latency
			}
			
			// Create a unique emitter ID based on the connection
			emitterID := fmt.Sprintf("emitter_%s", conn.RemoteAddr().String())
			log.Printf("New emitter connected: %s\n", emitterID)
			
			// Create and start a new EmitterHandler for this connection
			handler := &EmitterHandler{
				conn:      conn,
				emitterID: emitterID,
				router:    es.router,
				wg:        &es.wg,
			}
			
			es.wg.Add(1)
			go handler.handleConnection()
		}
	}
}

// handleConnection manages the lifecycle of a single emitter connection
func (eh *EmitterHandler) handleConnection() {
	defer eh.wg.Done()
	defer eh.conn.Close()
	
	log.Printf("Starting to handle connection for %s\n", eh.emitterID)
	
	// Buffer for reading data
	bufReader := bufio.NewReader(eh.conn)
	lenBuf := make([]byte, 4)
	for {
		// Read data from the connection
		_, err := io.ReadFull(bufReader, lenBuf)
		if err != nil {
			if err != io.EOF {
				log.Printf("Error reading from emitter %s: %v\n", eh.emitterID, err)
			} else {
				log.Printf("Emitter %s disconnected\n", eh.emitterID)
			}
			return
		}

		length := int(binary.BigEndian.Uint32(lenBuf))
		
		// Get buffer from pool
		buffer := messagePool.Get().([]byte)
		if cap(buffer) < length {
			buffer = make([]byte, length)
		} else {
			buffer = buffer[:length]
		}
		copy(buffer, lenBuf)

		_, err = io.ReadFull(bufReader, buffer[4:])
		if err != nil {
			// Return buffer to pool before returning
			if cap(buffer) <= 8192 {
				messagePool.Put(buffer[:0])
			}
			if err != io.EOF {
				log.Printf("Error reading from emitter %s: %v\n", eh.emitterID, err)
			} else {
				log.Printf("Emitter %s disconnected\n", eh.emitterID)
			}
			return
		}
		
		// Route message - the router should handle pooling return
		eh.router.RouteMessage(ByteSliceMessage(buffer))
	}
}
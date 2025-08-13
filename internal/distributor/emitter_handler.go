package distributor

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
)

type LogMessage interface {
	GetData() []byte
	GetLength() int
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

// EmitterHandler manages a single TCP connection from an emitter
type EmitterHandler struct {
	conn      net.Conn
	emitterID string
	router    RouterInterface
	inConstruction ByteSliceMessage
	lengthBuffer []byte
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
			
			// Create a unique emitter ID based on the connection
			emitterID := fmt.Sprintf("emitter_%s", conn.RemoteAddr().String())
			log.Printf("New emitter connected: %s\n", emitterID)
			
			// Create and start a new EmitterHandler for this connection
			handler := &EmitterHandler{
				conn:      conn,
				emitterID: emitterID,
				router:    es.router,
				lengthBuffer: make([]byte, 4),
				inConstruction: nil,
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
	
	for {
		// Read data from the connection
		_, err := io.ReadFull(eh.conn, eh.lengthBuffer)
		if err != nil {
			if err != io.EOF {
				log.Printf("Error reading from emitter %s: %v\n", eh.emitterID, err)
			} else {
				log.Printf("Emitter %s disconnected\n", eh.emitterID)
			}
			return
		}

		length := int(binary.BigEndian.Uint32(eh.lengthBuffer))
		eh.inConstruction = make(ByteSliceMessage, length)
		copy(eh.inConstruction, eh.lengthBuffer)

		_, err = io.ReadFull(eh.conn, eh.inConstruction[4:])
		if err != nil {
			if err != io.EOF {
				log.Printf("Error reading from emitter %s: %v\n", eh.emitterID, err)
			} else {
				log.Printf("Emitter %s disconnected\n", eh.emitterID)
			}
			return
		}
		eh.router.RouteMessage(eh.inConstruction)
	}
}
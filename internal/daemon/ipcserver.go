package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

// JSONRPCRequest represents a JSON-RPC 2.0 request.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      json.RawMessage `json:"id"`
}

// JSONRPCResponse represents a JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
	ID      json.RawMessage `json:"id"`
}

// JSONRPCError represents a JSON-RPC 2.0 error object.
type JSONRPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Standard JSON-RPC 2.0 error codes.
const (
	ErrCodeParse      = -32700
	ErrCodeInvalidReq = -32600
	ErrCodeNoMethod   = -32601
	ErrCodeInternal   = -32603
)

// Handler is a function that handles a JSON-RPC method call.
// It receives the raw params and returns a result or error.
type Handler func(params json.RawMessage) (interface{}, error)

// IPCServer listens on a Unix socket and handles JSON-RPC 2.0 requests.
// Each client connection is served in its own goroutine. Requests within
// a connection are processed sequentially (newline-delimited JSON).
type IPCServer struct {
	socketPath string
	listener   net.Listener
	handlers   map[string]Handler
	shutdownCh <-chan struct{}

	// wg tracks active client connections for graceful drain.
	wg sync.WaitGroup

	// mu protects listener during concurrent close.
	mu      sync.Mutex
	started bool
	closed  bool
}

// NewIPCServer creates an IPCServer that will listen on the given socket path.
// The shutdownCh should be closed when the daemon begins shutdown; this causes
// the server to stop accepting new connections.
func NewIPCServer(socketPath string, shutdownCh <-chan struct{}) *IPCServer {
	return &IPCServer{
		socketPath: socketPath,
		handlers:   make(map[string]Handler),
		shutdownCh: shutdownCh,
	}
}

// Handle registers a handler for the given JSON-RPC method name.
// Must be called before Start.
func (s *IPCServer) Handle(method string, h Handler) {
	s.handlers[method] = h
}

// Start begins listening on the Unix socket. It removes any stale socket
// file, creates the listener, and spawns a goroutine to accept connections.
// Returns an error if the socket cannot be created.
func (s *IPCServer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return fmt.Errorf("ipc: server already started")
	}

	// Remove stale socket file if it exists.
	if err := removeStaleSocket(s.socketPath); err != nil {
		return fmt.Errorf("ipc: %w", err)
	}

	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("ipc: listen: %w", err)
	}

	// Set socket permissions to owner-only (0700).
	if err := os.Chmod(s.socketPath, 0700); err != nil {
		ln.Close()
		return fmt.Errorf("ipc: chmod: %w", err)
	}

	s.listener = ln
	s.started = true

	go s.acceptLoop()
	return nil
}

// Stop closes the listener and waits for in-flight connections to drain.
// The timeout controls how long to wait before returning; connections are
// not forcibly closed (the caller should cancel contexts or close the daemon).
func (s *IPCServer) Stop(timeout time.Duration) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	if s.listener != nil {
		s.listener.Close()
	}
	s.mu.Unlock()

	// Wait for in-flight connections with timeout.
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		log.Printf("ipc: drain timeout after %v, %s", timeout, "some connections may be abandoned")
	}

	// Remove socket file.
	os.Remove(s.socketPath)
}

// SocketPath returns the path to the Unix socket.
func (s *IPCServer) SocketPath() string {
	return s.socketPath
}

// acceptLoop accepts connections until the listener is closed.
func (s *IPCServer) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// Check if we're shutting down.
			select {
			case <-s.shutdownCh:
				return
			default:
			}
			// Check if listener was closed.
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			log.Printf("ipc: accept error: %v", err)
			continue
		}

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConnection(conn)
		}()
	}
}

// handleConnection processes JSON-RPC requests on a single connection.
// Requests are newline-delimited JSON, processed sequentially.
func (s *IPCServer) handleConnection(conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	// Allow up to 1MB per line for large payloads.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		// Check for shutdown between requests.
		select {
		case <-s.shutdownCh:
			return
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		resp := s.processRequest(line)
		s.writeResponse(conn, resp)
	}
}

// processRequest parses a JSON-RPC request and dispatches it to the appropriate handler.
func (s *IPCServer) processRequest(data []byte) JSONRPCResponse {
	var req JSONRPCRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			Error: &JSONRPCError{
				Code:    ErrCodeParse,
				Message: "parse error",
			},
			ID: nil,
		}
	}

	if req.JSONRPC != "2.0" {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			Error: &JSONRPCError{
				Code:    ErrCodeInvalidReq,
				Message: "invalid request: jsonrpc must be \"2.0\"",
			},
			ID: req.ID,
		}
	}

	if req.Method == "" {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			Error: &JSONRPCError{
				Code:    ErrCodeInvalidReq,
				Message: "invalid request: method is required",
			},
			ID: req.ID,
		}
	}

	handler, ok := s.handlers[req.Method]
	if !ok {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			Error: &JSONRPCError{
				Code:    ErrCodeNoMethod,
				Message: fmt.Sprintf("method not found: %s", req.Method),
			},
			ID: req.ID,
		}
	}

	result, err := handler(req.Params)
	if err != nil {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			Error: &JSONRPCError{
				Code:    ErrCodeInternal,
				Message: err.Error(),
			},
			ID: req.ID,
		}
	}

	return JSONRPCResponse{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

// writeResponse marshals a JSON-RPC response and writes it as a newline-delimited line.
func (s *IPCServer) writeResponse(conn net.Conn, resp JSONRPCResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		log.Printf("ipc: marshal response error: %v", err)
		return
	}
	data = append(data, '\n')
	conn.Write(data)
}

// removeStaleSocket removes a socket file if it exists and is not actively
// listening. Returns an error only if the file exists and cannot be removed.
func removeStaleSocket(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat socket: %w", err)
	}

	// If it's not a socket, don't remove it.
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("path %s exists but is not a socket", path)
	}

	// Try connecting to see if someone is listening.
	conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
	if err == nil {
		conn.Close()
		return fmt.Errorf("socket %s is already in use", path)
	}

	// Socket exists but nobody is listening — it's stale.
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	return nil
}

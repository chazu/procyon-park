package cli

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/chazu/procyon-park/internal/ipc"
)

// methodHandler handles a JSON-RPC request by method name.
type methodHandler func(params json.RawMessage) (json.RawMessage, *ipc.Error)

// startMockDaemon creates a mock daemon Unix socket that routes requests by method.
// Returns the socket path. The server handles multiple sequential connections.
func startMockDaemon(t *testing.T, handlers map[string]methodHandler) string {
	t.Helper()
	sockPath := filepath.Join("/tmp", "cli-test-"+t.Name()+".sock")
	os.Remove(sockPath)
	t.Cleanup(func() { os.Remove(sockPath) })

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleConn(conn, handlers)
		}
	}()

	return sockPath
}

func handleConn(conn net.Conn, handlers map[string]methodHandler) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !scanner.Scan() {
		return
	}

	var req ipc.Request
	json.Unmarshal(scanner.Bytes(), &req)

	var resp ipc.Response
	resp.JSONRPC = "2.0"
	resp.ID = json.RawMessage(`1`)

	if handler, ok := handlers[req.Method]; ok {
		result, rpcErr := handler(req.Params)
		resp.Result = result
		resp.Error = rpcErr
	} else {
		resp.Error = &ipc.Error{Code: -32601, Message: "method not found: " + req.Method}
	}

	data, _ := json.Marshal(resp)
	conn.Write(append(data, '\n'))
}

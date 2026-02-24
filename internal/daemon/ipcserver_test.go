package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/chazu/maggie/vm"
	"github.com/chazu/procyon-park/internal/tuplestore"
)

// ---------------------------------------------------------------------------
// Socket Creation and Cleanup Tests
// ---------------------------------------------------------------------------

func TestIPCServerCreatesSocket(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	srv := NewIPCServer(sockPath, shutdownCh)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Stop(time.Second)

	// Socket file should exist
	info, err := os.Stat(sockPath)
	if err != nil {
		t.Fatalf("socket file not created: %v", err)
	}

	// Should be a socket
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatal("file is not a socket")
	}

	// Permissions should be 0700
	if perm := info.Mode().Perm(); perm != 0700 {
		t.Fatalf("expected permissions 0700, got %04o", perm)
	}
}

func TestIPCServerRemovesSocketOnStop(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	srv := NewIPCServer(sockPath, shutdownCh)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	srv.Stop(time.Second)

	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Fatal("socket file should be removed after Stop")
	}
}

func TestIPCServerRemovesStaleSocket(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	// Create a stale socket (a listener that we immediately close).
	// Remove any file shortSockPath may have left, then create the stale socket.
	os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("create stale socket: %v", err)
	}
	ln.Close()

	// The stale socket file still exists but nobody is listening.
	srv := NewIPCServer(sockPath, shutdownCh)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start should succeed over stale socket: %v", err)
	}
	defer srv.Stop(time.Second)

	// Should be able to connect to the new server.
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("cannot connect to new server: %v", err)
	}
	conn.Close()
}

func TestIPCServerRejectsActiveSocket(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	// Start a real listener to simulate an active socket.
	os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("create active socket: %v", err)
	}
	defer ln.Close()

	srv := NewIPCServer(sockPath, shutdownCh)
	err = srv.Start()
	if err == nil {
		srv.Stop(time.Second)
		t.Fatal("Start should fail when socket is in use")
	}
}

func TestIPCServerRejectsNonSocket(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "not-a-socket")

	// Create a regular file.
	if err := os.WriteFile(filePath, []byte("hello"), 0644); err != nil {
		t.Fatalf("create file: %v", err)
	}

	shutdownCh := make(chan struct{})
	srv := NewIPCServer(filePath, shutdownCh)
	err := srv.Start()
	if err == nil {
		srv.Stop(time.Second)
		t.Fatal("Start should fail when path is not a socket")
	}
}

func TestIPCServerDoubleStart(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	srv := NewIPCServer(sockPath, shutdownCh)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Stop(time.Second)

	err := srv.Start()
	if err == nil {
		t.Fatal("second Start should fail")
	}
}

func TestIPCServerDoubleStop(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	srv := NewIPCServer(sockPath, shutdownCh)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Double stop should not panic.
	srv.Stop(time.Second)
	srv.Stop(time.Second)
}

func TestIPCServerSocketPath(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})
	srv := NewIPCServer(sockPath, shutdownCh)

	if srv.SocketPath() != sockPath {
		t.Fatalf("expected %q, got %q", sockPath, srv.SocketPath())
	}
}

// ---------------------------------------------------------------------------
// Connection Handling Tests
// ---------------------------------------------------------------------------

func TestIPCServerAcceptsConnection(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	srv := NewIPCServer(sockPath, shutdownCh)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Stop(time.Second)

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	conn.Close()
}

func TestIPCServerConcurrentConnections(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	srv := NewIPCServer(sockPath, shutdownCh)
	srv.Handle("echo", func(params json.RawMessage) (interface{}, error) {
		var p map[string]interface{}
		json.Unmarshal(params, &p)
		return p, nil
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Stop(2 * time.Second)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()

			conn, err := net.Dial("unix", sockPath)
			if err != nil {
				t.Errorf("connection %d: dial failed: %v", n, err)
				return
			}
			defer conn.Close()

			req := fmt.Sprintf(`{"jsonrpc":"2.0","method":"echo","params":{"n":%d},"id":%d}`, n, n)
			req += "\n"
			if _, err := conn.Write([]byte(req)); err != nil {
				t.Errorf("connection %d: write failed: %v", n, err)
				return
			}

			scanner := bufio.NewScanner(conn)
			if !scanner.Scan() {
				t.Errorf("connection %d: no response", n)
				return
			}

			var resp JSONRPCResponse
			if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
				t.Errorf("connection %d: unmarshal failed: %v", n, err)
				return
			}

			if resp.Error != nil {
				t.Errorf("connection %d: unexpected error: %s", n, resp.Error.Message)
			}
		}(i)
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// JSON-RPC Request/Response Tests
// ---------------------------------------------------------------------------

// rpcCall sends a JSON-RPC request over a Unix socket and returns the response.
func rpcCall(t *testing.T, sockPath string, req string) JSONRPCResponse {
	t.Helper()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(2 * time.Second))

	if _, err := conn.Write([]byte(req + "\n")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		t.Fatalf("no response (scanner err: %v)", scanner.Err())
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v (raw: %s)", err, scanner.Bytes())
	}
	return resp
}

func TestIPCServerEchoMethod(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	srv := NewIPCServer(sockPath, shutdownCh)
	srv.Handle("echo", func(params json.RawMessage) (interface{}, error) {
		return json.RawMessage(params), nil
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"echo","params":{"msg":"hello"},"id":1}`)

	if resp.JSONRPC != "2.0" {
		t.Fatalf("expected jsonrpc 2.0, got %s", resp.JSONRPC)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	// Check the result contains our params.
	resultJSON, _ := json.Marshal(resp.Result)
	if string(resultJSON) != `{"msg":"hello"}` {
		t.Fatalf("unexpected result: %s", resultJSON)
	}

	// Check the ID matches.
	if string(resp.ID) != "1" {
		t.Fatalf("expected id 1, got %s", resp.ID)
	}
}

func TestIPCServerMethodNotFound(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	srv := NewIPCServer(sockPath, shutdownCh)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"nonexistent","id":1}`)

	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != ErrCodeNoMethod {
		t.Fatalf("expected error code %d, got %d", ErrCodeNoMethod, resp.Error.Code)
	}
}

func TestIPCServerInvalidJSON(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	srv := NewIPCServer(sockPath, shutdownCh)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{not valid json}`)

	if resp.Error == nil {
		t.Fatal("expected parse error")
	}
	if resp.Error.Code != ErrCodeParse {
		t.Fatalf("expected error code %d, got %d", ErrCodeParse, resp.Error.Code)
	}
}

func TestIPCServerInvalidVersion(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	srv := NewIPCServer(sockPath, shutdownCh)
	srv.Handle("test", func(params json.RawMessage) (interface{}, error) {
		return nil, nil
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"1.0","method":"test","id":1}`)

	if resp.Error == nil {
		t.Fatal("expected invalid request error")
	}
	if resp.Error.Code != ErrCodeInvalidReq {
		t.Fatalf("expected error code %d, got %d", ErrCodeInvalidReq, resp.Error.Code)
	}
}

func TestIPCServerMissingMethod(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	srv := NewIPCServer(sockPath, shutdownCh)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","id":1}`)

	if resp.Error == nil {
		t.Fatal("expected error for missing method")
	}
	if resp.Error.Code != ErrCodeInvalidReq {
		t.Fatalf("expected error code %d, got %d", ErrCodeInvalidReq, resp.Error.Code)
	}
}

func TestIPCServerHandlerError(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	srv := NewIPCServer(sockPath, shutdownCh)
	srv.Handle("fail", func(params json.RawMessage) (interface{}, error) {
		return nil, fmt.Errorf("something went wrong")
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"fail","id":1}`)

	if resp.Error == nil {
		t.Fatal("expected error from handler")
	}
	if resp.Error.Code != ErrCodeInternal {
		t.Fatalf("expected error code %d, got %d", ErrCodeInternal, resp.Error.Code)
	}
	if resp.Error.Message != "something went wrong" {
		t.Fatalf("expected error message 'something went wrong', got %q", resp.Error.Message)
	}
}

func TestIPCServerStringID(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	srv := NewIPCServer(sockPath, shutdownCh)
	srv.Handle("ping", func(params json.RawMessage) (interface{}, error) {
		return "pong", nil
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"ping","id":"abc-123"}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if string(resp.ID) != `"abc-123"` {
		t.Fatalf("expected id \"abc-123\", got %s", resp.ID)
	}
}

func TestIPCServerNullID(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	srv := NewIPCServer(sockPath, shutdownCh)
	srv.Handle("ping", func(params json.RawMessage) (interface{}, error) {
		return "pong", nil
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Stop(time.Second)

	resp := rpcCall(t, sockPath, `{"jsonrpc":"2.0","method":"ping","id":null}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if string(resp.ID) != "null" {
		t.Fatalf("expected null id, got %s", resp.ID)
	}
}

func TestIPCServerMultipleRequestsPerConnection(t *testing.T) {
	sockPath := shortSockPath(t)
	shutdownCh := make(chan struct{})

	srv := NewIPCServer(sockPath, shutdownCh)
	srv.Handle("add", func(params json.RawMessage) (interface{}, error) {
		var p struct {
			A int `json:"a"`
			B int `json:"b"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return p.A + p.B, nil
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Stop(time.Second)

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))

	scanner := bufio.NewScanner(conn)

	// Send three requests on the same connection.
	for i := 1; i <= 3; i++ {
		req := fmt.Sprintf(`{"jsonrpc":"2.0","method":"add","params":{"a":%d,"b":%d},"id":%d}`, i, i*10, i)
		conn.Write([]byte(req + "\n"))

		if !scanner.Scan() {
			t.Fatalf("request %d: no response", i)
		}

		var resp JSONRPCResponse
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("request %d: unmarshal: %v", i, err)
		}
		if resp.Error != nil {
			t.Fatalf("request %d: error: %s", i, resp.Error.Message)
		}

		// result is a float64 from JSON unmarshaling.
		expected := float64(i + i*10)
		resultF, ok := resp.Result.(float64)
		if !ok {
			t.Fatalf("request %d: result not a number: %T %v", i, resp.Result, resp.Result)
		}
		if resultF != expected {
			t.Fatalf("request %d: expected %v, got %v", i, expected, resultF)
		}
	}
}

// ---------------------------------------------------------------------------
// Daemon Integration Tests
// ---------------------------------------------------------------------------

func TestDaemonServerWithIPCSocket(t *testing.T) {
	sockPath := shortSockPath(t)
	pidPath := filepath.Join(t.TempDir(), "daemon.pid")

	v := mustNewVM(t)
	store := mustNewStore(t)

	d := New(v, store, Config{
		SocketPath:      sockPath,
		PIDPath:         pidPath,
		ShutdownTimeout: 2,
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Give the daemon a moment to start.
	time.Sleep(100 * time.Millisecond)

	// IPC server should be running.
	if d.IPCServer() == nil {
		t.Fatal("IPCServer should not be nil after Run")
	}

	// Socket file should exist.
	if _, err := os.Stat(sockPath); err != nil {
		t.Fatalf("socket file not created: %v", err)
	}

	// Should be connectable.
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("cannot connect to daemon socket: %v", err)
	}
	conn.Close()

	// Shutdown.
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not shut down")
	}

	// Socket should be cleaned up.
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Fatal("socket file should be removed after shutdown")
	}
}

func TestDaemonServerSocketFromDataDir(t *testing.T) {
	// Use a short DataDir so the socket path fits.
	dir, err := os.MkdirTemp("/tmp", "ipc")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	pidPath := filepath.Join(dir, "daemon.pid")

	v := mustNewVM(t)
	store := mustNewStore(t)

	d := New(v, store, Config{
		DataDir:         dir,
		PIDPath:         pidPath,
		ShutdownTimeout: 2,
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	// Socket should be at DataDir/daemon.sock.
	expectedPath := filepath.Join(dir, "daemon.sock")
	if _, err := os.Stat(expectedPath); err != nil {
		t.Fatalf("socket file not at expected path %s: %v", expectedPath, err)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not shut down")
	}
}

func TestDaemonServerNoSocket(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "test.pid")

	v := mustNewVM(t)
	store := mustNewStore(t)

	// No SocketPath and no DataDir — should not start IPC server.
	d := New(v, store, Config{
		PIDPath:         pidPath,
		ShutdownTimeout: 2,
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	time.Sleep(50 * time.Millisecond)

	if d.IPCServer() != nil {
		t.Fatal("IPCServer should be nil when no socket path is configured")
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not shut down")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// shortSockPath returns a socket path short enough for Unix domain sockets
// (max 108 bytes on macOS). Uses /tmp with a unique name per test.
func shortSockPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ipc")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "test.sock")
}

func mustNewVM(t *testing.T) *vm.VM {
	t.Helper()
	return vm.NewVM()
}

func mustNewStore(t *testing.T) *tuplestore.TupleStore {
	t.Helper()
	s, err := tuplestore.NewMemoryStore()
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	return s
}

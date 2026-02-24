// Phase 2 integration tests: full daemon lifecycle end-to-end.
// Exercises start → connect → JSON-RPC BBS ops → checkpoint → shutdown → restart with recovery.
package test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chazu/maggie/vm"
	"github.com/chazu/procyon-park/internal/checkpoint"
	"github.com/chazu/procyon-park/internal/daemon"
	"github.com/chazu/procyon-park/internal/tuplestore"
)

// ---------------------------------------------------------------------------
// JSON-RPC helpers (mirrors internal/daemon tests but from external package)
// ---------------------------------------------------------------------------

type jsonRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
	ID      int         `json:"id"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
	ID      int             `json:"id"`
}

// rpcDial connects to a Unix socket and returns the connection.
func rpcDial(t *testing.T, sockPath string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", sockPath, err)
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	return conn
}

// rpcSend sends a JSON-RPC request and reads the response on an existing connection.
func rpcSend(t *testing.T, conn net.Conn, method string, params interface{}, id int) jsonRPCResponse {
	t.Helper()
	req := jsonRPCRequest{JSONRPC: "2.0", Method: method, Params: params, ID: id}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("write request: %v", err)
	}

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		t.Fatalf("no response (err: %v)", scanner.Err())
	}

	var resp jsonRPCResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v (raw: %s)", err, scanner.Bytes())
	}
	return resp
}

// rpcCall opens a connection, sends one request, reads the response, and closes.
func rpcCall(t *testing.T, sockPath, method string, params interface{}, id int) jsonRPCResponse {
	t.Helper()
	conn := rpcDial(t, sockPath)
	defer conn.Close()
	return rpcSend(t, conn, method, params, id)
}

// shortSockDir returns a temp directory under /tmp with a short path suitable
// for Unix domain sockets (max 108 bytes on macOS).
func shortSockDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pp-int")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// ---------------------------------------------------------------------------
// Daemon lifecycle helpers
// ---------------------------------------------------------------------------

type testDaemon struct {
	server       *daemon.DaemonServer
	sockPath     string
	pidPath      string
	dataDir      string
	store        *tuplestore.TupleStore
	vm           *vm.VM
	cancel       context.CancelFunc
	errCh        chan error
	shutdownDone bool
}

// startDaemon creates and starts a daemon with an IPC socket. The daemon
// runs in a background goroutine and is cleaned up by the test.
func startDaemon(t *testing.T) *testDaemon {
	t.Helper()
	dir := shortSockDir(t)
	return startDaemonInDir(t, dir)
}

func startDaemonInDir(t *testing.T, dir string) *testDaemon {
	t.Helper()
	sockPath := filepath.Join(dir, "d.sock")
	pidPath := filepath.Join(dir, "d.pid")

	v := vm.NewVM()
	store, err := tuplestore.NewStore(filepath.Join(dir, "tuples.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	d := daemon.New(v, store, daemon.Config{
		SocketPath:      sockPath,
		PIDPath:         pidPath,
		DataDir:         dir,
		ShutdownTimeout: 2,
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Wait for socket to appear.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sockPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(sockPath); err != nil {
		cancel()
		t.Fatalf("socket not created within timeout: %v", err)
	}

	td := &testDaemon{
		server:   d,
		sockPath: sockPath,
		pidPath:  pidPath,
		dataDir:  dir,
		store:    store,
		vm:       v,
		cancel:   cancel,
		errCh:    errCh,
	}

	t.Cleanup(func() {
		if !td.shutdownDone {
			cancel()
			select {
			case <-errCh:
			case <-time.After(5 * time.Second):
			}
		}
	})

	return td
}

// shutdown cancels the daemon and waits for it to exit.
func (td *testDaemon) shutdown(t *testing.T) {
	t.Helper()
	td.cancel()
	select {
	case err := <-td.errCh:
		if err != nil {
			t.Fatalf("daemon Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not shut down within timeout")
	}
	td.shutdownDone = true
}

// ---------------------------------------------------------------------------
// Test 1: Full lifecycle — start, connect, BBS operations, shutdown
// ---------------------------------------------------------------------------

func TestIntegrationDaemonLifecycle(t *testing.T) {
	td := startDaemon(t)

	// 1. Verify daemon is reachable via Unix socket.
	conn := rpcDial(t, td.sockPath)
	defer conn.Close()

	// 2. tuple.write
	writeResp := rpcSend(t, conn, "tuple.write", map[string]interface{}{
		"category":  "fact",
		"scope":     "test-repo",
		"identity":  "lang",
		"payload":   `{"content":"Go"}`,
		"lifecycle": "session",
	}, 1)
	if writeResp.Error != nil {
		t.Fatalf("tuple.write error: %s", writeResp.Error.Message)
	}
	var writeResult map[string]interface{}
	json.Unmarshal(writeResp.Result, &writeResult)
	if writeResult["id"] == nil {
		t.Fatal("tuple.write should return an id")
	}

	// 3. tuple.read — should find the tuple we just wrote.
	readResp := rpcSend(t, conn, "tuple.read", map[string]interface{}{
		"category": "fact",
		"scope":    "test-repo",
	}, 2)
	if readResp.Error != nil {
		t.Fatalf("tuple.read error: %s", readResp.Error.Message)
	}
	var readResult map[string]interface{}
	json.Unmarshal(readResp.Result, &readResult)
	if readResult["identity"] != "lang" {
		t.Fatalf("expected identity 'lang', got %v", readResult["identity"])
	}

	// 4. Write a second tuple, then tuple.scan — should find both.
	rpcSend(t, conn, "tuple.write", map[string]interface{}{
		"category":  "fact",
		"scope":     "test-repo",
		"identity":  "version",
		"payload":   `{"content":"1.22"}`,
		"lifecycle": "session",
	}, 3)

	scanResp := rpcSend(t, conn, "tuple.scan", map[string]interface{}{
		"category": "fact",
		"scope":    "test-repo",
	}, 4)
	if scanResp.Error != nil {
		t.Fatalf("tuple.scan error: %s", scanResp.Error.Message)
	}
	var scanResult []interface{}
	json.Unmarshal(scanResp.Result, &scanResult)
	if len(scanResult) != 2 {
		t.Fatalf("expected 2 tuples from scan, got %d", len(scanResult))
	}

	// 5. tuple.take — should atomically remove one tuple.
	takeResp := rpcSend(t, conn, "tuple.take", map[string]interface{}{
		"category": "fact",
		"scope":    "test-repo",
		"identity": "lang",
	}, 5)
	if takeResp.Error != nil {
		t.Fatalf("tuple.take error: %s", takeResp.Error.Message)
	}
	var takeResult map[string]interface{}
	json.Unmarshal(takeResp.Result, &takeResult)
	if takeResult["identity"] != "lang" {
		t.Fatalf("expected take to return 'lang', got %v", takeResult["identity"])
	}

	// 6. Verify the taken tuple is gone.
	scanResp2 := rpcSend(t, conn, "tuple.scan", map[string]interface{}{
		"category": "fact",
		"scope":    "test-repo",
	}, 6)
	var scanResult2 []interface{}
	json.Unmarshal(scanResp2.Result, &scanResult2)
	if len(scanResult2) != 1 {
		t.Fatalf("expected 1 tuple after take, got %d", len(scanResult2))
	}

	conn.Close()

	// 7. Graceful shutdown.
	td.shutdown(t)

	// 8. Socket and PID file should be cleaned up.
	if _, err := os.Stat(td.sockPath); !os.IsNotExist(err) {
		t.Fatal("socket file should be removed after shutdown")
	}
	if _, err := os.Stat(td.pidPath); !os.IsNotExist(err) {
		t.Fatal("PID file should be removed after shutdown")
	}
}

// ---------------------------------------------------------------------------
// Test 2: Checkpoint on disk — write tuples, checkpoint, verify file exists
// ---------------------------------------------------------------------------

func TestIntegrationCheckpointOnDisk(t *testing.T) {
	dir := shortSockDir(t)
	sockPath := filepath.Join(dir, "d.sock")
	pidPath := filepath.Join(dir, "d.pid")
	imagePath := filepath.Join(dir, "test.image")

	v := vm.NewVM()
	store, err := tuplestore.NewStore(filepath.Join(dir, "tuples.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	d := daemon.New(v, store, daemon.Config{
		SocketPath:      sockPath,
		PIDPath:         pidPath,
		DataDir:         dir,
		ShutdownTimeout: 2,
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Wait for socket.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sockPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Set up checkpoint manager.
	mgr := checkpoint.NewManagerForVM(v, imagePath, 1*time.Hour) // long interval; we trigger manually
	if err := mgr.Start(); err != nil {
		cancel()
		t.Fatalf("checkpoint Start: %v", err)
	}

	// Write a tuple via IPC to exercise the system.
	rpcCall(t, sockPath, "tuple.write", map[string]interface{}{
		"category":  "fact",
		"scope":     "checkpoint-test",
		"identity":  "item-1",
		"payload":   `{"data":"hello"}`,
		"lifecycle": "session",
	}, 1)

	// Trigger a manual checkpoint.
	if err := mgr.CheckpointNow(); err != nil {
		t.Fatalf("CheckpointNow: %v", err)
	}

	// Verify image file exists and is non-empty.
	info, err := os.Stat(imagePath)
	if err != nil {
		t.Fatalf("image file not found: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("image file is empty")
	}
	if mgr.Count() != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", mgr.Count())
	}
	if mgr.LastError() != nil {
		t.Fatalf("unexpected checkpoint error: %v", mgr.LastError())
	}
	if mgr.LastSave().IsZero() {
		t.Fatal("lastSave should not be zero after checkpoint")
	}

	mgr.Stop()
	cancel()
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not shut down")
	}
}

// ---------------------------------------------------------------------------
// Test 3: State recovery — checkpoint, shutdown, restart, verify data persists
// ---------------------------------------------------------------------------

func TestIntegrationStateRecovery(t *testing.T) {
	dir := shortSockDir(t)
	imagePath := filepath.Join(dir, "test.image")
	dbPath := filepath.Join(dir, "tuples.db")

	// --- Phase 1: Start daemon, write tuples, checkpoint, shutdown ---

	v1 := vm.NewVM()
	store1, err := tuplestore.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore (phase 1): %v", err)
	}

	sockPath := filepath.Join(dir, "d.sock")
	pidPath := filepath.Join(dir, "d.pid")

	d1 := daemon.New(v1, store1, daemon.Config{
		SocketPath:      sockPath,
		PIDPath:         pidPath,
		DataDir:         dir,
		ShutdownTimeout: 2,
	})

	ctx1, cancel1 := context.WithCancel(context.Background())
	errCh1 := make(chan error, 1)
	go func() {
		errCh1 <- d1.Run(ctx1)
	}()

	// Wait for socket.
	waitForSocket(t, sockPath, 3*time.Second)

	// Write tuples via IPC.
	for i := 0; i < 5; i++ {
		resp := rpcCall(t, sockPath, "tuple.write", map[string]interface{}{
			"category":  "fact",
			"scope":     "recovery-test",
			"identity":  fmt.Sprintf("item-%d", i),
			"payload":   fmt.Sprintf(`{"n":%d}`, i),
			"lifecycle": "session",
		}, i+1)
		if resp.Error != nil {
			t.Fatalf("tuple.write %d: %s", i, resp.Error.Message)
		}
	}

	// Checkpoint the VM image.
	mgr := checkpoint.NewManagerForVM(v1, imagePath, 1*time.Hour)
	mgr.Start()
	if err := mgr.CheckpointNow(); err != nil {
		t.Fatalf("CheckpointNow: %v", err)
	}
	mgr.Stop()

	// Shutdown phase 1.
	cancel1()
	select {
	case err := <-errCh1:
		if err != nil {
			t.Fatalf("phase 1 Run error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("phase 1 shutdown timeout")
	}

	// --- Phase 2: Restart with same DB, verify tuples persist ---

	// Recover the image (verifies recovery logic).
	recoveredPath, err := checkpoint.RecoverImage(imagePath)
	if err != nil {
		t.Fatalf("RecoverImage: %v", err)
	}
	if recoveredPath != imagePath {
		t.Fatalf("expected recovered path %q, got %q", imagePath, recoveredPath)
	}

	v2 := vm.NewVM()
	if err := v2.LoadImage(recoveredPath); err != nil {
		t.Fatalf("LoadImage: %v", err)
	}

	store2, err := tuplestore.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore (phase 2): %v", err)
	}

	d2 := daemon.New(v2, store2, daemon.Config{
		SocketPath:      sockPath,
		PIDPath:         pidPath,
		DataDir:         dir,
		ShutdownTimeout: 2,
	})

	ctx2, cancel2 := context.WithCancel(context.Background())
	errCh2 := make(chan error, 1)
	go func() {
		errCh2 <- d2.Run(ctx2)
	}()

	waitForSocket(t, sockPath, 3*time.Second)

	// Verify all 5 tuples are still present via IPC.
	scanResp := rpcCall(t, sockPath, "tuple.scan", map[string]interface{}{
		"category": "fact",
		"scope":    "recovery-test",
	}, 100)
	if scanResp.Error != nil {
		t.Fatalf("tuple.scan (phase 2): %s", scanResp.Error.Message)
	}
	var scanResult []interface{}
	json.Unmarshal(scanResp.Result, &scanResult)
	if len(scanResult) != 5 {
		t.Fatalf("expected 5 tuples after restart, got %d", len(scanResult))
	}

	// Verify we can still write new tuples.
	writeResp := rpcCall(t, sockPath, "tuple.write", map[string]interface{}{
		"category":  "fact",
		"scope":     "recovery-test",
		"identity":  "post-restart",
		"payload":   `{"restarted":true}`,
		"lifecycle": "session",
	}, 101)
	if writeResp.Error != nil {
		t.Fatalf("tuple.write (phase 2): %s", writeResp.Error.Message)
	}

	// Final count should be 6.
	scanResp2 := rpcCall(t, sockPath, "tuple.scan", map[string]interface{}{
		"category": "fact",
		"scope":    "recovery-test",
	}, 102)
	var scanResult2 []interface{}
	json.Unmarshal(scanResp2.Result, &scanResult2)
	if len(scanResult2) != 6 {
		t.Fatalf("expected 6 tuples after post-restart write, got %d", len(scanResult2))
	}

	// Shutdown phase 2.
	cancel2()
	select {
	case err := <-errCh2:
		if err != nil {
			t.Fatalf("phase 2 Run error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("phase 2 shutdown timeout")
	}
}

// ---------------------------------------------------------------------------
// Test 4: Concurrent IPC clients
// ---------------------------------------------------------------------------

func TestIntegrationConcurrentClients(t *testing.T) {
	td := startDaemon(t)

	// Spawn 10 concurrent clients, each writing and reading.
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func(n int) {
			conn := rpcDial(t, td.sockPath)
			defer conn.Close()

			// Write a tuple.
			writeResp := rpcSend(t, conn, "tuple.write", map[string]interface{}{
				"category":  "claim",
				"scope":     "concurrent-test",
				"identity":  fmt.Sprintf("client-%d", n),
				"payload":   fmt.Sprintf(`{"client":%d}`, n),
				"lifecycle": "session",
			}, n*10+1)
			if writeResp.Error != nil {
				errs <- fmt.Errorf("client %d write: %s", n, writeResp.Error.Message)
				return
			}

			// Read it back.
			readResp := rpcSend(t, conn, "tuple.read", map[string]interface{}{
				"category": "claim",
				"scope":    "concurrent-test",
				"identity": fmt.Sprintf("client-%d", n),
			}, n*10+2)
			if readResp.Error != nil {
				errs <- fmt.Errorf("client %d read: %s", n, readResp.Error.Message)
				return
			}

			errs <- nil
		}(i)
	}

	for i := 0; i < 10; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent client error: %v", err)
		}
	}

	// Verify all 10 tuples exist.
	scanResp := rpcCall(t, td.sockPath, "tuple.scan", map[string]interface{}{
		"category": "claim",
		"scope":    "concurrent-test",
	}, 999)
	var scanResult []interface{}
	json.Unmarshal(scanResp.Result, &scanResult)
	if len(scanResult) != 10 {
		t.Fatalf("expected 10 tuples, got %d", len(scanResult))
	}

	td.shutdown(t)
}

// ---------------------------------------------------------------------------
// Test 5: Multiple requests on single connection
// ---------------------------------------------------------------------------

func TestIntegrationMultipleRequestsSingleConn(t *testing.T) {
	td := startDaemon(t)

	conn := rpcDial(t, td.sockPath)
	defer conn.Close()

	// Send 20 sequential requests on the same connection.
	for i := 0; i < 20; i++ {
		resp := rpcSend(t, conn, "tuple.write", map[string]interface{}{
			"category":  "event",
			"scope":     "multi-req",
			"identity":  fmt.Sprintf("evt-%d", i),
			"payload":   `{}`,
			"lifecycle": "session",
		}, i+1)
		if resp.Error != nil {
			t.Fatalf("request %d error: %s", i, resp.Error.Message)
		}
	}

	// Verify all 20 via a scan on the same connection.
	scanResp := rpcSend(t, conn, "tuple.scan", map[string]interface{}{
		"category": "event",
		"scope":    "multi-req",
	}, 100)
	var scanResult []interface{}
	json.Unmarshal(scanResp.Result, &scanResult)
	if len(scanResult) != 20 {
		t.Fatalf("expected 20 tuples, got %d", len(scanResult))
	}

	conn.Close()
	td.shutdown(t)
}

// ---------------------------------------------------------------------------
// Test 6: JSON-RPC error handling through daemon
// ---------------------------------------------------------------------------

func TestIntegrationErrorHandling(t *testing.T) {
	td := startDaemon(t)

	// Method not found.
	resp1 := rpcCall(t, td.sockPath, "bogus.method", nil, 1)
	if resp1.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp1.Error.Code != -32601 { // ErrCodeNoMethod
		t.Fatalf("expected code -32601, got %d", resp1.Error.Code)
	}

	// Invalid params for tuple.write (missing category).
	resp2 := rpcCall(t, td.sockPath, "tuple.write", map[string]interface{}{
		"scope": "test",
	}, 2)
	if resp2.Error == nil {
		t.Fatal("expected error for missing category")
	}
	if resp2.Error.Code != -32602 { // ErrCodeInvalidParams
		t.Fatalf("expected code -32602, got %d", resp2.Error.Code)
	}

	// tuple.take on non-existent tuple — should return null, not error.
	resp3 := rpcCall(t, td.sockPath, "tuple.take", map[string]interface{}{
		"category": "nonexistent",
	}, 3)
	if resp3.Error != nil {
		t.Fatalf("unexpected error: %s", resp3.Error.Message)
	}
	if string(resp3.Result) != "null" {
		t.Fatalf("expected null result for take of nonexistent tuple, got %s", resp3.Result)
	}

	td.shutdown(t)
}

// ---------------------------------------------------------------------------
// Test 7: Checkpoint recovery edge cases
// ---------------------------------------------------------------------------

func TestIntegrationCheckpointRecoveryNoImage(t *testing.T) {
	dir := shortSockDir(t)
	imagePath := filepath.Join(dir, "nonexistent.image")

	_, err := checkpoint.RecoverImage(imagePath)
	if err != checkpoint.ErrNoImage {
		t.Fatalf("expected ErrNoImage, got %v", err)
	}
}

func TestIntegrationCheckpointRecoverFromPrev(t *testing.T) {
	dir := shortSockDir(t)
	imagePath := filepath.Join(dir, "test.image")
	prevPath := imagePath + ".prev"

	// Create a valid .prev image.
	v := vm.NewVM()
	if err := v.SaveImageAtomic(prevPath); err != nil {
		t.Fatalf("SaveImageAtomic (.prev): %v", err)
	}

	// No main image exists — recovery should promote .prev.
	recovered, err := checkpoint.RecoverImage(imagePath)
	if err != nil {
		t.Fatalf("RecoverImage: %v", err)
	}
	if recovered != imagePath {
		t.Fatalf("expected recovered path %q, got %q", imagePath, recovered)
	}

	// Main image should now exist.
	if _, err := os.Stat(imagePath); err != nil {
		t.Fatalf("main image not found after recovery: %v", err)
	}

	// .prev should be gone.
	if _, err := os.Stat(prevPath); !os.IsNotExist(err) {
		t.Fatal(".prev should be removed after promotion")
	}

	// Verify we can load the recovered image.
	v2 := vm.NewVM()
	if err := v2.LoadImage(recovered); err != nil {
		t.Fatalf("LoadImage from recovered .prev: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Test 8: Full round-trip — write, read, scan, take, verify consistency
// ---------------------------------------------------------------------------

func TestIntegrationBBSRoundTrip(t *testing.T) {
	td := startDaemon(t)

	// Write tuples of different categories.
	categories := []string{"fact", "claim", "obstacle", "event"}
	for i, cat := range categories {
		resp := rpcCall(t, td.sockPath, "tuple.write", map[string]interface{}{
			"category":  cat,
			"scope":     "roundtrip",
			"identity":  fmt.Sprintf("%s-1", cat),
			"payload":   fmt.Sprintf(`{"type":"%s"}`, cat),
			"lifecycle": "session",
		}, i+1)
		if resp.Error != nil {
			t.Fatalf("write %s: %s", cat, resp.Error.Message)
		}
	}

	// Scan all — should get 4.
	allResp := rpcCall(t, td.sockPath, "tuple.scan", map[string]interface{}{
		"scope": "roundtrip",
	}, 10)
	var allResult []interface{}
	json.Unmarshal(allResp.Result, &allResult)
	if len(allResult) != 4 {
		t.Fatalf("expected 4 tuples, got %d", len(allResult))
	}

	// Scan only facts — should get 1.
	factResp := rpcCall(t, td.sockPath, "tuple.scan", map[string]interface{}{
		"category": "fact",
		"scope":    "roundtrip",
	}, 11)
	var factResult []interface{}
	json.Unmarshal(factResp.Result, &factResult)
	if len(factResult) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(factResult))
	}

	// Read the claim (non-destructive).
	readResp := rpcCall(t, td.sockPath, "tuple.read", map[string]interface{}{
		"category": "claim",
		"scope":    "roundtrip",
	}, 12)
	if readResp.Error != nil {
		t.Fatalf("read claim: %s", readResp.Error.Message)
	}
	var readResult map[string]interface{}
	json.Unmarshal(readResp.Result, &readResult)
	if readResult["identity"] != "claim-1" {
		t.Fatalf("expected identity 'claim-1', got %v", readResult["identity"])
	}

	// Claim should still be there after read.
	claimScan := rpcCall(t, td.sockPath, "tuple.scan", map[string]interface{}{
		"category": "claim",
		"scope":    "roundtrip",
	}, 13)
	var claimResult []interface{}
	json.Unmarshal(claimScan.Result, &claimResult)
	if len(claimResult) != 1 {
		t.Fatalf("claim should still exist after read, got %d", len(claimResult))
	}

	// Take the obstacle (destructive).
	takeResp := rpcCall(t, td.sockPath, "tuple.take", map[string]interface{}{
		"category": "obstacle",
		"scope":    "roundtrip",
	}, 14)
	if takeResp.Error != nil {
		t.Fatalf("take obstacle: %s", takeResp.Error.Message)
	}

	// Obstacle should be gone.
	obsScan := rpcCall(t, td.sockPath, "tuple.scan", map[string]interface{}{
		"category": "obstacle",
		"scope":    "roundtrip",
	}, 15)
	var obsResult []interface{}
	json.Unmarshal(obsScan.Result, &obsResult)
	if len(obsResult) != 0 {
		t.Fatalf("obstacle should be gone after take, got %d", len(obsResult))
	}

	// Total should now be 3.
	finalResp := rpcCall(t, td.sockPath, "tuple.scan", map[string]interface{}{
		"scope": "roundtrip",
	}, 16)
	var finalResult []interface{}
	json.Unmarshal(finalResp.Result, &finalResult)
	if len(finalResult) != 3 {
		t.Fatalf("expected 3 tuples after take, got %d", len(finalResult))
	}

	td.shutdown(t)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// waitForSocket polls until the socket file appears.
func waitForSocket(t *testing.T, sockPath string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sockPath); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %s not created within %v", sockPath, timeout)
}

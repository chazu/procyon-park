package ipc

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// startTestServer creates a Unix socket server that handles one JSON-RPC request.
// Returns the socket path and a cleanup function.
func startTestServer(t *testing.T, handler func(req Request) Response) string {
	t.Helper()
	sockPath := filepath.Join("/tmp", "ipc-test-"+t.Name()+".sock")
	os.Remove(sockPath)
	t.Cleanup(func() { os.Remove(sockPath) })

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		scanner := bufio.NewScanner(conn)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		if !scanner.Scan() {
			return
		}

		var req Request
		json.Unmarshal(scanner.Bytes(), &req)

		resp := handler(req)
		data, _ := json.Marshal(resp)
		conn.Write(append(data, '\n'))
	}()

	return sockPath
}

func TestCall_Success(t *testing.T) {
	sock := startTestServer(t, func(req Request) Response {
		if req.Method != "test.echo" {
			t.Errorf("expected method test.echo, got %q", req.Method)
		}
		return Response{
			JSONRPC: "2.0",
			Result:  json.RawMessage(`{"msg":"hello"}`),
			ID:      json.RawMessage(`1`),
		}
	})

	result, err := Call(sock, "test.echo", map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var data struct{ Msg string }
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if data.Msg != "hello" {
		t.Fatalf("expected msg=hello, got %q", data.Msg)
	}
}

func TestCall_RPCError(t *testing.T) {
	sock := startTestServer(t, func(req Request) Response {
		return Response{
			JSONRPC: "2.0",
			Error:   &Error{Code: -32601, Message: "method not found"},
			ID:      json.RawMessage(`1`),
		}
	})

	_, err := Call(sock, "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for RPC error response")
	}

	rpcErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", err)
	}
	if rpcErr.Code != -32601 {
		t.Fatalf("expected code -32601, got %d", rpcErr.Code)
	}
}

func TestCall_ConnectionRefused(t *testing.T) {
	_, err := Call("/tmp/nonexistent-ipc-test.sock", "test", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent socket")
	}
}

func TestCall_ParamsMarshaled(t *testing.T) {
	sock := startTestServer(t, func(req Request) Response {
		var params map[string]string
		json.Unmarshal(req.Params, &params)
		if params["repo"] != "test-repo" {
			t.Errorf("expected repo=test-repo, got %q", params["repo"])
		}
		return Response{
			JSONRPC: "2.0",
			Result:  json.RawMessage(`"ok"`),
			ID:      json.RawMessage(`1`),
		}
	})

	_, err := Call(sock, "test.params", map[string]string{"repo": "test-repo"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

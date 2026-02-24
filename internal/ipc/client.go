package ipc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// Call sends a JSON-RPC 2.0 request to the daemon at socketPath and returns
// the raw result. Returns an *Error if the daemon returns a JSON-RPC error.
func Call(socketPath, method string, params interface{}) (json.RawMessage, error) {
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to daemon at %s: %w", socketPath, err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(60 * time.Second))

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}

	req := Request{JSONRPC: "2.0", Method: method, Params: paramsJSON, ID: 1}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	if _, err := conn.Write(append(data, '\n')); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		return nil, fmt.Errorf("no response from daemon")
	}

	var resp Response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if resp.Error != nil {
		return nil, resp.Error
	}

	return resp.Result, nil
}

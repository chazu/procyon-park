package cli

import (
	"fmt"
	"testing"
)

func TestExitCodeConstants(t *testing.T) {
	// Verify exit codes have expected values.
	codes := map[string]int{
		"Success":     ExitSuccess,
		"Error":       ExitError,
		"Usage":       ExitUsage,
		"Connection":  ExitConnection,
		"Timeout":     ExitTimeout,
		"NotFound":    ExitNotFound,
		"Interrupted": ExitInterrupted,
	}
	expected := map[string]int{
		"Success":     0,
		"Error":       1,
		"Usage":       2,
		"Connection":  3,
		"Timeout":     4,
		"NotFound":    5,
		"Interrupted": 130,
	}
	for name, got := range codes {
		if got != expected[name] {
			t.Errorf("Exit%s: expected %d, got %d", name, expected[name], got)
		}
	}
}

func TestExitErr(t *testing.T) {
	inner := fmt.Errorf("connection refused")
	ee := NewExitErr(ExitConnection, inner)

	if ee.Code != ExitConnection {
		t.Errorf("expected code %d, got %d", ExitConnection, ee.Code)
	}
	if ee.Error() != "connection refused" {
		t.Errorf("expected error string %q, got %q", "connection refused", ee.Error())
	}
	if ee.Unwrap() != inner {
		t.Errorf("Unwrap should return inner error")
	}
}

func TestExitCodeFromError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, ExitSuccess},
		{"plain error", fmt.Errorf("oops"), ExitError},
		{"exit err connection", NewExitErr(ExitConnection, fmt.Errorf("no daemon")), ExitConnection},
		{"exit err not found", NewExitErr(ExitNotFound, fmt.Errorf("missing")), ExitNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := exitCodeFromError(tt.err)
			if got != tt.want {
				t.Errorf("exitCodeFromError: expected %d, got %d", tt.want, got)
			}
		})
	}
}

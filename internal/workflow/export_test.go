package workflow

import (
	"context"
	"time"
)

// ExportRunFromWorkflow exposes runFromWorkflow for external test packages.
var ExportRunFromWorkflow = runFromWorkflow

// ExportWaitForTerminal exposes waitForTerminal for external test packages.
func ExportWaitForTerminal(store *Store, repoName, instanceID string, timeout time.Duration) (*Instance, error) {
	deadline := time.After(timeout)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return nil, context.DeadlineExceeded
		case <-ticker.C:
			inst, err := store.GetInstance(repoName, instanceID)
			if err != nil {
				return nil, err
			}
			if inst == nil {
				continue
			}
			switch inst.Status {
			case StatusCompleted, StatusFailed, StatusCancelled:
				return inst, nil
			}
		}
	}
}

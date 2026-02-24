package cli

import (
	"testing"
)

func TestWorkflowCommandRegistered(t *testing.T) {
	wf, _, err := rootCmd.Find([]string{"workflow"})
	if err != nil {
		t.Fatalf("workflow command not found: %v", err)
	}
	if wf.Name() != "workflow" {
		t.Fatalf("expected command name 'workflow', got %q", wf.Name())
	}

	subs := []string{"run", "list", "show", "cancel", "approve", "reject"}
	for _, name := range subs {
		found := false
		for _, sub := range wf.Commands() {
			if sub.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("subcommand %q not found under 'workflow'", name)
		}
	}
}

func TestWorkflowStubsReturnNotImplemented(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"run", []string{"workflow", "run", "test-workflow"}},
		{"list", []string{"workflow", "list"}},
		{"show", []string{"workflow", "show", "test-workflow"}},
		{"cancel", []string{"workflow", "cancel", "wf-123"}},
		{"approve", []string{"workflow", "approve", "wf-123"}},
		{"reject", []string{"workflow", "reject", "wf-123"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rootCmd.SetArgs(tt.args)
			err := rootCmd.Execute()
			if err == nil {
				t.Fatal("expected not-implemented error, got nil")
			}
			ee, ok := err.(*ExitErr)
			if !ok {
				t.Fatalf("expected *ExitErr, got %T: %v", err, err)
			}
			if ee.Code != ExitNotImplemented {
				t.Errorf("expected exit code %d, got %d", ExitNotImplemented, ee.Code)
			}
		})
	}
}

func TestWorkflowRunRequiresArgs(t *testing.T) {
	rootCmd.SetArgs([]string{"workflow", "run"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args, got nil")
	}
}

func TestExitNotImplementedConstant(t *testing.T) {
	if ExitNotImplemented != 10 {
		t.Errorf("ExitNotImplemented should be 10, got %d", ExitNotImplemented)
	}
}

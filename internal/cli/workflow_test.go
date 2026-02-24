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

	subs := []string{"run", "list", "show", "cancel", "approve", "reject", "defs"}
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

func TestWorkflowRunRequiresArgs(t *testing.T) {
	rootCmd.SetArgs([]string{"workflow", "run"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args, got nil")
	}
}

func TestWorkflowRunRequiresRepoName(t *testing.T) {
	// workflow run requires --repo-name flag.
	rootCmd.SetArgs([]string{"workflow", "run", "test-wf"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing --repo-name, got nil")
	}
}

func TestWorkflowShowRequiresArgs(t *testing.T) {
	rootCmd.SetArgs([]string{"workflow", "show"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing instance ID, got nil")
	}
}

func TestWorkflowCancelRequiresArgs(t *testing.T) {
	rootCmd.SetArgs([]string{"workflow", "cancel"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing instance ID, got nil")
	}
}

func TestWorkflowApproveRequiresArgs(t *testing.T) {
	rootCmd.SetArgs([]string{"workflow", "approve"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing instance ID, got nil")
	}
}

func TestWorkflowRejectRequiresArgs(t *testing.T) {
	rootCmd.SetArgs([]string{"workflow", "reject"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing instance ID, got nil")
	}
}

func TestWorkflowDefsNoArgs(t *testing.T) {
	// defs takes no positional args — check it doesn't error on args validation.
	rootCmd.SetArgs([]string{"workflow", "defs", "extra"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for unexpected args, got nil")
	}
}

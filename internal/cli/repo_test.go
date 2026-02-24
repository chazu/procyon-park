package cli

import (
	"testing"
)

func TestRepoCommandRegistered(t *testing.T) {
	// Verify the repo command and its subcommands are registered.
	repo, _, err := rootCmd.Find([]string{"repo"})
	if err != nil {
		t.Fatalf("repo command not found: %v", err)
	}
	if repo.Name() != "repo" {
		t.Fatalf("expected command name 'repo', got %q", repo.Name())
	}

	subs := []string{"register", "unregister", "list", "status"}
	for _, name := range subs {
		found := false
		for _, sub := range repo.Commands() {
			if sub.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("subcommand %q not found under 'repo'", name)
		}
	}
}

func TestRepoRegisterRequiresArgs(t *testing.T) {
	rootCmd.SetArgs([]string{"repo", "register"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args, got nil")
	}
}

func TestRepoUnregisterRequiresArgs(t *testing.T) {
	rootCmd.SetArgs([]string{"repo", "unregister"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args, got nil")
	}
}

func TestRepoStatusAcceptsOptionalArg(t *testing.T) {
	// Verify status accepts 0 or 1 args (doesn't error on arg count).
	// It will error on daemon connection, but not on arg validation.
	rootCmd.SetArgs([]string{"repo", "status"})
	err := rootCmd.Execute()
	// Expected to fail on EnsureDaemon, not arg count.
	if err != nil {
		ee, ok := err.(*ExitErr)
		if ok && ee.Code == ExitUsage {
			t.Fatal("status should accept 0 args without usage error")
		}
	}
}

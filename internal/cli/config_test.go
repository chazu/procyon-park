package cli

import (
	"testing"
)

func TestConfigCommandRegistered(t *testing.T) {
	config, _, err := rootCmd.Find([]string{"config"})
	if err != nil {
		t.Fatalf("config command not found: %v", err)
	}
	if config.Name() != "config" {
		t.Fatalf("expected command name 'config', got %q", config.Name())
	}

	subs := []string{"get", "set", "list", "edit"}
	for _, name := range subs {
		found := false
		for _, sub := range config.Commands() {
			if sub.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("subcommand %q not found under 'config'", name)
		}
	}
}

func TestConfigGetRequiresKey(t *testing.T) {
	rootCmd.SetArgs([]string{"config", "get"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing key arg, got nil")
	}
}

func TestConfigSetRequiresTwoArgs(t *testing.T) {
	rootCmd.SetArgs([]string{"config", "set", "only-key"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing value arg, got nil")
	}
}

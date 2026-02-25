package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()

	if cfg.Agent.Command != "claude" {
		t.Errorf("Agent.Command = %q, want %q", cfg.Agent.Command, "claude")
	}
	if cfg.Agent.MaxConcurrent != 4 {
		t.Errorf("Agent.MaxConcurrent = %d, want 4", cfg.Agent.MaxConcurrent)
	}
	if cfg.Daemon.PollInterval != "5s" {
		t.Errorf("Daemon.PollInterval = %q, want %q", cfg.Daemon.PollInterval, "5s")
	}
	if cfg.Features.BBSEnabled != true {
		t.Error("Features.BBSEnabled should default to true")
	}
	if cfg.Features.WorkflowsEnabled != true {
		t.Error("Features.WorkflowsEnabled should default to true")
	}
	if cfg.Telemetry.Enabled != false {
		t.Error("Telemetry.Enabled should default to false")
	}
}

func TestLoadNoFiles(t *testing.T) {
	Reset()
	// Point global path to a nonexistent dir.
	t.Setenv("HOME", t.TempDir())

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Should get defaults.
	want := defaultConfig()
	if cfg.Agent.Command != want.Agent.Command {
		t.Errorf("Agent.Command = %q, want %q", cfg.Agent.Command, want.Agent.Command)
	}
}

func TestLoadGlobalConfig(t *testing.T) {
	Reset()
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".procyon-park")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(`
[agent]
command = "aider"
max_concurrent = 8

[daemon]
http_port = 9090
`), 0644)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Agent.Command != "aider" {
		t.Errorf("Agent.Command = %q, want %q", cfg.Agent.Command, "aider")
	}
	if cfg.Agent.MaxConcurrent != 8 {
		t.Errorf("Agent.MaxConcurrent = %d, want 8", cfg.Agent.MaxConcurrent)
	}
	if cfg.Daemon.HTTPPort != 9090 {
		t.Errorf("Daemon.HTTPPort = %d, want 9090", cfg.Daemon.HTTPPort)
	}
	// Unset fields should retain defaults.
	if cfg.Daemon.PollInterval != "5s" {
		t.Errorf("Daemon.PollInterval = %q, want %q (default)", cfg.Daemon.PollInterval, "5s")
	}
}

func TestLoadPerRepoOverride(t *testing.T) {
	Reset()
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Global config.
	globalDir := filepath.Join(home, ".procyon-park")
	os.MkdirAll(globalDir, 0755)
	os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte(`
[agent]
command = "aider"
max_concurrent = 8

[telemetry]
enabled = true
endpoint = "https://global.example.com"
`), 0644)

	// Per-repo config: override only command and endpoint.
	repoRoot := t.TempDir()
	repoDir := filepath.Join(repoRoot, ".procyon-park")
	os.MkdirAll(repoDir, 0755)
	os.WriteFile(filepath.Join(repoDir, "config.toml"), []byte(`
[agent]
command = "claude"

[telemetry]
endpoint = "https://repo.example.com"
`), 0644)

	cfg, err := Load(repoRoot)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Per-repo overrides command.
	if cfg.Agent.Command != "claude" {
		t.Errorf("Agent.Command = %q, want %q", cfg.Agent.Command, "claude")
	}
	// Global max_concurrent survives (leaf-level merge).
	if cfg.Agent.MaxConcurrent != 8 {
		t.Errorf("Agent.MaxConcurrent = %d, want 8 (from global)", cfg.Agent.MaxConcurrent)
	}
	// Telemetry.Enabled from global.
	if cfg.Telemetry.Enabled != true {
		t.Error("Telemetry.Enabled should be true (from global)")
	}
	// Endpoint overridden by repo.
	if cfg.Telemetry.Endpoint != "https://repo.example.com" {
		t.Errorf("Telemetry.Endpoint = %q, want repo override", cfg.Telemetry.Endpoint)
	}
}

func TestEnvOverrides(t *testing.T) {
	Reset()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PP_AGENT_COMMAND", "codex")
	t.Setenv("PP_AGENT_MAX_CONCURRENT", "16")
	t.Setenv("PP_TELEMETRY_ENABLED", "true")
	t.Setenv("PP_DAEMON_HTTP_PORT", "4321")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Agent.Command != "codex" {
		t.Errorf("Agent.Command = %q, want %q", cfg.Agent.Command, "codex")
	}
	if cfg.Agent.MaxConcurrent != 16 {
		t.Errorf("Agent.MaxConcurrent = %d, want 16", cfg.Agent.MaxConcurrent)
	}
	if cfg.Telemetry.Enabled != true {
		t.Error("Telemetry.Enabled should be true via env")
	}
	if cfg.Daemon.HTTPPort != 4321 {
		t.Errorf("Daemon.HTTPPort = %d, want 4321", cfg.Daemon.HTTPPort)
	}
}

func TestEnvOverridesAfterTOML(t *testing.T) {
	Reset()
	home := t.TempDir()
	t.Setenv("HOME", home)

	// TOML says command = "aider"
	configDir := filepath.Join(home, ".procyon-park")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(`
[agent]
command = "aider"
`), 0644)

	// Env says command = "codex" — env wins.
	t.Setenv("PP_AGENT_COMMAND", "codex")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Agent.Command != "codex" {
		t.Errorf("Agent.Command = %q, want %q (env override)", cfg.Agent.Command, "codex")
	}
}

func TestValidation(t *testing.T) {
	cfg := defaultConfig()
	if err := Validate(cfg); err != nil {
		t.Fatalf("default config should be valid: %v", err)
	}

	cfg.Agent.MaxConcurrent = -1
	if err := Validate(cfg); err == nil {
		t.Error("expected error for negative MaxConcurrent")
	}

	cfg = defaultConfig()
	cfg.Daemon.HTTPPort = 70000
	if err := Validate(cfg); err == nil {
		t.Error("expected error for port > 65535")
	}
}

func TestUnknownKeysRejected(t *testing.T) {
	Reset()
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".procyon-park")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(`
[agent]
command = "claude"
bogus_key = "oops"
`), 0644)

	_, err := Load("")
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
	if got := err.Error(); !contains(got, "unknown keys") {
		t.Errorf("error = %q, want it to mention unknown keys", got)
	}
}

func TestCaching(t *testing.T) {
	Reset()
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg1, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg2, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg1 != cfg2 {
		t.Error("expected same pointer from cache")
	}

	// Different repo root should get a different entry.
	repoRoot := t.TempDir()
	cfg3, err := Load(repoRoot)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg3 == cfg1 {
		t.Error("different repo roots should not share cache entries")
	}
}

func TestGlobalPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := GlobalPath()
	if err != nil {
		t.Fatalf("GlobalPath: %v", err)
	}
	want := filepath.Join(home, ".procyon-park", "config.toml")
	if path != want {
		t.Errorf("GlobalPath = %q, want %q", path, want)
	}
}

func TestRepoPath(t *testing.T) {
	root := "/tmp/myrepo"
	path := RepoPath(root)
	want := filepath.Join(root, ".procyon-park", "config.toml")
	if path != want {
		t.Errorf("RepoPath = %q, want %q", path, want)
	}
}

func TestBoolEnvValues(t *testing.T) {
	Reset()
	home := t.TempDir()
	t.Setenv("HOME", home)

	for _, val := range []string{"true", "1", "yes"} {
		Reset()
		t.Setenv("PP_TELEMETRY_ENABLED", val)
		cfg, err := Load("")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !cfg.Telemetry.Enabled {
			t.Errorf("PP_TELEMETRY_ENABLED=%q should set Enabled=true", val)
		}
	}

	for _, val := range []string{"false", "0", "no", ""} {
		Reset()
		t.Setenv("PP_TELEMETRY_ENABLED", val)
		cfg, err := Load("")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Telemetry.Enabled {
			t.Errorf("PP_TELEMETRY_ENABLED=%q should set Enabled=false", val)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstring(s, sub))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

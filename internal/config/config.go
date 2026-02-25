// Package config implements two-layer TOML configuration for procyon-park.
// Global config lives at ~/.procyon-park/config.toml, per-repo config at
// <repo>/.procyon-park/config.toml. Per-repo values override globals at
// the leaf field level (not whole sections). Environment variables with
// PP_ prefix override both layers.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

// Config is the top-level configuration structure.
type Config struct {
	Agent     AgentConfig     `toml:"agent"`
	Daemon    DaemonConfig    `toml:"daemon"`
	Telemetry TelemetryConfig `toml:"telemetry"`
	Hub       HubConfig       `toml:"hub"`
	Features  FeaturesConfig  `toml:"features"`
}

// AgentConfig holds agent-related settings.
type AgentConfig struct {
	Command       string `toml:"command" env:"PP_AGENT_COMMAND"`
	MaxConcurrent int    `toml:"max_concurrent" env:"PP_AGENT_MAX_CONCURRENT"`
}

// DaemonConfig holds daemon process settings.
type DaemonConfig struct {
	PollInterval string `toml:"poll_interval" env:"PP_DAEMON_POLL_INTERVAL"`
	HTTPPort     int    `toml:"http_port" env:"PP_DAEMON_HTTP_PORT"`
}

// TelemetryConfig holds telemetry settings.
type TelemetryConfig struct {
	Enabled  bool   `toml:"enabled" env:"PP_TELEMETRY_ENABLED"`
	Endpoint string `toml:"endpoint" env:"PP_TELEMETRY_ENDPOINT"`
}

// HubConfig holds hub/mesh settings.
type HubConfig struct {
	Enabled      bool     `toml:"enabled" env:"PP_HUB_ENABLED"`
	ListenAddr   string   `toml:"listen_addr" env:"PP_HUB_LISTEN_ADDR"`
	Peers        []string `toml:"peers"`
	AutoDiscover bool     `toml:"auto_discover" env:"PP_HUB_AUTO_DISCOVER"`
}

// FeaturesConfig holds feature flag settings.
type FeaturesConfig struct {
	BBSEnabled        bool `toml:"bbs_enabled" env:"PP_FEATURES_BBS_ENABLED"`
	WorkflowsEnabled  bool `toml:"workflows_enabled" env:"PP_FEATURES_WORKFLOWS_ENABLED"`
	TelemetryOTEL     bool `toml:"telemetry_otel" env:"PP_FEATURES_TELEMETRY_OTEL"`
	HubDiscovery      bool `toml:"hub_discovery" env:"PP_FEATURES_HUB_DISCOVERY"`
}

// defaultConfig returns compiled defaults.
func defaultConfig() *Config {
	return &Config{
		Agent: AgentConfig{
			Command:       "claude",
			MaxConcurrent: 4,
		},
		Daemon: DaemonConfig{
			PollInterval: "5s",
			HTTPPort:     0,
		},
		Telemetry: TelemetryConfig{
			Enabled:  false,
			Endpoint: "",
		},
		Hub: HubConfig{
			Enabled:      false,
			ListenAddr:   "",
			AutoDiscover: false,
		},
		Features: FeaturesConfig{
			BBSEnabled:       true,
			WorkflowsEnabled: true,
			TelemetryOTEL:    false,
			HubDiscovery:     false,
		},
	}
}

// globalMu protects the global singleton and the per-repo cache.
var (
	globalMu   sync.Mutex
	globalOnce sync.Once
	globalCfg  *Config
	repoCache  = make(map[string]*Config)
)

// GlobalPath returns the path to the global config file.
func GlobalPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: home dir: %w", err)
	}
	return filepath.Join(home, ".procyon-park", "config.toml"), nil
}

// RepoPath returns the path to the per-repo config file for the given repo root.
func RepoPath(repoRoot string) string {
	return filepath.Join(repoRoot, ".procyon-park", "config.toml")
}

// Load returns the merged configuration for the given repo root.
// It loads defaults, overlays the global config, then the per-repo config,
// then environment variable overrides. The result is cached per repo root.
// Pass empty string for repoRoot to get global-only config.
func Load(repoRoot string) (*Config, error) {
	globalMu.Lock()
	defer globalMu.Unlock()

	key := repoRoot
	if cached, ok := repoCache[key]; ok {
		return cached, nil
	}

	cfg, err := loadUncached(repoRoot)
	if err != nil {
		return nil, err
	}

	repoCache[key] = cfg
	return cfg, nil
}

// loadUncached builds the config without checking the cache.
func loadUncached(repoRoot string) (*Config, error) {
	cfg := defaultConfig()

	// Layer 1: global config
	globalPath, err := GlobalPath()
	if err != nil {
		return nil, err
	}
	if err := loadLayer(globalPath, cfg); err != nil {
		return nil, fmt.Errorf("global config: %w", err)
	}

	// Layer 2: per-repo config
	if repoRoot != "" {
		repoPath := RepoPath(repoRoot)
		if err := loadLayer(repoPath, cfg); err != nil {
			return nil, fmt.Errorf("repo config (%s): %w", repoRoot, err)
		}
	}

	// Layer 3: environment overrides
	applyEnvOverrides(cfg)

	return cfg, nil
}

// loadLayer reads a TOML file and merges it into cfg at the leaf level.
// If the file does not exist, this is a no-op.
func loadLayer(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}

	// Decode into a fresh Config to detect unknown keys.
	var layer Config
	md, err := toml.Decode(string(data), &layer)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	// Reject unknown keys.
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return fmt.Errorf("unknown keys in %s: %s", path, strings.Join(keys, ", "))
	}

	// Leaf-level merge: only overwrite fields that were explicitly set in the TOML.
	mergeDecoded(cfg, &layer, md)

	return nil
}

// mergeDecoded copies fields from src to dst only for keys that were explicitly
// decoded (present in the TOML file). This gives leaf-level merge semantics.
func mergeDecoded(dst, src *Config, md toml.MetaData) {
	dv := reflect.ValueOf(dst).Elem()
	sv := reflect.ValueOf(src).Elem()
	dt := dv.Type()

	for i := 0; i < dt.NumField(); i++ {
		section := dt.Field(i)
		tag := section.Tag.Get("toml")
		if tag == "" {
			continue
		}

		dSection := dv.Field(i)
		sSection := sv.Field(i)

		for j := 0; j < section.Type.NumField(); j++ {
			field := section.Type.Field(j)
			ftag := field.Tag.Get("toml")
			if ftag == "" {
				continue
			}
			fullKey := tag + "." + ftag
			if md.IsDefined(tag, ftag) {
				dSection.Field(j).Set(sSection.Field(j))
			}
			_ = fullKey // suppress unused
		}
	}
}

// applyEnvOverrides reads PP_* environment variables and overwrites matching
// config fields. The env tag on each field specifies the variable name.
func applyEnvOverrides(cfg *Config) {
	v := reflect.ValueOf(cfg).Elem()
	t := v.Type()

	for i := 0; i < t.NumField(); i++ {
		section := v.Field(i)
		st := section.Type()

		for j := 0; j < st.NumField(); j++ {
			field := st.Field(j)
			envKey := field.Tag.Get("env")
			if envKey == "" {
				continue
			}
			envVal, ok := os.LookupEnv(envKey)
			if !ok {
				continue
			}
			setFieldFromString(section.Field(j), envVal)
		}
	}
}

// setFieldFromString sets a reflect.Value from a string, handling bool, int, and string types.
func setFieldFromString(f reflect.Value, s string) {
	switch f.Kind() {
	case reflect.String:
		f.SetString(s)
	case reflect.Int, reflect.Int64:
		var n int64
		fmt.Sscanf(s, "%d", &n)
		f.SetInt(n)
	case reflect.Bool:
		f.SetBool(s == "true" || s == "1" || s == "yes")
	}
}

// Validate checks cfg for invalid values. Returns an error describing all problems.
func Validate(cfg *Config) error {
	var errs []string

	if cfg.Agent.MaxConcurrent < 0 {
		errs = append(errs, "agent.max_concurrent must be >= 0")
	}
	if cfg.Daemon.HTTPPort < 0 || cfg.Daemon.HTTPPort > 65535 {
		errs = append(errs, "daemon.http_port must be 0-65535")
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("config validation: %s", strings.Join(errs, "; "))
}

// Reset clears the singleton cache. Useful for testing.
func Reset() {
	globalMu.Lock()
	defer globalMu.Unlock()
	globalOnce = sync.Once{}
	globalCfg = nil
	repoCache = make(map[string]*Config)
}

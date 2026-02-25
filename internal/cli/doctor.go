// doctor.go implements the 'pp doctor' command for setup validation.
// It runs a series of health checks and reports results with checkmarks/X marks.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chazu/procyon-park/internal/config"
	"github.com/chazu/procyon-park/internal/identity"
	"github.com/chazu/procyon-park/internal/registry"
	"github.com/spf13/cobra"
)

// CheckResult holds the outcome of a single doctor check.
type CheckResult struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

func init() {
	rootCmd.AddCommand(doctorCmd())
}

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Verify system setup and health",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			w := cmd.OutOrStdout()

			dataDir := DataDir()
			results := runDoctorChecks(ctx, dataDir)

			if OutputJSON() {
				data, err := json.MarshalIndent(results, "", "  ")
				if err != nil {
					return fmt.Errorf("marshal results: %w", err)
				}
				fmt.Fprintln(w, string(data))
				return nil
			}

			// Text output with checkmarks.
			failed := 0
			for _, r := range results {
				if r.OK {
					fmt.Fprintf(w, "  %s  %s\n", colorGreen("✓"), r.Message)
				} else {
					fmt.Fprintf(w, "  %s  %s\n", colorRed("✗"), r.Message)
					if r.Fix != "" {
						fmt.Fprintf(w, "      → %s\n", r.Fix)
					}
					failed++
				}
			}

			fmt.Fprintln(w)
			if failed == 0 {
				fmt.Fprintln(w, "All checks passed.")
			} else {
				fmt.Fprintf(w, "%d check(s) failed.\n", failed)
			}
			return nil
		},
	}
}

// runDoctorChecks executes all health checks and returns results.
func runDoctorChecks(ctx context.Context, dataDir string) []CheckResult {
	var results []CheckResult

	results = append(results, checkDataDir(dataDir))
	results = append(results, checkConfigParseable(dataDir))
	results = append(results, checkIdentity(dataDir))
	results = append(results, checkBBS(dataDir))
	results = append(results, checkGit(ctx))
	results = append(results, checkTmux(ctx))
	results = append(results, checkRepos(ctx, dataDir))
	results = append(results, checkDaemonSocket(dataDir))

	return results
}

// checkDataDir verifies ~/.procyon-park/ exists with correct structure.
func checkDataDir(dataDir string) CheckResult {
	info, err := os.Stat(dataDir)
	if err != nil {
		return CheckResult{
			Name:    "data_dir",
			OK:      false,
			Message: fmt.Sprintf("Data directory missing: %s", dataDir),
			Fix:     "Run: pp init",
		}
	}
	if !info.IsDir() {
		return CheckResult{
			Name:    "data_dir",
			OK:      false,
			Message: fmt.Sprintf("%s exists but is not a directory", dataDir),
			Fix:     fmt.Sprintf("Remove %s and run: pp init", dataDir),
		}
	}
	return CheckResult{
		Name:    "data_dir",
		OK:      true,
		Message: fmt.Sprintf("Data directory: %s", dataDir),
	}
}

// checkConfigParseable verifies config.toml parses without errors.
func checkConfigParseable(dataDir string) CheckResult {
	configPath := filepath.Join(dataDir, "config.toml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return CheckResult{
			Name:    "config",
			OK:      false,
			Message: "Config file missing",
			Fix:     "Run: pp init",
		}
	}

	// Reset config cache to force a fresh parse.
	config.Reset()
	cfg, err := config.Load("")
	if err != nil {
		return CheckResult{
			Name:    "config",
			OK:      false,
			Message: fmt.Sprintf("Config parse error: %s", err),
			Fix:     fmt.Sprintf("Check syntax in %s", configPath),
		}
	}
	if err := config.Validate(cfg); err != nil {
		return CheckResult{
			Name:    "config",
			OK:      false,
			Message: fmt.Sprintf("Config validation error: %s", err),
			Fix:     fmt.Sprintf("Fix values in %s", configPath),
		}
	}
	return CheckResult{
		Name:    "config",
		OK:      true,
		Message: "Config parses and validates OK",
	}
}

// checkIdentity verifies node identity exists and is valid.
func checkIdentity(dataDir string) CheckResult {
	identityDir := filepath.Join(dataDir, "identity")
	info, err := identity.Load(identityDir)
	if err != nil {
		return CheckResult{
			Name:    "identity",
			OK:      false,
			Message: "Node identity not found",
			Fix:     "Run: pp init",
		}
	}
	if info.NodeID == "" {
		return CheckResult{
			Name:    "identity",
			OK:      false,
			Message: "Node identity has empty node ID",
			Fix:     "Remove ~/.procyon-park/identity/ and run: pp init",
		}
	}
	return CheckResult{
		Name:    "identity",
		OK:      true,
		Message: fmt.Sprintf("Node identity: %s", info.NodeID),
	}
}

// checkBBS verifies BBS tuplespace database exists.
func checkBBS(dataDir string) CheckResult {
	bbsPath := filepath.Join(dataDir, "bbs.db")
	if _, err := os.Stat(bbsPath); os.IsNotExist(err) {
		return CheckResult{
			Name:    "bbs",
			OK:      false,
			Message: "BBS tuplespace not found",
			Fix:     "Run: pp init",
		}
	}
	return CheckResult{
		Name:    "bbs",
		OK:      true,
		Message: "BBS tuplespace accessible",
	}
}

// checkGit verifies git is available.
func checkGit(ctx context.Context) CheckResult {
	cmd := exec.CommandContext(ctx, "git", "--version")
	out, err := cmd.Output()
	if err != nil {
		return CheckResult{
			Name:    "git",
			OK:      false,
			Message: "git not found in PATH",
			Fix:     "Install git: https://git-scm.com/downloads",
		}
	}
	version := strings.TrimSpace(string(out))
	return CheckResult{
		Name:    "git",
		OK:      true,
		Message: version,
	}
}

// checkTmux verifies tmux is available.
func checkTmux(ctx context.Context) CheckResult {
	cmd := exec.CommandContext(ctx, "tmux", "-V")
	out, err := cmd.Output()
	if err != nil {
		return CheckResult{
			Name:    "tmux",
			OK:      false,
			Message: "tmux not found in PATH",
			Fix:     "Install tmux: brew install tmux (macOS) or apt install tmux (Linux)",
		}
	}
	version := strings.TrimSpace(string(out))
	return CheckResult{
		Name:    "tmux",
		OK:      true,
		Message: version,
	}
}

// checkRepos verifies registered repos still exist and are git repos.
func checkRepos(ctx context.Context, dataDir string) CheckResult {
	regPath := filepath.Join(dataDir, "repos.json")
	if _, err := os.Stat(regPath); os.IsNotExist(err) {
		return CheckResult{
			Name:    "repos",
			OK:      true,
			Message: "No repositories registered",
		}
	}

	reg, err := registry.New(regPath)
	if err != nil {
		return CheckResult{
			Name:    "repos",
			OK:      false,
			Message: fmt.Sprintf("Cannot open registry: %s", err),
		}
	}

	repos, err := reg.List()
	if err != nil {
		return CheckResult{
			Name:    "repos",
			OK:      false,
			Message: fmt.Sprintf("Cannot read registry: %s", err),
		}
	}

	if len(repos) == 0 {
		return CheckResult{
			Name:    "repos",
			OK:      true,
			Message: "No repositories registered",
		}
	}

	var problems []string
	for _, repo := range repos {
		info, err := os.Stat(repo.Path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: path does not exist", repo.Name))
			continue
		}
		if !info.IsDir() {
			problems = append(problems, fmt.Sprintf("%s: path is not a directory", repo.Name))
			continue
		}
		gitDir := filepath.Join(repo.Path, ".git")
		if _, err := os.Stat(gitDir); err != nil {
			problems = append(problems, fmt.Sprintf("%s: not a git repository", repo.Name))
		}
	}

	if len(problems) > 0 {
		return CheckResult{
			Name:    "repos",
			OK:      false,
			Message: fmt.Sprintf("Repository issues: %s", strings.Join(problems, "; ")),
			Fix:     "Run: pp repo list  — then fix or unregister broken repos",
		}
	}

	return CheckResult{
		Name:    "repos",
		OK:      true,
		Message: fmt.Sprintf("%d registered repo(s) OK", len(repos)),
	}
}

// checkDaemonSocket checks if the daemon socket is reachable.
func checkDaemonSocket(dataDir string) CheckResult {
	sockPath := filepath.Join(dataDir, "daemon.sock")
	conn, err := net.DialTimeout("unix", sockPath, 500*time.Millisecond)
	if err != nil {
		return CheckResult{
			Name:    "daemon",
			OK:      false,
			Message: "Daemon not reachable",
			Fix:     "Start with: pp daemon run (or any pp command auto-starts it)",
		}
	}
	conn.Close()
	return CheckResult{
		Name:    "daemon",
		OK:      true,
		Message: "Daemon socket reachable",
	}
}

// colorGreen wraps s in ANSI green if color is enabled.
func colorGreen(s string) string {
	if NoColor() {
		return s
	}
	return "\033[32m" + s + "\033[0m"
}

// colorRed wraps s in ANSI red if color is enabled.
func colorRed(s string) string {
	if NoColor() {
		return s
	}
	return "\033[31m" + s + "\033[0m"
}

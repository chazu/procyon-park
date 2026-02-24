// Package tmux provides Go functions for managing tmux sessions.
// It wraps tmux CLI commands for session lifecycle management,
// key sending, and pane capture. Session names follow the convention
// pp-{repoName}-{agentName}.
package tmux

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// SessionName returns the conventional session name for a repo and agent.
// The format is pp-{repoName}-{agentName}.
func SessionName(repoName, agentName string) string {
	return fmt.Sprintf("pp-%s-%s", repoName, agentName)
}

// CreateSession creates a new detached tmux session with the given name,
// working directory, and environment variables. Environment variables are
// injected using tmux's -e flag (requires tmux 3.2+).
func CreateSession(name, workdir string, env map[string]string) error {
	args := []string{"new-session", "-d", "-s", name}
	if workdir != "" {
		args = append(args, "-c", workdir)
	}
	for k, v := range env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}
	return runTmux(args...)
}

// KillSession destroys the tmux session with the given name.
func KillSession(name string) error {
	return runTmux("kill-session", "-t", name)
}

// SessionExists returns true if a tmux session with the given name exists.
func SessionExists(name string) bool {
	err := runTmux("has-session", "-t", name)
	return err == nil
}

// SendKeys sends keys to the given tmux session, followed by Enter.
// It waits briefly after sending to allow the command to begin executing.
func SendKeys(session, keys string) error {
	if err := runTmux("send-keys", "-t", session, keys, "Enter"); err != nil {
		return err
	}
	time.Sleep(100 * time.Millisecond)
	return nil
}

// SendKeysLiteral sends keys to the given tmux session using the -l flag
// (literal mode), without appending Enter.
func SendKeysLiteral(session, keys string) error {
	return runTmux("send-keys", "-t", session, "-l", keys)
}

// CapturePane captures the last n lines of scrollback from the given
// tmux session's active pane. If lines is 0, it captures the visible pane.
func CapturePane(session string, lines int) (string, error) {
	args := []string{"capture-pane", "-t", session, "-p"}
	if lines > 0 {
		args = append(args, "-S", fmt.Sprintf("-%d", lines))
	}
	cmd := exec.Command("tmux", args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("tmux capture-pane: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("tmux capture-pane: %w", err)
	}
	return string(out), nil
}

// ListSessions returns the names of all active tmux sessions.
func ListSessions() ([]string, error) {
	cmd := exec.Command("tmux", "list-sessions", "-F", "#{session_name}")
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := strings.TrimSpace(string(exitErr.Stderr))
			// "no server running" or "no sessions" is not an error — just empty.
			if strings.Contains(stderr, "no server running") ||
				strings.Contains(stderr, "no sessions") {
				return nil, nil
			}
			return nil, fmt.Errorf("tmux list-sessions: %s", stderr)
		}
		return nil, fmt.Errorf("tmux list-sessions: %w", err)
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}
	return strings.Split(raw, "\n"), nil
}

// runTmux executes a tmux command with the given arguments.
func runTmux(args ...string) error {
	cmd := exec.Command("tmux", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux %s: %s", args[0], strings.TrimSpace(string(out)))
	}
	return nil
}

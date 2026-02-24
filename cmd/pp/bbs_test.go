package main

import (
	"encoding/json"
	"testing"

	"github.com/chazu/procyon-park/internal/cli"
)

// ---------------------------------------------------------------------------
// wildcardToPtr Tests
// ---------------------------------------------------------------------------

func TestWildcardToPtr_QuestionMark(t *testing.T) {
	if wildcardToPtr("?") != nil {
		t.Fatal("expected nil for '?'")
	}
}

func TestWildcardToPtr_NonWildcard(t *testing.T) {
	p := wildcardToPtr("fact")
	if p == nil || *p != "fact" {
		t.Fatalf("expected pointer to 'fact', got %v", p)
	}
}

func TestWildcardToPtr_Empty(t *testing.T) {
	p := wildcardToPtr("")
	if p == nil || *p != "" {
		t.Fatalf("expected pointer to empty string, got %v", p)
	}
}

// ---------------------------------------------------------------------------
// buildPatternParams Tests
// ---------------------------------------------------------------------------

func TestBuildPatternParams_NoArgs(t *testing.T) {
	params := buildPatternParams([]string{})
	if len(params) != 0 {
		t.Fatalf("expected empty params, got %d keys", len(params))
	}
}

func TestBuildPatternParams_CategoryOnly(t *testing.T) {
	params := buildPatternParams([]string{"fact"})
	if params["category"] == nil || *params["category"] != "fact" {
		t.Fatal("expected category=fact")
	}
	if _, ok := params["scope"]; ok {
		t.Fatal("scope should not be set")
	}
}

func TestBuildPatternParams_CategoryAndScope(t *testing.T) {
	params := buildPatternParams([]string{"claim", "myrepo"})
	if *params["category"] != "claim" {
		t.Fatalf("expected category=claim, got %v", *params["category"])
	}
	if *params["scope"] != "myrepo" {
		t.Fatalf("expected scope=myrepo, got %v", *params["scope"])
	}
}

func TestBuildPatternParams_AllThree(t *testing.T) {
	params := buildPatternParams([]string{"available", "repo", "task-1"})
	if *params["category"] != "available" {
		t.Fatalf("expected category=available")
	}
	if *params["scope"] != "repo" {
		t.Fatalf("expected scope=repo")
	}
	if *params["identity"] != "task-1" {
		t.Fatalf("expected identity=task-1")
	}
}

func TestBuildPatternParams_Wildcards(t *testing.T) {
	params := buildPatternParams([]string{"?", "myrepo", "?"})
	if params["category"] != nil {
		t.Fatal("expected nil category for '?'")
	}
	if *params["scope"] != "myrepo" {
		t.Fatal("expected scope=myrepo")
	}
	if params["identity"] != nil {
		t.Fatal("expected nil identity for '?'")
	}
}

// ---------------------------------------------------------------------------
// BBS command structure tests
// ---------------------------------------------------------------------------

func TestBBSCmdRegistered(t *testing.T) {
	if bbsCmd == nil {
		t.Fatal("bbsCmd is nil")
	}
	if bbsCmd.Use != "bbs" {
		t.Fatalf("expected Use='bbs', got %q", bbsCmd.Use)
	}
}

func TestBBSSubcommands(t *testing.T) {
	expected := map[string]bool{
		"out":            false,
		"in":             false,
		"rd":             false,
		"scan":           false,
		"pulse":          false,
		"seed-available": false,
	}

	for _, sub := range bbsCmd.Commands() {
		if _, ok := expected[sub.Name()]; ok {
			expected[sub.Name()] = true
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("subcommand %q not registered on bbsCmd", name)
		}
	}
}

func TestBBSOutArgsValidation(t *testing.T) {
	// out requires 3-4 args.
	if err := bbsOutCmd.Args(bbsOutCmd, []string{"a", "b"}); err == nil {
		t.Fatal("expected error for 2 args")
	}
	if err := bbsOutCmd.Args(bbsOutCmd, []string{"a", "b", "c"}); err != nil {
		t.Fatalf("unexpected error for 3 args: %v", err)
	}
	if err := bbsOutCmd.Args(bbsOutCmd, []string{"a", "b", "c", "d"}); err != nil {
		t.Fatalf("unexpected error for 4 args: %v", err)
	}
	if err := bbsOutCmd.Args(bbsOutCmd, []string{"a", "b", "c", "d", "e"}); err == nil {
		t.Fatal("expected error for 5 args")
	}
}

func TestBBSInArgsValidation(t *testing.T) {
	// in requires 1-3 args.
	if err := bbsInCmd.Args(bbsInCmd, []string{}); err == nil {
		t.Fatal("expected error for 0 args")
	}
	if err := bbsInCmd.Args(bbsInCmd, []string{"a"}); err != nil {
		t.Fatalf("unexpected error for 1 arg: %v", err)
	}
	if err := bbsInCmd.Args(bbsInCmd, []string{"a", "b", "c"}); err != nil {
		t.Fatalf("unexpected error for 3 args: %v", err)
	}
	if err := bbsInCmd.Args(bbsInCmd, []string{"a", "b", "c", "d"}); err == nil {
		t.Fatal("expected error for 4 args")
	}
}

func TestBBSScanArgsValidation(t *testing.T) {
	// scan takes 0-3 args.
	if err := bbsScanCmd.Args(bbsScanCmd, []string{}); err != nil {
		t.Fatalf("unexpected error for 0 args: %v", err)
	}
	if err := bbsScanCmd.Args(bbsScanCmd, []string{"a", "b", "c"}); err != nil {
		t.Fatalf("unexpected error for 3 args: %v", err)
	}
	if err := bbsScanCmd.Args(bbsScanCmd, []string{"a", "b", "c", "d"}); err == nil {
		t.Fatal("expected error for 4 args")
	}
}

func TestBBSSeedAvailableArgsValidation(t *testing.T) {
	// seed-available requires at least 1 arg (scope).
	if err := bbsSeedAvailableCmd.Args(bbsSeedAvailableCmd, []string{}); err == nil {
		t.Fatal("expected error for 0 args")
	}
	if err := bbsSeedAvailableCmd.Args(bbsSeedAvailableCmd, []string{"scope"}); err != nil {
		t.Fatalf("unexpected error for 1 arg: %v", err)
	}
	if err := bbsSeedAvailableCmd.Args(bbsSeedAvailableCmd, []string{"scope", "task-1", "task-2"}); err != nil {
		t.Fatalf("unexpected error for 3 args: %v", err)
	}
}

// ---------------------------------------------------------------------------
// printTupleResult Tests (output behavior)
// ---------------------------------------------------------------------------

func TestBuildPatternParams_MarshalJSON(t *testing.T) {
	// Verify that buildPatternParams produces JSON-serializable maps.
	params := buildPatternParams([]string{"fact", "?", "health"})
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded map[string]*string
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if decoded["category"] == nil || *decoded["category"] != "fact" {
		t.Fatal("expected category=fact in JSON")
	}
	if decoded["scope"] != nil {
		t.Fatal("expected scope=null in JSON for wildcard")
	}
	if decoded["identity"] == nil || *decoded["identity"] != "health" {
		t.Fatal("expected identity=health in JSON")
	}
}

// ---------------------------------------------------------------------------
// NoDaemon tests (commands should fail gracefully without daemon)
// ---------------------------------------------------------------------------

func TestBBSOut_NoDaemon(t *testing.T) {
	code := runBBSWithArgs(t, "out", "fact", "repo", "test")
	if code == 0 {
		t.Fatal("expected non-zero exit when daemon is not running")
	}
}

func TestBBSIn_NoDaemon(t *testing.T) {
	code := runBBSWithArgs(t, "in", "fact", "--timeout", "100ms")
	if code == 0 {
		t.Fatal("expected non-zero exit when daemon is not running")
	}
}

func TestBBSRd_NoDaemon(t *testing.T) {
	code := runBBSWithArgs(t, "rd", "fact")
	if code == 0 {
		t.Fatal("expected non-zero exit when daemon is not running")
	}
}

func TestBBSScan_NoDaemon(t *testing.T) {
	code := runBBSWithArgs(t, "scan", "fact")
	if code == 0 {
		t.Fatal("expected non-zero exit when daemon is not running")
	}
}

func TestBBSPulse_NoDaemon(t *testing.T) {
	code := runBBSWithArgs(t, "pulse", "--agent-id", "Widget")
	if code == 0 {
		t.Fatal("expected non-zero exit when daemon is not running")
	}
}

func TestBBSPulse_MissingAgentID(t *testing.T) {
	code := runBBSWithArgs(t, "pulse")
	if code == 0 {
		t.Fatal("expected non-zero exit when --agent-id is missing")
	}
}

func TestBBSSeedAvailable_NoDaemon(t *testing.T) {
	code := runBBSWithArgs(t, "seed-available", "myrepo", "task-1")
	if code == 0 {
		t.Fatal("expected non-zero exit when daemon is not running")
	}
}

// ---------------------------------------------------------------------------
// BBS help / usage tests
// ---------------------------------------------------------------------------

func TestBBSUsage(t *testing.T) {
	code := runBBSWithArgs(t)
	if code != 0 {
		t.Fatalf("expected exit 0 for 'pp bbs' (help), got %d", code)
	}
}

// runBBSWithArgs runs 'pp bbs <args...>' via Cobra using a non-existent socket
// so that daemon connections fail predictably. Returns the exit code.
func runBBSWithArgs(t *testing.T, args ...string) int {
	t.Helper()
	fullArgs := append([]string{"bbs", "--socket", "/tmp/nonexistent-pp-test.sock"}, args...)
	return runPP(t, fullArgs...)
}

// runPP runs the CLI with the given args and returns the exit code.
// Uses a non-existent socket to prevent accidental daemon interaction.
func runPP(t *testing.T, args ...string) int {
	t.Helper()

	// Save and restore global flag state.
	origAgentID := bbsAgentID
	origInstance := bbsInstance
	origTimeout := bbsTimeout
	defer func() {
		bbsAgentID = origAgentID
		bbsInstance = origInstance
		bbsTimeout = origTimeout
	}()

	return cli.ExecuteArgs(args)
}

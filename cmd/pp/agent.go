// agent.go implements the 'pp agent' subcommands: spawn, dismiss, status, list, prune.
// All commands communicate with the daemon via JSON-RPC over Unix socket.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// defaultSocketPath returns the daemon Unix socket path.
func defaultSocketPath(dataDir string) string {
	return filepath.Join(dataDir, "daemon.sock")
}

// rpcCall sends a JSON-RPC 2.0 request to the daemon and returns the result.
func rpcCall(socketPath, method string, params interface{}) (json.RawMessage, error) {
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to daemon at %s: %w", socketPath, err)
	}
	defer conn.Close()

	// Set read/write deadline.
	conn.SetDeadline(time.Now().Add(60 * time.Second))

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}

	req := struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
		ID      int             `json:"id"`
	}{
		JSONRPC: "2.0",
		Method:  method,
		Params:  paramsJSON,
		ID:      1,
	}

	reqData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	reqData = append(reqData, '\n')

	if _, err := conn.Write(reqData); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		return nil, fmt.Errorf("no response from daemon")
	}

	var resp struct {
		JSONRPC string          `json:"jsonrpc"`
		Result  json.RawMessage `json:"result,omitempty"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
		ID json.RawMessage `json:"id"`
	}

	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("daemon error (%d): %s", resp.Error.Code, resp.Error.Message)
	}

	return resp.Result, nil
}

// handleAgent dispatches the agent subcommand. Returns the process exit code.
func handleAgent(args []string) int {
	subcmd, agentArgs, dataDir, err := parseAgentArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	socketPath := defaultSocketPath(dataDir)

	switch subcmd {
	case "spawn":
		return runAgentSpawn(socketPath, agentArgs)
	case "dismiss":
		return runAgentDismiss(socketPath, agentArgs)
	case "status":
		return runAgentStatus(socketPath, agentArgs)
	case "list":
		return runAgentList(socketPath, agentArgs)
	case "prune":
		return runAgentPrune(socketPath, agentArgs)
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown agent subcommand %q\n", subcmd)
		return 1
	}
}

// parseAgentArgs extracts the subcommand, remaining args, and data dir.
func parseAgentArgs(args []string) (subcmd string, remaining []string, dataDir string, err error) {
	dataDir = defaultDataDir()

	if len(args) == 0 {
		return "", nil, "", fmt.Errorf("missing agent subcommand\n\n%s", agentUsage())
	}

	subcmd = args[0]
	switch subcmd {
	case "spawn", "dismiss", "status", "list", "prune":
	case "--help", "-help", "help":
		fmt.Print(agentUsage())
		return "", nil, "", fmt.Errorf("") // sentinel: caller should exit 0
	default:
		return "", nil, "", fmt.Errorf("unknown agent subcommand %q\n\n%s", subcmd, agentUsage())
	}

	// Extract --data-dir from remaining args, pass the rest through.
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--data-dir" {
			if i+1 >= len(rest) {
				return "", nil, "", fmt.Errorf("--data-dir requires a value")
			}
			dataDir = rest[i+1]
			// Remove --data-dir and its value from remaining.
			rest = append(rest[:i], rest[i+2:]...)
			i--
		}
	}

	return subcmd, rest, dataDir, nil
}

// agentUsage returns the help text for agent subcommands.
func agentUsage() string {
	return `Usage: pp agent <command> [options]

Commands:
  spawn     Spawn a new agent in a repo
  dismiss   Dismiss (tear down) an agent
  status    Check agent status with liveness detection
  list      List agents for a repo
  prune     Clean up dead agents and orphaned resources

Global options:
  --data-dir PATH   Data directory (default: ~/.procyon-park)

Spawn options:
  --role ROLE           Agent role: cub or king (required)
  --task-id ID          Beads issue ID (required)
  --base-branch BRANCH  Git branch to base on (required)
  --repo-name NAME      Repository name (required)
  --repo-root PATH      Absolute path to repo (required)
  --epic-id ID          Epic ID for feature branch context
  --agent-cmd CMD       Command to launch in agent session
  --prime-text TEXT      Priming instruction to send
  --worktree-base PATH  Worktree base directory
  --prompt-wait-ms MS   Wait time after launch (default: 2000)

Dismiss options:
  --agent-name NAME     Agent name (required)
  --repo-name NAME      Repository name (required)
  --repo-root PATH      Absolute path to repo (required)

Status options:
  --agent-name NAME     Agent name (required)
  --repo-name NAME      Repository name (required)

List options:
  --repo-name NAME      Repository name (required)

Prune options:
  --repo-name NAME      Repository name (required)
  --repo-root PATH      Absolute path to repo (required)
  --worktree-base PATH  Worktree base directory (required)
  --branch-age DURATION Minimum branch age to delete (default: 24h)
`
}

// parseFlags is a simple flag parser that extracts --key value pairs into a map.
func parseFlags(args []string) (map[string]string, error) {
	flags := make(map[string]string)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			return nil, fmt.Errorf("unexpected argument %q", arg)
		}
		key := strings.TrimPrefix(arg, "--")
		if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
			// Boolean-style flag (not used here but defensive).
			flags[key] = ""
			continue
		}
		flags[key] = args[i+1]
		i++
	}
	return flags, nil
}

// requireFlag returns the value of a required flag or an error.
func requireFlag(flags map[string]string, name string) (string, error) {
	v, ok := flags[name]
	if !ok || v == "" {
		return "", fmt.Errorf("--%s is required", name)
	}
	return v, nil
}

// runAgentSpawn handles 'pp agent spawn'.
func runAgentSpawn(socketPath string, args []string) int {
	flags, err := parseFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	role, err := requireFlag(flags, "role")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	taskID, err := requireFlag(flags, "task-id")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	baseBranch, err := requireFlag(flags, "base-branch")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	repoName, err := requireFlag(flags, "repo-name")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	repoRoot, err := requireFlag(flags, "repo-root")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	params := map[string]interface{}{
		"role":        role,
		"task_id":     taskID,
		"base_branch": baseBranch,
		"repo_name":   repoName,
		"repo_root":   repoRoot,
	}

	if v, ok := flags["worktree-base"]; ok && v != "" {
		params["worktree_base"] = v
	}
	if v, ok := flags["epic-id"]; ok && v != "" {
		params["epic_id"] = v
	}
	if v, ok := flags["agent-cmd"]; ok && v != "" {
		params["agent_cmd"] = v
	}
	if v, ok := flags["prime-text"]; ok && v != "" {
		params["prime_text"] = v
	}
	if v, ok := flags["prompt-wait-ms"]; ok && v != "" {
		ms, err := strconv.Atoi(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: --prompt-wait-ms must be an integer\n")
			return 1
		}
		params["prompt_wait_ms"] = ms
	}

	result, err := rpcCall(socketPath, "agent.spawn", params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	var spawnResult struct {
		AgentName   string `json:"AgentName"`
		Branch      string `json:"Branch"`
		Worktree    string `json:"Worktree"`
		TmuxSession string `json:"TmuxSession"`
	}
	if err := json.Unmarshal(result, &spawnResult); err != nil {
		// Fall back to raw output.
		fmt.Println(string(result))
		return 0
	}

	fmt.Printf("Agent spawned:\n")
	fmt.Printf("  Name:     %s\n", spawnResult.AgentName)
	fmt.Printf("  Branch:   %s\n", spawnResult.Branch)
	fmt.Printf("  Worktree: %s\n", spawnResult.Worktree)
	fmt.Printf("  Session:  %s\n", spawnResult.TmuxSession)
	return 0
}

// runAgentDismiss handles 'pp agent dismiss'.
func runAgentDismiss(socketPath string, args []string) int {
	flags, err := parseFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	agentName, err := requireFlag(flags, "agent-name")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	repoName, err := requireFlag(flags, "repo-name")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	repoRoot, err := requireFlag(flags, "repo-root")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	params := map[string]string{
		"agent_name": agentName,
		"repo_name":  repoName,
		"repo_root":  repoRoot,
	}

	_, err = rpcCall(socketPath, "agent.dismiss", params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	fmt.Printf("Agent %q dismissed\n", agentName)
	return 0
}

// runAgentStatus handles 'pp agent status'.
func runAgentStatus(socketPath string, args []string) int {
	flags, err := parseFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	agentName, err := requireFlag(flags, "agent-name")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	repoName, err := requireFlag(flags, "repo-name")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	params := map[string]string{
		"agent_name": agentName,
		"repo_name":  repoName,
	}

	result, err := rpcCall(socketPath, "agent.status", params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	var status struct {
		StoredStatus  string `json:"stored_status"`
		ActualStatus  string `json:"actual_status"`
		SessionExists bool   `json:"session_exists"`
	}
	if err := json.Unmarshal(result, &status); err != nil {
		fmt.Println(string(result))
		return 0
	}

	fmt.Printf("Agent: %s\n", agentName)
	fmt.Printf("  Stored status:  %s\n", status.StoredStatus)
	fmt.Printf("  Actual status:  %s\n", status.ActualStatus)
	fmt.Printf("  Session exists: %v\n", status.SessionExists)
	return 0
}

// runAgentList handles 'pp agent list'.
func runAgentList(socketPath string, args []string) int {
	flags, err := parseFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	repoName, err := requireFlag(flags, "repo-name")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	params := map[string]string{
		"repo_name": repoName,
	}

	result, err := rpcCall(socketPath, "agent.list", params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	var agents []struct {
		Name    string `json:"name"`
		Payload string `json:"payload"`
	}
	if err := json.Unmarshal(result, &agents); err != nil {
		fmt.Println(string(result))
		return 0
	}

	if len(agents) == 0 {
		fmt.Println("No agents registered")
		return 0
	}

	for _, a := range agents {
		// Try to parse payload for a nice display.
		var info struct {
			Role   string `json:"role"`
			Status string `json:"status"`
			Task   string `json:"task"`
			Branch string `json:"branch"`
		}
		if err := json.Unmarshal([]byte(a.Payload), &info); err == nil {
			fmt.Printf("%-12s  role=%-4s  status=%-6s  task=%s  branch=%s\n",
				a.Name, info.Role, info.Status, info.Task, info.Branch)
		} else {
			fmt.Printf("%-12s  %s\n", a.Name, a.Payload)
		}
	}
	return 0
}

// runAgentPrune handles 'pp agent prune'.
func runAgentPrune(socketPath string, args []string) int {
	flags, err := parseFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	repoName, err := requireFlag(flags, "repo-name")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	repoRoot, err := requireFlag(flags, "repo-root")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	worktreeBase, err := requireFlag(flags, "worktree-base")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	params := map[string]interface{}{
		"repo_name":     repoName,
		"repo_root":     repoRoot,
		"worktree_base": worktreeBase,
	}

	if v, ok := flags["branch-age"]; ok && v != "" {
		dur, err := time.ParseDuration(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: --branch-age must be a valid duration (e.g. 24h)\n")
			return 1
		}
		params["branch_age_ms"] = int(dur.Milliseconds())
	}

	result, err := rpcCall(socketPath, "agent.prune", params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	var pruneResult struct {
		DeadAgents        []string `json:"DeadAgents"`
		OrphanedWorktrees []string `json:"OrphanedWorktrees"`
		PreservedBranches []string `json:"PreservedBranches"`
		DeletedBranches   []string `json:"DeletedBranches"`
		Errors            []string `json:"Errors"`
	}
	if err := json.Unmarshal(result, &pruneResult); err != nil {
		fmt.Println(string(result))
		return 0
	}

	if len(pruneResult.DeadAgents) > 0 {
		fmt.Printf("Dead agents cleaned up: %s\n", strings.Join(pruneResult.DeadAgents, ", "))
	}
	if len(pruneResult.OrphanedWorktrees) > 0 {
		fmt.Printf("Orphaned worktrees removed: %d\n", len(pruneResult.OrphanedWorktrees))
	}
	if len(pruneResult.DeletedBranches) > 0 {
		fmt.Printf("Deleted branches: %s\n", strings.Join(pruneResult.DeletedBranches, ", "))
	}
	if len(pruneResult.PreservedBranches) > 0 {
		fmt.Printf("Preserved branches (too recent): %s\n", strings.Join(pruneResult.PreservedBranches, ", "))
	}
	if len(pruneResult.Errors) > 0 {
		fmt.Printf("Non-fatal errors: %s\n", strings.Join(pruneResult.Errors, "; "))
	}
	if len(pruneResult.DeadAgents) == 0 && len(pruneResult.OrphanedWorktrees) == 0 && len(pruneResult.DeletedBranches) == 0 {
		fmt.Println("Nothing to prune")
	}
	return 0
}

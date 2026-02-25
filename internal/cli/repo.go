// repo.go implements the 'pp repo' command group for managing tracked repositories.
// Commands: register, unregister, list, status.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chazu/procyon-park/internal/ipc"
	"github.com/chazu/procyon-park/internal/output"
	"github.com/spf13/cobra"
)

func init() {
	repoCmd := &cobra.Command{
		Use:   "repo",
		Short: "Manage tracked repositories",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	repoCmd.AddCommand(repoAddCmd())
	repoCmd.AddCommand(repoRegisterCmd())
	repoCmd.AddCommand(repoUnregisterCmd())
	repoCmd.AddCommand(repoListCmd())
	repoCmd.AddCommand(repoStatusCmd())

	AddCommand(repoCmd)
}

// repoAddCmd returns the 'pp repo add' command. It registers a repository,
// creates <repo>/.procyon-park/ if needed, and prints the result.
func repoAddCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "add <path>",
		Short: "Add a repository for tracking",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := EnsureDaemon(); err != nil {
				return NewExitErr(ExitConnection, err)
			}

			repoPath, err := filepath.Abs(args[0])
			if err != nil {
				return NewExitErr(ExitError, fmt.Errorf("resolve path: %w", err))
			}

			// Validate that the path is a git repository.
			gitDir := filepath.Join(repoPath, ".git")
			if info, err := os.Stat(gitDir); err != nil || !info.IsDir() {
				return NewExitErr(ExitError, fmt.Errorf("%s is not a git repository", repoPath))
			}

			if name == "" {
				name = filepath.Base(repoPath)
			}

			params := map[string]string{
				"name": name,
				"path": repoPath,
			}

			result, err := ipc.Call(SocketPath(), "repo.register", params)
			if err != nil {
				return NewExitErr(ExitError, fmt.Errorf("repo.register: %w", err))
			}

			// Create <repo>/.procyon-park/ for per-repo config and state.
			repoPPDir := filepath.Join(repoPath, ".procyon-park")
			if err := os.MkdirAll(repoPPDir, 0755); err != nil {
				// Non-fatal: warn but don't fail.
				fmt.Fprintf(os.Stderr, "warning: could not create %s: %v\n", repoPPDir, err)
			}

			f, fErr := output.ResolveFormat(flagOutput, os.Stdout)
			if fErr != nil {
				return NewExitErr(ExitUsage, fErr)
			}

			if f == output.FormatJSON || f == output.FormatJSONPretty {
				fmt.Fprintln(os.Stdout, string(result))
			} else if !flagQuiet {
				fmt.Fprintf(os.Stdout, "added repository %q at %s\n", name, repoPath)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "name for the repository (default: directory name)")
	return cmd
}

// repoRegisterCmd returns the 'pp repo register' command.
func repoRegisterCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "register <path>",
		Short: "Register a repository for tracking",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := EnsureDaemon(); err != nil {
				return NewExitErr(ExitConnection, err)
			}

			repoPath, err := filepath.Abs(args[0])
			if err != nil {
				return NewExitErr(ExitError, fmt.Errorf("resolve path: %w", err))
			}

			if name == "" {
				name = filepath.Base(repoPath)
			}

			params := map[string]string{
				"name": name,
				"path": repoPath,
			}

			result, err := ipc.Call(SocketPath(), "repo.register", params)
			if err != nil {
				return NewExitErr(ExitError, fmt.Errorf("repo.register: %w", err))
			}

			f, fErr := output.ResolveFormat(flagOutput, os.Stdout)
			if fErr != nil {
				return NewExitErr(ExitUsage, fErr)
			}

			if f == output.FormatJSON || f == output.FormatJSONPretty {
				fmt.Fprintln(os.Stdout, string(result))
			} else if !flagQuiet {
				fmt.Fprintf(os.Stdout, "registered repository %q at %s\n", name, repoPath)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "name for the repository (default: directory name)")
	return cmd
}

// repoUnregisterCmd returns the 'pp repo unregister' command.
func repoUnregisterCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unregister <name>",
		Short: "Unregister a tracked repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := EnsureDaemon(); err != nil {
				return NewExitErr(ExitConnection, err)
			}

			params := map[string]string{"name": args[0]}
			result, err := ipc.Call(SocketPath(), "repo.unregister", params)
			if err != nil {
				return NewExitErr(ExitError, fmt.Errorf("repo.unregister: %w", err))
			}

			f, fErr := output.ResolveFormat(flagOutput, os.Stdout)
			if fErr != nil {
				return NewExitErr(ExitUsage, fErr)
			}

			if f == output.FormatJSON || f == output.FormatJSONPretty {
				fmt.Fprintln(os.Stdout, string(result))
			} else if !flagQuiet {
				fmt.Fprintf(os.Stdout, "unregistered repository %q\n", args[0])
			}
			return nil
		},
	}
}

// repoListCmd returns the 'pp repo list' command.
func repoListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List tracked repositories",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := EnsureDaemon(); err != nil {
				return NewExitErr(ExitConnection, err)
			}

			result, err := ipc.Call(SocketPath(), "repo.list", nil)
			if err != nil {
				return NewExitErr(ExitError, fmt.Errorf("repo.list: %w", err))
			}

			f, fErr := output.ResolveFormat(flagOutput, os.Stdout)
			if fErr != nil {
				return NewExitErr(ExitUsage, fErr)
			}

			if f == output.FormatJSON || f == output.FormatJSONPretty {
				fmt.Fprintln(os.Stdout, string(result))
				return nil
			}

			var repos []struct {
				Name string `json:"name"`
				Path string `json:"path"`
			}
			if err := json.Unmarshal(result, &repos); err != nil {
				return NewExitErr(ExitError, fmt.Errorf("parse response: %w", err))
			}

			if len(repos) == 0 {
				if !flagQuiet {
					fmt.Fprintln(os.Stdout, "no repositories registered")
				}
				return nil
			}

			records := make([]*output.Record, 0, len(repos))
			for _, r := range repos {
				rec := output.NewRecord()
				rec.Set("name", r.Name)
				rec.Set("path", r.Path)
				records = append(records, rec)
			}

			formatter := output.NewFormatter(f)
			return formatter.Format(os.Stdout, records)
		},
	}
}

// repoStatusCmd returns the 'pp repo status' command.
func repoStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [name]",
		Short: "Show status of a tracked repository",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := EnsureDaemon(); err != nil {
				return NewExitErr(ExitConnection, err)
			}

			var params interface{}
			if len(args) > 0 {
				params = map[string]string{"name": args[0]}
			}

			result, err := ipc.Call(SocketPath(), "repo.status", params)
			if err != nil {
				return NewExitErr(ExitError, fmt.Errorf("repo.status: %w", err))
			}

			f, fErr := output.ResolveFormat(flagOutput, os.Stdout)
			if fErr != nil {
				return NewExitErr(ExitUsage, fErr)
			}

			if f == output.FormatJSON || f == output.FormatJSONPretty {
				fmt.Fprintln(os.Stdout, string(result))
				return nil
			}

			var statuses []struct {
				Name   string `json:"name"`
				Path   string `json:"path"`
				Status string `json:"status"`
			}
			if err := json.Unmarshal(result, &statuses); err != nil {
				return NewExitErr(ExitError, fmt.Errorf("parse response: %w", err))
			}

			if len(statuses) == 0 {
				if !flagQuiet {
					fmt.Fprintln(os.Stdout, "no repositories found")
				}
				return nil
			}

			records := make([]*output.Record, 0, len(statuses))
			for _, s := range statuses {
				rec := output.NewRecord()
				rec.Set("name", s.Name)
				rec.Set("path", s.Path)
				rec.Set("status", s.Status)
				records = append(records, rec)
			}

			formatter := output.NewFormatter(f)
			return formatter.Format(os.Stdout, records)
		},
	}
}

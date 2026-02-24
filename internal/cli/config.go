// config.go implements the 'pp config' command group for managing configuration.
// Commands: get, set, list, edit.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/chazu/procyon-park/internal/ipc"
	"github.com/chazu/procyon-park/internal/output"
	"github.com/spf13/cobra"
)

func init() {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Manage configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	configCmd.AddCommand(configGetCmd())
	configCmd.AddCommand(configSetCmd())
	configCmd.AddCommand(configListCmd())
	configCmd.AddCommand(configEditCmd())

	AddCommand(configCmd)
}

// configGetCmd returns the 'pp config get' command.
func configGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Get a configuration value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := EnsureDaemon(); err != nil {
				return NewExitErr(ExitConnection, err)
			}

			params := map[string]string{"key": args[0]}
			result, err := ipc.Call(SocketPath(), "config.get", params)
			if err != nil {
				return NewExitErr(ExitError, fmt.Errorf("config.get: %w", err))
			}

			f, fErr := output.ResolveFormat(flagOutput, os.Stdout)
			if fErr != nil {
				return NewExitErr(ExitUsage, fErr)
			}

			if f == output.FormatJSON || f == output.FormatJSONPretty {
				fmt.Fprintln(os.Stdout, string(result))
				return nil
			}

			// For text/table, unwrap the JSON string value.
			var value interface{}
			if err := json.Unmarshal(result, &value); err != nil {
				fmt.Fprintln(os.Stdout, string(result))
				return nil
			}

			switch v := value.(type) {
			case string:
				fmt.Fprintln(os.Stdout, v)
			default:
				fmt.Fprintln(os.Stdout, string(result))
			}
			return nil
		},
	}
}

// configSetCmd returns the 'pp config set' command.
func configSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := EnsureDaemon(); err != nil {
				return NewExitErr(ExitConnection, err)
			}

			params := map[string]string{"key": args[0], "value": args[1]}
			result, err := ipc.Call(SocketPath(), "config.set", params)
			if err != nil {
				return NewExitErr(ExitError, fmt.Errorf("config.set: %w", err))
			}

			f, fErr := output.ResolveFormat(flagOutput, os.Stdout)
			if fErr != nil {
				return NewExitErr(ExitUsage, fErr)
			}

			if f == output.FormatJSON || f == output.FormatJSONPretty {
				fmt.Fprintln(os.Stdout, string(result))
			} else if !flagQuiet {
				fmt.Fprintf(os.Stdout, "%s = %s\n", args[0], args[1])
			}
			return nil
		},
	}
}

// configListCmd returns the 'pp config list' command.
func configListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all configuration values",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := EnsureDaemon(); err != nil {
				return NewExitErr(ExitConnection, err)
			}

			result, err := ipc.Call(SocketPath(), "config.list", nil)
			if err != nil {
				return NewExitErr(ExitError, fmt.Errorf("config.list: %w", err))
			}

			f, fErr := output.ResolveFormat(flagOutput, os.Stdout)
			if fErr != nil {
				return NewExitErr(ExitUsage, fErr)
			}

			if f == output.FormatJSON || f == output.FormatJSONPretty {
				fmt.Fprintln(os.Stdout, string(result))
				return nil
			}

			var entries []struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			}
			if err := json.Unmarshal(result, &entries); err != nil {
				return NewExitErr(ExitError, fmt.Errorf("parse response: %w", err))
			}

			if len(entries) == 0 {
				if !flagQuiet {
					fmt.Fprintln(os.Stdout, "no configuration set")
				}
				return nil
			}

			records := make([]*output.Record, 0, len(entries))
			for _, e := range entries {
				rec := output.NewRecord()
				rec.Set("key", e.Key)
				rec.Set("value", e.Value)
				records = append(records, rec)
			}

			formatter := output.NewFormatter(f)
			return formatter.Format(os.Stdout, records)
		},
	}
}

// configEditCmd returns the 'pp config edit' command.
func configEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open configuration file in $EDITOR",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := EnsureDaemon(); err != nil {
				return NewExitErr(ExitConnection, err)
			}

			// Ask the daemon for the config file path.
			result, err := ipc.Call(SocketPath(), "config.path", nil)
			if err != nil {
				return NewExitErr(ExitError, fmt.Errorf("config.path: %w", err))
			}

			var configPath string
			if err := json.Unmarshal(result, &configPath); err != nil {
				return NewExitErr(ExitError, fmt.Errorf("parse response: %w", err))
			}

			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}

			editorCmd := exec.Command(editor, configPath)
			editorCmd.Stdin = os.Stdin
			editorCmd.Stdout = os.Stdout
			editorCmd.Stderr = os.Stderr

			if err := editorCmd.Run(); err != nil {
				return NewExitErr(ExitError, fmt.Errorf("editor: %w", err))
			}
			return nil
		},
	}
}

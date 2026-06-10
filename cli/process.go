package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	sdkhost "github.com/fluxplane/fluxplane-plugin/host"
	"github.com/fluxplane/fluxplane-plugin/management"
)

func processManagerRequired(backend management.Backend) (management.ProcessManager, error) {
	if err := backendRequired(backend); err != nil {
		return nil, err
	}
	manager, ok := backend.(management.ProcessManager)
	if !ok {
		return nil, fmt.Errorf("fluxplane-plugin: the configured backend does not support process management")
	}
	return manager, nil
}

func newProcessCommand(backend management.Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "process",
		Aliases: []string{"proc", "ps"},
		Short:   "Inspect and stop host-managed background processes started by plugins",
		Long: "Plugins start long-lived background processes (e.g. kubernetes port-forwards) through the " +
			"host process capability; their records persist across sessions. This command group lists " +
			"them with a liveness probe, tails their log files, and stops them.",
	}
	cmd.AddCommand(
		newProcessListCommand(backend),
		newProcessLogsCommand(backend),
		newProcessStopCommand(backend),
	)
	return cmd
}

func newProcessListCommand(backend management.Backend) *cobra.Command {
	var group, plugin, label string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List host-managed background processes with liveness",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			manager, err := processManagerRequired(backend)
			if err != nil {
				return err
			}
			result, err := manager.ListProcesses(cmd.Context(), sdkhost.ProcessListRequest{Group: group, Label: label, Plugin: plugin})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&group, "group", "", "filter by process group (e.g. kubernetes.portforward)")
	cmd.Flags().StringVar(&plugin, "plugin", "", "filter by the plugin that started the process")
	cmd.Flags().StringVar(&label, "label", "", "filter by exact label")
	return cmd
}

func newProcessLogsCommand(backend management.Backend) *cobra.Command {
	var lines int
	var follow bool
	cmd := &cobra.Command{
		Use:   "logs ID",
		Short: "Print (or follow) a background process's log file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := processManagerRequired(backend)
			if err != nil {
				return err
			}
			record, err := findProcess(cmd, manager, args[0])
			if err != nil {
				return err
			}
			if strings.TrimSpace(record.LogPath) == "" {
				return fmt.Errorf("fluxplane-plugin: process %q has no log file recorded", record.ID)
			}
			offset, err := printLogTail(cmd.OutOrStdout(), record.LogPath, lines)
			if err != nil {
				return err
			}
			if !follow {
				return nil
			}
			return followLog(cmd, record.LogPath, offset)
		},
		ValidArgsFunction: processIDCompletion(backend),
	}
	cmd.Flags().IntVarP(&lines, "lines", "n", 100, "number of trailing lines to print")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing as the log grows (Ctrl-C to stop)")
	return cmd
}

func newProcessStopCommand(backend management.Backend) *cobra.Command {
	var signal string
	cmd := &cobra.Command{
		Use:   "stop ID",
		Short: "Stop a background process (signals its whole process group) and remove its record",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := processManagerRequired(backend)
			if err != nil {
				return err
			}
			record, err := findProcess(cmd, manager, args[0])
			if err != nil {
				return err
			}
			result, err := manager.StopProcess(cmd.Context(), sdkhost.ProcessStopRequest{ID: record.ID, Signal: signal})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
		ValidArgsFunction: processIDCompletion(backend),
	}
	cmd.Flags().StringVar(&signal, "signal", "SIGTERM", "signal to send")
	return cmd
}

// findProcess resolves an exact or unambiguous-prefix process ID.
func findProcess(cmd *cobra.Command, manager management.ProcessManager, id string) (sdkhost.ProcessRecord, error) {
	id = strings.TrimSpace(id)
	result, err := manager.ListProcesses(cmd.Context(), sdkhost.ProcessListRequest{})
	if err != nil {
		return sdkhost.ProcessRecord{}, err
	}
	var matches []sdkhost.ProcessRecord
	for _, record := range result.Processes {
		if record.ID == id {
			return record, nil
		}
		if strings.HasPrefix(record.ID, id) {
			matches = append(matches, record)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return sdkhost.ProcessRecord{}, fmt.Errorf("fluxplane-plugin: no background process %q (see `fluxplane-plugin process list`)", id)
	default:
		ids := make([]string, 0, len(matches))
		for _, m := range matches {
			ids = append(ids, m.ID)
		}
		return sdkhost.ProcessRecord{}, fmt.Errorf("fluxplane-plugin: process id %q is ambiguous: %s", id, strings.Join(ids, ", "))
	}
}

// printLogTail writes the last n lines of path and returns the file size, the
// offset follow-mode continues from.
func printLogTail(out io.Writer, path string, n int) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("fluxplane-plugin: read process log: %w", err)
	}
	text := string(data)
	if n > 0 {
		lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
		if len(lines) > n {
			lines = lines[len(lines)-n:]
		}
		text = strings.Join(lines, "\n")
		if text != "" {
			text += "\n"
		}
	}
	_, _ = io.WriteString(out, text)
	return int64(len(data)), nil
}

// followLog polls the log file and prints anything appended past offset until
// the command context is cancelled.
func followLog(cmd *cobra.Command, path string, offset int64) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-cmd.Context().Done():
			return nil
		case <-ticker.C:
		}
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Size() < offset {
			offset = 0 // truncated/rotated: start over
		}
		if info.Size() == offset {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			_ = file.Close()
			return err
		}
		n, err := io.Copy(cmd.OutOrStdout(), file)
		_ = file.Close()
		if err != nil {
			return err
		}
		offset += n
	}
}

// processIDCompletion completes background-process IDs with their labels.
func processIDCompletion(backend management.Backend) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		manager, err := processManagerRequired(backend)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		result, err := manager.ListProcesses(cmd.Context(), sdkhost.ProcessListRequest{})
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var out []cobra.Completion
		for _, record := range result.Processes {
			if !strings.HasPrefix(record.ID, toComplete) {
				continue
			}
			desc := strings.TrimSpace(strings.Join([]string{record.Plugin, record.Label}, " "))
			out = append(out, cobra.CompletionWithDesc(record.ID, desc))
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

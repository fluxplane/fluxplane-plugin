package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fluxplane/fluxplane-plugin/management"
)

func newDevCommand(backend management.Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dev",
		Short: "Developer-loop helpers for working on plugins from a local workspace",
	}
	cmd.AddCommand(newDevSyncCommand(backend))
	return cmd
}

func newDevSyncCommand(backend management.Backend) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "sync [plugin...]",
		Short: "Rebuild installed plugins from their workspace local_path (bypasses the marketplace)",
		Long: "Rebuilds each installed plugin's binary from the local_path recorded at install time, " +
			"writing it back to the installed binary path.\n\n" +
			"Unlike `install`, this never consults the marketplace catalog, so a stale cached " +
			"marketplace.json (whose entries carry no usable local_path) cannot shadow your workspace " +
			"build. With no arguments it rebuilds every installed plugin that has a recorded local_path.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			syncer, ok := backend.(management.LocalSyncer)
			if !ok {
				return fmt.Errorf("fluxplane-plugin: the configured backend does not support dev sync")
			}
			req := management.SyncRequest{All: len(args) == 0, DryRun: dryRun}
			for _, name := range args {
				if name = strings.TrimSpace(name); name != "" {
					req.Refs = append(req.Refs, parseRef(name))
				}
			}
			result, err := syncer.SyncLocalPlugins(cmd.Context(), req)
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "resolve and report what would be rebuilt without building")
	return cmd
}

package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fluxplane/fluxplane-plugin/management"
)

func versionManagerRequired(backend management.Backend) (management.VersionManager, error) {
	if err := backendRequired(backend); err != nil {
		return nil, err
	}
	manager, ok := backend.(management.VersionManager)
	if !ok {
		return nil, fmt.Errorf("fluxplane-plugin: the configured backend does not support version pinning")
	}
	return manager, nil
}

func newPinCommand(backend management.Backend) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "pin PLUGIN[@VERSION]",
		Short: "Hold a plugin at a version so upgrade and install --all skip it",
		Long: "Pins a plugin at a version. With PLUGIN@VERSION the plugin is reinstalled at that exact " +
			"version first; with a bare PLUGIN the currently installed version is pinned.\n\n" +
			"Pinned plugins are skipped by `upgrade` and `install --all` until unpinned. Note that " +
			"`dev sync` intentionally still rebuilds pinned plugins from the workspace (the dev loop " +
			"trumps the pin).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := versionManagerRequired(backend)
			if err != nil {
				return err
			}
			ref := parseRef(args[0])
			// Pinning a different version than the installed one means
			// installing it: reinstall at the requested version first.
			if ref.Version != "" && !dryRun {
				current := installedVersionOf(cmd, backend, ref.Name)
				if current != ref.Version {
					if _, err := backend.InstallPlugin(cmd.Context(), management.InstallRequest{Ref: ref, Force: true, PreferRemote: true}); err != nil {
						return err
					}
				}
			}
			result, err := manager.PinPlugin(cmd.Context(), management.PinRequest{Ref: ref, DryRun: dryRun})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
		ValidArgsFunction: pluginNameCompletion(backend),
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report the pin without changing state")
	return cmd
}

func newUnpinCommand(backend management.Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unpin PLUGIN",
		Short: "Release a plugin's version pin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := versionManagerRequired(backend)
			if err != nil {
				return err
			}
			result, err := manager.PinPlugin(cmd.Context(), management.PinRequest{Ref: parseRef(args[0]), Unpin: true})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
		ValidArgsFunction: pluginNameCompletion(backend),
	}
	return cmd
}

func newRollbackCommand(backend management.Backend) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "rollback PLUGIN",
		Short: "Reinstall the previously installed version of a plugin",
		Long: "Swaps a plugin back to the version recorded before its last install/upgrade. Rolling back " +
			"twice round-trips. Refuses while the plugin is pinned.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := versionManagerRequired(backend)
			if err != nil {
				return err
			}
			result, err := manager.RollbackPlugin(cmd.Context(), management.RollbackRequest{Ref: parseRef(args[0]), DryRun: dryRun})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
		ValidArgsFunction: pluginNameCompletion(backend),
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report the rollback target without reinstalling")
	return cmd
}

// installedVersionOf reports the recorded installed version of a plugin, or ""
// when unknown. Best effort, used to skip a redundant reinstall on pin.
func installedVersionOf(cmd *cobra.Command, backend management.Backend, name string) string {
	plugins, err := backend.ListPlugins(cmd.Context(), management.ListRequest{All: true})
	if err != nil {
		return ""
	}
	for _, plugin := range plugins {
		if strings.EqualFold(plugin.Ref.Name, name) {
			return plugin.InstalledVersion
		}
	}
	return ""
}

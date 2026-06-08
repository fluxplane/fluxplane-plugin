package cli

import (
	"context"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fluxplane/fluxplane-plugin/management"
)

// selfInstallTarget is the published CLI entrypoint upgraded by `upgrade`.
const selfInstallTarget = "github.com/fluxplane/fluxplane-plugin/cmd/fluxplane-plugin@latest"

type batchInstallResult struct {
	Plugin    management.Ref `json:"plugin"`
	Installed bool           `json:"installed,omitempty"`
	Updated   bool           `json:"updated,omitempty"`
	Skipped   bool           `json:"skipped,omitempty"`
	Error     string         `json:"error,omitempty"`
}

type upgradeResult struct {
	CLIUpdated bool                 `json:"cli_updated"`
	CLIError   string               `json:"cli_error,omitempty"`
	Plugins    []batchInstallResult `json:"plugins,omitempty"`
	Skill      skillRefreshResult   `json:"skill"`
}

// installAllPlugins (re)installs every marketplace plugin. With remote=true it
// forces the published go_install source over any local_path (used by upgrade);
// otherwise it builds from local_path when available (dev sync). Per-plugin
// failures are collected, never aborting the batch.
func installAllPlugins(ctx context.Context, backend management.Backend, remote, dryRun bool) []batchInstallResult {
	catalog, err := backend.SearchPlugins(ctx, management.SearchRequest{})
	if err != nil {
		return []batchInstallResult{{Error: err.Error()}}
	}
	var out []batchInstallResult
	for _, plugin := range catalog.Plugins {
		name := strings.TrimSpace(plugin.Ref.Name)
		if name == "" {
			continue
		}
		// Only attempt plugins resolvable from the marketplace (they carry a
		// go_install or local_path label); skip ad-hoc installed plugins.
		if strings.TrimSpace(plugin.Labels["go_install"]) == "" && strings.TrimSpace(plugin.Labels["local_path"]) == "" {
			out = append(out, batchInstallResult{Plugin: plugin.Ref, Skipped: true})
			continue
		}
		res := batchInstallResult{Plugin: plugin.Ref}
		r, err := backend.InstallPlugin(ctx, management.InstallRequest{Ref: plugin.Ref, Force: true, PreferRemote: remote, DryRun: dryRun})
		if err != nil {
			res.Error = err.Error()
		} else {
			res.Installed = r.Installed
			res.Updated = r.Updated
		}
		out = append(out, res)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Plugin.Name < out[j].Plugin.Name })
	return out
}

func newUpgradeCommand(backend management.Backend) *cobra.Command {
	var skipSelf bool
	var skipPlugins bool
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Update the fluxplane-plugin CLI and installed plugins to their latest published versions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			result := upgradeResult{}
			if !skipSelf && !dryRun {
				if err := goInstallSelf(cmd); err != nil {
					result.CLIError = err.Error()
				} else {
					result.CLIUpdated = true
				}
			}
			if !skipPlugins {
				result.Plugins = installAllPlugins(cmd.Context(), backend, true, dryRun)
			}
			if !dryRun {
				result.Skill = refreshInstalledSkills(cmd.Context(), backend)
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().BoolVar(&skipSelf, "skip-cli", false, "do not update the fluxplane-plugin CLI itself")
	cmd.Flags().BoolVar(&skipPlugins, "skip-plugins", false, "do not update installed plugins")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "resolve without installing")
	return cmd
}

// goInstallSelf reinstalls the CLI from its published latest version. It runs in
// module mode (go install pkg@latest ignores any workspace), streaming progress
// to stderr so stdout stays the JSON result.
func goInstallSelf(cmd *cobra.Command) error {
	c := exec.CommandContext(cmd.Context(), "go", "install", selfInstallTarget)
	c.Stdout = cmd.ErrOrStderr()
	c.Stderr = cmd.ErrOrStderr()
	c.Env = append(os.Environ(), "GOWORK=off")
	return c.Run()
}

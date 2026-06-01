package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fluxplane/fluxplane-plugin/management"
)

// Options configures the reusable plugin management command tree.
type Options struct {
	Backend management.Backend
	Out     io.Writer
	Err     io.Writer
}

// New returns a reusable Cobra root command for plugin management.
func New(opts Options) *cobra.Command {
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	errOut := opts.Err
	if errOut == nil {
		errOut = io.Discard
	}

	cmd := &cobra.Command{
		Use:          "fluxplane-plugin",
		Short:        "Manage Fluxplane plugins",
		SilenceUsage: true,
	}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.AddCommand(
		newInstallCommand(opts.Backend),
		newListCommand(opts.Backend),
		newRemoveCommand(opts.Backend),
		newSearchCommand(opts.Backend),
		newManifestCommand(opts.Backend),
		newRunCommand(opts.Backend),
	)
	return cmd
}

func backendRequired(backend management.Backend) error {
	if backend == nil {
		return errors.New("fluxplane-plugin: no plugin management backend configured")
	}
	return nil
}

func parseRef(arg string) management.Ref {
	name, version, _ := strings.Cut(arg, "@")
	return management.Ref{Name: strings.TrimSpace(name), Version: strings.TrimSpace(version)}
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func newInstallCommand(backend management.Backend) *cobra.Command {
	var source string
	var force bool
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "install PLUGIN[@VERSION]",
		Short: "Install a plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			result, err := backend.InstallPlugin(cmd.Context(), management.InstallRequest{
				Ref:    parseRef(args[0]),
				Source: source,
				Force:  force,
				DryRun: dryRun,
			})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "plugin source or registry")
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing plugin")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "resolve without installing")
	return cmd
}

func newListCommand(backend management.Backend) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List installed plugins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			plugins, err := backend.ListPlugins(cmd.Context(), management.ListRequest{All: all})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), plugins)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include disabled or hidden plugins")
	return cmd
}

func newRemoveCommand(backend management.Backend) *cobra.Command {
	var force bool
	var dryRun bool
	cmd := &cobra.Command{
		Use:     "remove PLUGIN",
		Aliases: []string{"rm", "uninstall"},
		Short:   "Remove an installed plugin",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			result, err := backend.RemovePlugin(cmd.Context(), management.RemoveRequest{Ref: parseRef(args[0]), Force: force, DryRun: dryRun})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "force removal")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "resolve without removing")
	return cmd
}

func newSearchCommand(backend management.Backend) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "search [QUERY]",
		Short: "Search plugin registries",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			query := ""
			if len(args) > 0 {
				query = args[0]
			}
			result, err := backend.SearchPlugins(cmd.Context(), management.SearchRequest{Query: query, Limit: limit})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum number of plugins to return")
	return cmd
}

func newManifestCommand(backend management.Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "manifest PLUGIN[@VERSION]",
		Short: "Print a plugin manifest",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			result, err := backend.PluginManifest(cmd.Context(), management.ManifestRequest{Ref: parseRef(args[0])})
			if err != nil {
				return err
			}
			if len(result.Manifest) == 0 {
				return printJSON(cmd.OutOrStdout(), result)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(result.Manifest))
			return err
		},
	}
	return cmd
}

func newRunCommand(backend management.Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run PLUGIN [-- ARGS...]",
		Short: "Run a plugin",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			result, err := backend.RunPlugin(cmd.Context(), management.RunRequest{Ref: parseRef(args[0]), Args: args[1:]})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	return cmd
}

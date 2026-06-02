package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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
		newUpdateCommand(opts.Backend),
		newListCommand(opts.Backend),
		newStatusCommand(opts.Backend),
		newEnableCommand(opts.Backend),
		newDisableCommand(opts.Backend),
		newRemoveCommand(opts.Backend),
		newSearchCommand(opts.Backend),
		newManifestCommand(opts.Backend),
		newAuthCommand(opts.Backend),
		newOperationCommand(opts.Backend),
		newDatasourceCommand(opts.Backend),
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

type runtimeFlags struct {
	kind    string
	command string
	args    []string
	path    string
}

func (f runtimeFlags) spec() management.RuntimeSpec {
	return management.RuntimeSpec{Kind: strings.TrimSpace(f.kind), Command: strings.TrimSpace(f.command), Args: append([]string(nil), f.args...), Path: strings.TrimSpace(f.path)}
}

func addRuntimeFlags(cmd *cobra.Command, flags *runtimeFlags) {
	cmd.Flags().StringVar(&flags.kind, "runtime-kind", "", "plugin runtime kind")
	cmd.Flags().StringVar(&flags.command, "command", "", "plugin command")
	cmd.Flags().StringArrayVar(&flags.args, "arg", nil, "plugin command argument")
	cmd.Flags().StringVar(&flags.path, "path", "", "plugin filesystem path")
}

func readManifest(path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("fluxplane-plugin: read manifest: %w", err)
	}
	return data, nil
}

func readJSONPayload(input, path string) (json.RawMessage, error) {
	input = strings.TrimSpace(input)
	path = strings.TrimSpace(path)
	if input != "" && path != "" {
		return nil, errors.New("fluxplane-plugin: pass either --input or --input-file, not both")
	}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("fluxplane-plugin: read input file: %w", err)
		}
		input = strings.TrimSpace(string(data))
	}
	if input == "" {
		return nil, nil
	}
	raw := json.RawMessage(input)
	if !json.Valid(raw) {
		return nil, errors.New("fluxplane-plugin: input must be valid JSON")
	}
	return append(json.RawMessage(nil), raw...), nil
}

func parseMetadata(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for _, value := range values {
		key, val, ok := strings.Cut(value, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("fluxplane-plugin: metadata value %q must be key=value", value)
		}
		out[key] = strings.TrimSpace(val)
	}
	return out, nil
}

func newInstallCommand(backend management.Backend) *cobra.Command {
	var source string
	var force bool
	var dryRun bool
	var manifestPath string
	var manifestRef string
	var runtime runtimeFlags
	cmd := &cobra.Command{
		Use:   "install PLUGIN[@VERSION]",
		Short: "Install a plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			manifest, err := readManifest(manifestPath)
			if err != nil {
				return err
			}
			result, err := backend.InstallPlugin(cmd.Context(), management.InstallRequest{
				Ref:         parseRef(args[0]),
				Source:      source,
				Force:       force,
				Runtime:     runtime.spec(),
				Manifest:    manifest,
				ManifestRef: manifestRef,
				DryRun:      dryRun,
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
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "plugin manifest JSON file")
	cmd.Flags().StringVar(&manifestRef, "manifest-ref", "", "plugin manifest reference")
	addRuntimeFlags(cmd, &runtime)
	return cmd
}

func newUpdateCommand(backend management.Backend) *cobra.Command {
	var source string
	var dryRun bool
	var manifestPath string
	var manifestRef string
	var runtime runtimeFlags
	cmd := &cobra.Command{
		Use:   "update PLUGIN[@VERSION]",
		Short: "Update a plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			manifest, err := readManifest(manifestPath)
			if err != nil {
				return err
			}
			result, err := backend.UpdatePlugin(cmd.Context(), management.UpdateRequest{
				Ref:         parseRef(args[0]),
				Source:      source,
				Runtime:     runtime.spec(),
				Manifest:    manifest,
				ManifestRef: manifestRef,
				DryRun:      dryRun,
			})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "plugin source or registry")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "resolve without updating")
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "plugin manifest JSON file")
	cmd.Flags().StringVar(&manifestRef, "manifest-ref", "", "plugin manifest reference")
	addRuntimeFlags(cmd, &runtime)
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

func newStatusCommand(backend management.Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status [PLUGIN[@VERSION]]",
		Short: "Show plugin state",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			var ref management.Ref
			if len(args) > 0 {
				ref = parseRef(args[0])
			}
			result, err := backend.PluginStatus(cmd.Context(), management.StatusRequest{Ref: ref})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	return cmd
}

func newEnableCommand(backend management.Backend) *cobra.Command {
	return newEnabledCommand(backend, true)
}

func newDisableCommand(backend management.Backend) *cobra.Command {
	return newEnabledCommand(backend, false)
}

func newEnabledCommand(backend management.Backend, enabled bool) *cobra.Command {
	var dryRun bool
	use := "enable PLUGIN[@VERSION]"
	short := "Enable a plugin"
	if !enabled {
		use = "disable PLUGIN[@VERSION]"
		short = "Disable a plugin"
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			result, err := backend.SetPluginEnabled(cmd.Context(), management.SetEnabledRequest{Ref: parseRef(args[0]), Enabled: enabled, DryRun: dryRun})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "resolve without changing state")
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

func newAuthCommand(backend management.Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage plugin auth state",
	}
	cmd.AddCommand(
		newAuthStatusCommand(backend),
		newAuthMethodsCommand(backend),
		newAuthConnectCommand(backend),
		newAuthTestCommand(backend),
		newAuthDisconnectCommand(backend),
	)
	return cmd
}

func newAuthStatusCommand(backend management.Backend) *cobra.Command {
	var instance string
	cmd := &cobra.Command{
		Use:   "status PLUGIN[@VERSION]",
		Short: "Show plugin auth state",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			result, err := backend.AuthStatus(cmd.Context(), management.AuthStatusRequest{Ref: parseRef(args[0]), Instance: instance})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	return cmd
}

func newAuthMethodsCommand(backend management.Backend) *cobra.Command {
	var instance string
	cmd := &cobra.Command{
		Use:   "methods PLUGIN[@VERSION]",
		Short: "List plugin auth methods",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			result, err := backend.AuthMethods(cmd.Context(), management.AuthMethodsRequest{Ref: parseRef(args[0]), Instance: instance})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	return cmd
}

func newAuthConnectCommand(backend management.Backend) *cobra.Command {
	var instance string
	var method string
	var metadataValues []string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "connect PLUGIN[@VERSION]",
		Short: "Connect plugin auth",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			metadata, err := parseMetadata(metadataValues)
			if err != nil {
				return err
			}
			result, err := backend.AuthConnect(cmd.Context(), management.AuthConnectRequest{Ref: parseRef(args[0]), Instance: instance, Method: method, Metadata: metadata, DryRun: dryRun})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	cmd.Flags().StringVar(&method, "method", "default", "auth method")
	cmd.Flags().StringArrayVar(&metadataValues, "field", nil, "auth field as key=value")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "resolve without changing state")
	return cmd
}

func newAuthTestCommand(backend management.Backend) *cobra.Command {
	var instance string
	var method string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "test PLUGIN[@VERSION]",
		Short: "Record plugin auth test state",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			result, err := backend.AuthTest(cmd.Context(), management.AuthTestRequest{Ref: parseRef(args[0]), Instance: instance, Method: method, DryRun: dryRun})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	cmd.Flags().StringVar(&method, "method", "default", "auth method")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "resolve without changing state")
	return cmd
}

func newAuthDisconnectCommand(backend management.Backend) *cobra.Command {
	var instance string
	var method string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "disconnect PLUGIN[@VERSION]",
		Short: "Clear plugin auth state",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			result, err := backend.AuthDisconnect(cmd.Context(), management.AuthDisconnectRequest{Ref: parseRef(args[0]), Instance: instance, Method: method, DryRun: dryRun})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	cmd.Flags().StringVar(&method, "method", "default", "auth method")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "resolve without changing state")
	return cmd
}

func newOperationCommand(backend management.Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "operation",
		Aliases: []string{"op", "operations"},
		Short:   "List and invoke plugin operations",
	}
	cmd.AddCommand(
		newOperationListCommand(backend),
		newOperationInvokeCommand(backend),
	)
	return cmd
}

func newOperationListCommand(backend management.Backend) *cobra.Command {
	var instance string
	cmd := &cobra.Command{
		Use:   "list PLUGIN[@VERSION]",
		Short: "List plugin operations",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			result, err := backend.ListOperations(cmd.Context(), management.OperationListRequest{Ref: parseRef(args[0]), Instance: instance})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	return cmd
}

func newOperationInvokeCommand(backend management.Backend) *cobra.Command {
	var instance string
	var input string
	var inputFile string
	cmd := &cobra.Command{
		Use:     "invoke PLUGIN[@VERSION] OPERATION",
		Aliases: []string{"run", "call"},
		Short:   "Invoke a plugin operation",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			payload, err := readJSONPayload(input, inputFile)
			if err != nil {
				return err
			}
			result, err := backend.InvokeOperation(cmd.Context(), management.OperationInvokeRequest{Ref: parseRef(args[0]), Instance: instance, Operation: args[1], Input: payload})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	cmd.Flags().StringVar(&input, "input", "", "operation input JSON")
	cmd.Flags().StringVar(&inputFile, "input-file", "", "operation input JSON file")
	return cmd
}

func newDatasourceCommand(backend management.Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "datasource",
		Aliases: []string{"ds", "datasources"},
		Short:   "List and query plugin datasources",
	}
	cmd.AddCommand(
		newDatasourceListCommand(backend),
		newDatasourceCallCommand(backend, "search"),
		newDatasourceCallCommand(backend, "get"),
		newDatasourceCallCommand(backend, "lookup"),
	)
	return cmd
}

func newDatasourceListCommand(backend management.Backend) *cobra.Command {
	var instance string
	cmd := &cobra.Command{
		Use:   "list PLUGIN[@VERSION]",
		Short: "List plugin datasources",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			result, err := backend.ListDatasources(cmd.Context(), management.DatasourceListRequest{Ref: parseRef(args[0]), Instance: instance})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	return cmd
}

func newDatasourceCallCommand(backend management.Backend, capability string) *cobra.Command {
	var instance string
	var input string
	var inputFile string
	cmd := &cobra.Command{
		Use:   capability + " PLUGIN[@VERSION]",
		Short: "Call plugin datasource " + capability,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			payload, err := readJSONPayload(input, inputFile)
			if err != nil {
				return err
			}
			result, err := backend.CallDatasource(cmd.Context(), management.DatasourceCallRequest{Ref: parseRef(args[0]), Instance: instance, Capability: capability, Input: payload})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	cmd.Flags().StringVar(&input, "input", "", "datasource input JSON")
	cmd.Flags().StringVar(&inputFile, "input-file", "", "datasource input JSON file")
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

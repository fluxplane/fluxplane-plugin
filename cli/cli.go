package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	fpendpoint "github.com/fluxplane/fluxplane-endpoint"
	"github.com/fluxplane/fluxplane-plugin/management"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
	"github.com/fluxplane/fluxplane-plugin/protocol"
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
		// Runs only when a command's RunE succeeded. Regenerates installed
		// skills after state-changing commands so the skill never goes stale.
		PersistentPostRunE: func(c *cobra.Command, _ []string) error {
			refreshSkillsAfterStateChange(c, opts.Backend)
			return nil
		},
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
		newLookupCommand(opts.Backend),
		newContextCommand(opts.Backend),
		newEvidenceCommand(opts.Backend),
		newIndexCommand(opts.Backend),
		newEndpointCommand(opts.Backend),
		newRunCommand(opts.Backend),
		newSkillCommand(opts.Backend),
		newUpgradeCommand(opts.Backend),
		newDevCommand(opts.Backend),
		newDoctorCommand(opts.Backend),
		newSelftestCommand(opts.Backend),
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

func copyRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
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

func parseAuthEndpoints(values []string, urlValue, id, product string) ([]management.AuthEndpoint, error) {
	var out []management.AuthEndpoint
	if strings.TrimSpace(urlValue) != "" {
		out = append(out, management.AuthEndpoint{ID: strings.TrimSpace(id), URL: strings.TrimSpace(urlValue), Product: strings.TrimSpace(product)})
	}
	for _, value := range values {
		name, endpointURL, ok := strings.Cut(value, "=")
		if !ok {
			endpointURL = name
			name = ""
		}
		endpointURL = strings.TrimSpace(endpointURL)
		if endpointURL == "" {
			return nil, fmt.Errorf("fluxplane-plugin: endpoint value %q must include a URL", value)
		}
		out = append(out, management.AuthEndpoint{Name: strings.TrimSpace(name), URL: endpointURL, Product: strings.TrimSpace(product)})
	}
	return out, nil
}

func promptAuthConnect(cmd *cobra.Command, backend management.Backend, ref management.Ref, instance, method string, metadata map[string]string, endpoints []management.AuthEndpoint) (map[string]string, []management.AuthEndpoint, string, error) {
	if metadata == nil {
		metadata = map[string]string{}
	}
	reader := bufio.NewReader(cmd.InOrStdin())
	methods, err := backend.AuthMethods(cmd.Context(), management.AuthMethodsRequest{Ref: ref, Instance: instance})
	if err != nil {
		return nil, nil, "", err
	}
	selected := selectAuthMethod(method, methods.Methods)
	for _, authMethod := range methods.Methods {
		if strings.TrimSpace(authMethod.Name) != selected {
			continue
		}
		for _, field := range authMethod.Fields {
			name := strings.TrimSpace(field.Name)
			if name == "" || strings.TrimSpace(metadata[name]) != "" {
				continue
			}
			defaultValue, _ := firstValueFromEnv(field.Env)
			value, err := promptValue(cmd, reader, name, defaultValue)
			if err != nil {
				return nil, nil, "", err
			}
			if strings.TrimSpace(value) != "" {
				metadata[name] = strings.TrimSpace(value)
			}
		}
		break
	}
	manifest, err := backend.PluginManifest(cmd.Context(), management.ManifestRequest{Ref: ref})
	if err != nil {
		return nil, nil, "", err
	}
	var decoded sdkmanifest.PluginManifest
	if len(manifest.Manifest) > 0 {
		if err := json.Unmarshal(manifest.Manifest, &decoded); err != nil {
			return nil, nil, "", err
		}
	}
	configured := map[string]bool{}
	for _, endpoint := range endpoints {
		if strings.TrimSpace(endpoint.Name) != "" {
			configured[strings.TrimSpace(endpoint.Name)] = true
		}
	}
	for _, spec := range decoded.Endpoints {
		name := strings.TrimSpace(spec.Name)
		if configured[name] {
			continue
		}
		defaultValue, _ := firstValueFromEnv(spec.Env)
		value, err := promptValue(cmd, reader, firstNonEmpty(name, "endpoint_url"), defaultValue)
		if err != nil {
			return nil, nil, "", err
		}
		if strings.TrimSpace(value) != "" {
			endpoints = append(endpoints, management.AuthEndpoint{Name: name, URL: strings.TrimSpace(value), Product: firstString(spec.Products)})
		}
	}
	return metadata, endpoints, selected, nil
}

func promptValue(cmd *cobra.Command, reader *bufio.Reader, label, defaultValue string) (string, error) {
	if strings.TrimSpace(defaultValue) != "" {
		if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "%s [%s]: ", label, defaultValue); err != nil {
			return "", err
		}
	} else if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "%s: ", label); err != nil {
		return "", err
	}
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultValue, nil
	}
	return value, nil
}

func firstValueFromEnv(keys []string) (string, bool) {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(strings.TrimSpace(key))); value != "" {
			return value, true
		}
	}
	return "", false
}

func selectAuthMethod(requested string, methods []sdkmanifest.AuthMethod) string {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		return requested
	}
	for _, method := range methods {
		if name := strings.TrimSpace(method.Name); name != "" {
			return name
		}
	}
	return "default"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func firstString(values []string) string {
	return firstNonEmpty(values...)
}

func mergeStringMaps(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, value := range base {
		out[key] = value
	}
	for key, value := range override {
		out[key] = value
	}
	return out
}

func endpointSaveInput(args []string, input, inputFile string) (fpendpoint.EndpointRef, error) {
	payload, err := readJSONPayload(input, inputFile)
	if err != nil {
		return fpendpoint.EndpointRef{}, err
	}
	if len(payload) > 0 {
		var endpoint fpendpoint.EndpointRef
		if err := json.Unmarshal(payload, &endpoint); err != nil {
			return fpendpoint.EndpointRef{}, fmt.Errorf("fluxplane-plugin: endpoint input must be an endpoint JSON object: %w", err)
		}
		return endpoint, nil
	}
	if len(args) != 2 {
		return fpendpoint.EndpointRef{}, errors.New("fluxplane-plugin: endpoint save requires ID and URL unless --input is provided")
	}
	return fpendpoint.EndpointRef{ID: strings.TrimSpace(args[0]), URL: strings.TrimSpace(args[1])}, nil
}

func newInstallCommand(backend management.Backend) *cobra.Command {
	var source string
	var force bool
	var dryRun bool
	var manifestPath string
	var manifestRef string
	var all bool
	var remote bool
	var runtime runtimeFlags
	cmd := &cobra.Command{
		Use:   "install [PLUGIN[@VERSION]]",
		Short: "Install a plugin (or all marketplace plugins with --all)",
		Args: func(cmd *cobra.Command, args []string) error {
			if all {
				return cobra.NoArgs(cmd, args)
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			if all {
				return printJSON(cmd.OutOrStdout(), installAllPlugins(cmd.Context(), backend, remote, dryRun))
			}
			manifest, err := readManifest(manifestPath)
			if err != nil {
				return err
			}
			result, err := backend.InstallPlugin(cmd.Context(), management.InstallRequest{
				Ref:          parseRef(args[0]),
				Source:       source,
				Force:        force,
				Runtime:      runtime.spec(),
				Manifest:     manifest,
				ManifestRef:  manifestRef,
				DryRun:       dryRun,
				PreferRemote: remote,
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
	cmd.Flags().BoolVar(&all, "all", false, "install every marketplace plugin (rebuilds local_build plugins)")
	cmd.Flags().BoolVar(&remote, "remote", false, "force the published go_install source instead of a local_path build")
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
	var endpointValues []string
	var endpointURL string
	var endpointID string
	var endpointProduct string
	var auto bool
	var interactive bool
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "connect PLUGIN[@VERSION]",
		Short: "Connect plugin auth",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			ref := parseRef(args[0])
			if auto {
				result, err := backend.AuthAuto(cmd.Context(), management.AuthAutoRequest{Ref: ref, Instance: instance, DryRun: dryRun})
				if err != nil {
					return err
				}
				return printJSON(cmd.OutOrStdout(), result)
			}
			metadata, err := parseMetadata(metadataValues)
			if err != nil {
				return err
			}
			endpoints, err := parseAuthEndpoints(endpointValues, endpointURL, endpointID, endpointProduct)
			if err != nil {
				return err
			}
			if interactive {
				metadata, endpoints, method, err = promptAuthConnect(cmd, backend, ref, instance, method, metadata, endpoints)
				if err != nil {
					return err
				}
			}
			result, err := backend.AuthConnect(cmd.Context(), management.AuthConnectRequest{Ref: ref, Instance: instance, Method: method, Metadata: metadata, Endpoints: endpoints, DryRun: dryRun})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	cmd.Flags().StringVar(&method, "method", "", "auth method")
	cmd.Flags().StringArrayVar(&metadataValues, "field", nil, "auth field as key=value")
	cmd.Flags().StringArrayVar(&endpointValues, "endpoint", nil, "endpoint as name=url or url")
	cmd.Flags().StringVar(&endpointURL, "endpoint-url", "", "endpoint URL")
	cmd.Flags().StringVar(&endpointID, "endpoint-id", "", "endpoint ID")
	cmd.Flags().StringVar(&endpointProduct, "endpoint-product", "", "endpoint product")
	cmd.Flags().BoolVar(&auto, "auto", false, "connect from manifest-declared environment variables")
	cmd.Flags().BoolVar(&interactive, "interactive", false, "prompt for manifest-declared setup values")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "resolve without changing state")
	cmd.AddCommand(newAuthAutoCommand(backend))
	return cmd
}

func newAuthAutoCommand(backend management.Backend) *cobra.Command {
	var instance string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "auto [PLUGIN[@VERSION]]",
		Short: "Connect auth from manifest-declared environment variables",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			if len(args) == 1 {
				result, err := backend.AuthAuto(cmd.Context(), management.AuthAutoRequest{Ref: parseRef(args[0]), Instance: instance, DryRun: dryRun})
				if err != nil {
					return err
				}
				return printJSON(cmd.OutOrStdout(), result)
			}
			plugins, err := backend.ListPlugins(cmd.Context(), management.ListRequest{All: true})
			if err != nil {
				return err
			}
			out := struct {
				Instance string                      `json:"instance"`
				Plugins  []management.AuthAutoResult `json:"plugins"`
			}{Instance: instance}
			for _, plugin := range plugins {
				result, err := backend.AuthAuto(cmd.Context(), management.AuthAutoRequest{Ref: plugin.Ref, Instance: instance, DryRun: dryRun})
				if err != nil {
					result = management.AuthAutoResult{Plugin: plugin.Ref, Instance: instance, Message: err.Error()}
				}
				out.Plugins = append(out.Plugins, result)
			}
			return printJSON(cmd.OutOrStdout(), out)
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
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
		newOperationDescribeCommand(backend),
		newOperationSearchCommand(backend),
		newOperationInvokeCommand(backend),
		newOperationBatchCommand(backend),
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
	var dryRun bool
	var noValidate bool
	var resultOnly bool
	var fields string
	cmd := &cobra.Command{
		Use:     "invoke PLUGIN[@VERSION] OPERATION",
		Aliases: []string{"run", "call"},
		Short:   "Invoke a plugin operation",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			ref := parseRef(args[0])
			opName := args[1]
			payload, err := readJSONPayload(input, inputFile)
			if err != nil {
				return err
			}

			// Local pre-validation against the operation's schema. Schema discovery
			// failures are non-fatal for a real invoke (never block a valid call),
			// but surfaced under --dry-run.
			if !noValidate {
				schema, found, derr := operationSchema(cmd.Context(), backend, ref, instance, opName)
				if derr != nil && dryRun {
					return derr
				}
				if found {
					problems := validateOperationInput(schema, payload)
					if dryRun {
						return printJSON(cmd.OutOrStdout(), operationDryRunResult{
							Plugin: ref.Name, Operation: opName, Valid: len(problems) == 0, Problems: problems, Input: payload,
						})
					}
					if len(problems) > 0 {
						_ = printJSON(cmd.ErrOrStderr(), protocol.Error{Code: "invalid_input", Message: "input failed local validation", Fields: problemFields(problems)})
						return fmt.Errorf("fluxplane-plugin: input failed local validation for %s %s", ref.Name, opName)
					}
				} else if dryRun {
					return printJSON(cmd.OutOrStdout(), operationDryRunResult{Plugin: ref.Name, Operation: opName, Valid: true, Input: payload})
				}
			} else if dryRun {
				return printJSON(cmd.OutOrStdout(), operationDryRunResult{Plugin: ref.Name, Operation: opName, Valid: true, Input: payload})
			}

			result, err := backend.InvokeOperation(cmd.Context(), management.OperationInvokeRequest{Ref: ref, Instance: instance, Operation: opName, Input: payload})
			if err != nil {
				return err
			}
			return printOperationResult(cmd.OutOrStdout(), result, resultOnly, splitFieldPaths(fields))
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	cmd.Flags().StringVar(&input, "input", "", "operation input JSON")
	cmd.Flags().StringVar(&inputFile, "input-file", "", "operation input JSON file")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate input locally and report; do not call the backend")
	cmd.Flags().BoolVar(&noValidate, "no-validate", false, "skip local input validation")
	cmd.Flags().BoolVar(&resultOnly, "result-only", false, "print only the operation result, not the envelope")
	cmd.Flags().StringVar(&fields, "field", "", "comma-separated dot-paths to extract from the result (e.g. key,issue.fields.status.name)")
	return cmd
}

// operationSchema resolves an operation's parsed input schema via the backend.
// Returns (schema, found, err); found is false when the op isn't advertised.
func operationSchema(ctx context.Context, backend management.Backend, ref management.Ref, instance, opName string) (operationInputSchema, bool, error) {
	list, err := backend.ListOperations(ctx, management.OperationListRequest{Ref: ref, Instance: instance})
	if err != nil {
		return operationInputSchema{}, false, err
	}
	name := strings.TrimSpace(opName)
	for _, op := range list.Operations {
		if strings.TrimSpace(op.Name) == name {
			return parseOperationInputSchema(op), true, nil
		}
	}
	return operationInputSchema{}, false, nil
}

func problemFields(problems []validationProblem) map[string]string {
	out := map[string]string{}
	for _, p := range problems {
		key := p.Field
		if key == "" {
			key = "_"
		}
		out[key] = p.Reason
	}
	return out
}

func newOperationBatchCommand(backend management.Backend) *cobra.Command {
	var instance string
	var input string
	var inputFile string
	cmd := &cobra.Command{
		Use:   "batch PLUGIN[@VERSION]",
		Short: "Invoke multiple plugin operations",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			payload, err := readJSONPayload(input, inputFile)
			if err != nil {
				return err
			}
			calls, err := parseOperationBatch(payload)
			if err != nil {
				return err
			}
			result, err := backend.BatchOperations(cmd.Context(), management.OperationBatchRequest{Ref: parseRef(args[0]), Instance: instance, Calls: calls})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	cmd.Flags().StringVar(&input, "input", "", "operation batch JSON")
	cmd.Flags().StringVar(&inputFile, "input-file", "", "operation batch JSON file")
	return cmd
}

func parseOperationBatch(raw json.RawMessage) ([]protocol.OperationCall, error) {
	if len(raw) == 0 {
		return nil, errors.New("fluxplane-plugin: operation batch input is required")
	}
	var calls []protocol.OperationCall
	if err := json.Unmarshal(raw, &calls); err == nil && len(calls) > 0 {
		return normalizeOperationBatchCalls(calls)
	}
	var batch protocol.OperationBatch
	if err := json.Unmarshal(raw, &batch); err != nil {
		return nil, fmt.Errorf("fluxplane-plugin: operation batch input must be an array of calls or {\"calls\":[...]}: %w", err)
	}
	return normalizeOperationBatchCalls(batch.Calls)
}

func normalizeOperationBatchCalls(calls []protocol.OperationCall) ([]protocol.OperationCall, error) {
	if len(calls) == 0 {
		return nil, errors.New("fluxplane-plugin: operation batch must contain at least one call")
	}
	out := make([]protocol.OperationCall, 0, len(calls))
	for i, call := range calls {
		call.Name = strings.TrimSpace(call.Name)
		if call.Name == "" {
			return nil, fmt.Errorf("fluxplane-plugin: batch call %d operation name is required", i+1)
		}
		if strings.TrimSpace(call.ID) == "" {
			call.ID = fmt.Sprintf("%d", i+1)
		}
		out = append(out, call)
	}
	return out, nil
}

func newDatasourceCommand(backend management.Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "datasource",
		Aliases: []string{"ds", "datasources"},
		Short:   "List and query plugin datasources",
	}
	cmd.AddCommand(
		newDatasourceListCommand(backend),
		newDatasourceRecordsCommand(backend),
		newDatasourceCallCommand(backend, "search"),
		newDatasourceSearchAllCommand(backend),
		newDatasourceCallCommand(backend, "get"),
		newDatasourceBatchGetCommand(backend),
		newDatasourceCallCommand(backend, "lookup"),
		newDatasourceLookupAllCommand(backend),
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

func newDatasourceRecordsCommand(backend management.Backend) *cobra.Command {
	cmd := newDatasourceCallCommand(backend, "list")
	cmd.Use = "records PLUGIN[@VERSION]"
	cmd.Aliases = []string{"list-records", "record-list"}
	cmd.Short = "List datasource records"
	return cmd
}

func newDatasourceBatchGetCommand(backend management.Backend) *cobra.Command {
	cmd := newDatasourceCallCommand(backend, "batch_get")
	cmd.Use = "batch-get PLUGIN[@VERSION]"
	cmd.Aliases = []string{"batch"}
	cmd.Short = "Get multiple datasource records"
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

func newDatasourceSearchAllCommand(backend management.Backend) *cobra.Command {
	var instance string
	var entity string
	var limit int
	cmd := &cobra.Command{
		Use:   "search-all QUERY",
		Short: "Search all installed datasource plugins",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			query := strings.Join(args, " ")
			result, err := fanoutDatasource(cmd.Context(), backend, "search", instance, map[string]any{"query": query, "entity": entity, "limit": limit})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), map[string]any{"query": query, "results": result})
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	cmd.Flags().StringVar(&entity, "entity", "", "entity type filter")
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum records per plugin")
	return cmd
}

func newDatasourceLookupAllCommand(backend management.Backend) *cobra.Command {
	return newLookupCommandWithUse(backend, "lookup-all TEXT", "Lookup across all installed datasource plugins")
}

func newLookupCommand(backend management.Backend) *cobra.Command {
	return newLookupCommandWithUse(backend, "lookup TEXT", "Lookup canonical datasource references")
}

func newLookupCommandWithUse(backend management.Backend, use, short string) *cobra.Command {
	var instance string
	var entity string
	var limit int
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			text := strings.Join(args, " ")
			result, err := fanoutDatasource(cmd.Context(), backend, "lookup", instance, map[string]any{"text": text, "entity": entity, "limit": limit})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), map[string]any{"text": text, "results": result})
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	cmd.Flags().StringVar(&entity, "entity", "", "entity type filter")
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum matches per plugin")
	return cmd
}

func newContextCommand(backend management.Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "context",
		Aliases: []string{"ctx", "contexts"},
		Short:   "List and build plugin context providers",
	}
	cmd.AddCommand(
		newContextListCommand(backend),
		newContextBuildCommand(backend),
		newContextBuildAllCommand(backend),
	)
	return cmd
}

func newContextListCommand(backend management.Backend) *cobra.Command {
	var instance string
	cmd := &cobra.Command{
		Use:   "list PLUGIN[@VERSION]",
		Short: "List plugin context providers",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			result, err := backend.ListContextProviders(cmd.Context(), management.ContextListRequest{Ref: parseRef(args[0]), Instance: instance})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	return cmd
}

func newContextBuildCommand(backend management.Backend) *cobra.Command {
	var instance string
	var query string
	var kinds []string
	var limit int
	var input string
	var inputFile string
	cmd := &cobra.Command{
		Use:     "build PLUGIN[@VERSION]",
		Aliases: []string{"run"},
		Short:   "Build plugin context blocks",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			payload, err := readJSONPayload(input, inputFile)
			if err != nil {
				return err
			}
			result, err := backend.BuildContext(cmd.Context(), management.ContextBuildRequest{Ref: parseRef(args[0]), Instance: instance, Query: query, Kinds: kinds, Limit: limit, Input: payload})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	cmd.Flags().StringVar(&query, "query", "", "context query")
	cmd.Flags().StringArrayVar(&kinds, "kind", nil, "context block kind filter")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum context blocks to return")
	cmd.Flags().StringVar(&input, "input", "", "context build input JSON")
	cmd.Flags().StringVar(&inputFile, "input-file", "", "context build input JSON file")
	return cmd
}

func newContextBuildAllCommand(backend management.Backend) *cobra.Command {
	var instance string
	var kinds []string
	var limit int
	cmd := &cobra.Command{
		Use:   "build-all QUERY",
		Short: "Build context from all installed context plugins",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			query := strings.Join(args, " ")
			result, err := fanoutContext(cmd.Context(), backend, instance, query, kinds, limit)
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), map[string]any{"query": query, "results": result})
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	cmd.Flags().StringArrayVar(&kinds, "kind", nil, "context block kind filter")
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum context blocks per plugin")
	return cmd
}

func newEvidenceCommand(backend management.Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "evidence",
		Short: "Inspect and run plugin evidence observers",
	}
	cmd.AddCommand(
		newEvidenceListCommand(backend),
		newEvidenceObserveCommand(backend),
	)
	return cmd
}

func newEvidenceListCommand(backend management.Backend) *cobra.Command {
	var instance string
	cmd := &cobra.Command{
		Use:   "list PLUGIN[@VERSION]",
		Short: "List plugin evidence declarations",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			result, err := backend.ListEvidence(cmd.Context(), management.EvidenceListRequest{Ref: parseRef(args[0]), Instance: instance})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	return cmd
}

func newEvidenceObserveCommand(backend management.Backend) *cobra.Command {
	var instance string
	var phase string
	var input string
	var inputFile string
	cmd := &cobra.Command{
		Use:   "observe PLUGIN[@VERSION]",
		Short: "Run plugin evidence observers",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			payload, err := readJSONPayload(input, inputFile)
			if err != nil {
				return err
			}
			result, err := backend.ObserveEvidence(cmd.Context(), management.EvidenceObserveRequest{
				Ref:      parseRef(args[0]),
				Instance: instance,
				Phase:    sdkmanifest.ObservationPhase(strings.TrimSpace(phase)),
				Input:    payload,
			})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	cmd.Flags().StringVar(&phase, "phase", "", "observation phase")
	cmd.Flags().StringVar(&input, "input", "", "evidence observe input JSON")
	cmd.Flags().StringVar(&inputFile, "input-file", "", "evidence observe input JSON file")
	return cmd
}

type fanoutCallResult struct {
	Plugin   management.Ref  `json:"plugin"`
	Instance string          `json:"instance,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
}

func fanoutDatasource(ctx context.Context, backend management.Backend, capability, instance string, payload map[string]any) ([]fanoutCallResult, error) {
	plugins, err := datasourceCapablePlugins(ctx, backend, instance, capability)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	out := make([]fanoutCallResult, 0, len(plugins))
	for _, plugin := range plugins {
		call, err := backend.CallDatasource(ctx, management.DatasourceCallRequest{Ref: plugin.Ref, Instance: instance, Capability: capability, Input: raw})
		result := fanoutCallResult{Plugin: plugin.Ref, Instance: instance}
		if err != nil {
			result.Error = err.Error()
		} else {
			result.Result = copyRaw(call.Result)
		}
		out = append(out, result)
	}
	return out, nil
}

func datasourceCapablePlugins(ctx context.Context, backend management.Backend, instance, capability string) ([]management.Plugin, error) {
	plugins, err := backend.ListPlugins(ctx, management.ListRequest{All: true})
	if err != nil {
		return nil, err
	}
	var out []management.Plugin
	for _, plugin := range plugins {
		if !plugin.Installed || !plugin.Enabled {
			continue
		}
		listed, err := backend.ListDatasources(ctx, management.DatasourceListRequest{Ref: plugin.Ref, Instance: instance})
		if err != nil {
			continue
		}
		for _, datasource := range listed.Datasources {
			if hasCapability(datasource.Capabilities, capability) {
				out = append(out, plugin)
				break
			}
		}
	}
	return out, nil
}

func fanoutContext(ctx context.Context, backend management.Backend, instance, query string, kinds []string, limit int) ([]fanoutCallResult, error) {
	plugins, err := contextCapablePlugins(ctx, backend, instance)
	if err != nil {
		return nil, err
	}
	out := make([]fanoutCallResult, 0, len(plugins))
	for _, plugin := range plugins {
		call, err := backend.BuildContext(ctx, management.ContextBuildRequest{Ref: plugin.Ref, Instance: instance, Query: query, Kinds: kinds, Limit: limit})
		result := fanoutCallResult{Plugin: plugin.Ref, Instance: instance}
		if err != nil {
			result.Error = err.Error()
		} else {
			result.Result = copyRaw(call.Result)
		}
		out = append(out, result)
	}
	return out, nil
}

func contextCapablePlugins(ctx context.Context, backend management.Backend, instance string) ([]management.Plugin, error) {
	plugins, err := backend.ListPlugins(ctx, management.ListRequest{All: true})
	if err != nil {
		return nil, err
	}
	var out []management.Plugin
	for _, plugin := range plugins {
		if !plugin.Installed || !plugin.Enabled {
			continue
		}
		listed, err := backend.ListContextProviders(ctx, management.ContextListRequest{Ref: plugin.Ref, Instance: instance})
		if err != nil || len(listed.Context) == 0 {
			continue
		}
		out = append(out, plugin)
	}
	return out, nil
}

func hasCapability(capabilities []string, capability string) bool {
	capability = strings.TrimSpace(capability)
	for _, candidate := range capabilities {
		if strings.TrimSpace(candidate) == capability {
			return true
		}
	}
	return false
}

func newIndexCommand(backend management.Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Build and inspect plugin indexes",
	}
	cmd.AddCommand(newIndexBuildCommand(backend))
	cmd.AddCommand(newIndexStatusCommand(backend))
	return cmd
}

func newIndexBuildCommand(backend management.Backend) *cobra.Command {
	var instance string
	var index string
	var entity string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "build PLUGIN[@VERSION]",
		Short: "Build plugin index records",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			result, err := backend.BuildIndex(cmd.Context(), management.IndexBuildRequest{
				Ref:      parseRef(args[0]),
				Instance: instance,
				Index:    index,
				Entity:   entity,
				DryRun:   dryRun,
			})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	cmd.Flags().StringVar(&index, "index", "", "index name to build")
	cmd.Flags().StringVar(&entity, "entity", "", "entity type to build")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate and render without storing")
	return cmd
}

func newIndexStatusCommand(backend management.Backend) *cobra.Command {
	var instance string
	cmd := &cobra.Command{
		Use:   "status [PLUGIN[@VERSION]]",
		Short: "Show plugin index status",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			req := management.IndexStatusRequest{Instance: instance}
			if len(args) > 0 {
				req.Ref = parseRef(args[0])
			}
			result, err := backend.IndexStatus(cmd.Context(), req)
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	return cmd
}

func newEndpointCommand(backend management.Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:       "endpoint",
		Aliases:   []string{"endpoints"},
		Short:     "Discover and manage plugin endpoints",
		ValidArgs: []string{"discover", "list", "get", "save", "health", "test", "doctor", "import", "remove"},
	}
	cmd.AddCommand(newEndpointListCommand(backend))
	cmd.AddCommand(newEndpointGetCommand(backend))
	cmd.AddCommand(newEndpointSaveCommand(backend))
	cmd.AddCommand(newEndpointHealthCommand(backend))
	cmd.AddCommand(newEndpointTestCommand(backend))
	cmd.AddCommand(newEndpointDoctorCommand(backend))
	cmd.AddCommand(newEndpointImportCommand(backend))
	cmd.AddCommand(newEndpointRemoveCommand(backend))
	cmd.AddCommand(newEndpointDiscoverCommand(backend))
	return cmd
}

func newEndpointListCommand(backend management.Backend) *cobra.Command {
	var product string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List stored endpoints",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			result, err := backend.ListEndpoints(cmd.Context(), management.EndpointListRequest{Product: product})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "filter by product")
	return cmd
}

func newEndpointGetCommand(backend management.Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get ID",
		Short: "Show a stored endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			result, err := backend.GetEndpoint(cmd.Context(), management.EndpointGetRequest{ID: args[0]})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	return cmd
}

func newEndpointSaveCommand(backend management.Backend) *cobra.Command {
	var product string
	var protocolName string
	var source string
	var credentialRef string
	var labelValues []string
	var annotationValues []string
	var input string
	var inputFile string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "save [ID] [URL]",
		Short: "Store an endpoint",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			endpoint, err := endpointSaveInput(args, input, inputFile)
			if err != nil {
				return err
			}
			if product != "" {
				endpoint.Product = product
			}
			if protocolName != "" {
				endpoint.Protocol = protocolName
			}
			if source != "" {
				endpoint.Source = source
			}
			if credentialRef != "" {
				endpoint.CredentialRef = credentialRef
			}
			labels, err := parseMetadata(labelValues)
			if err != nil {
				return err
			}
			annotations, err := parseMetadata(annotationValues)
			if err != nil {
				return err
			}
			endpoint.Labels = mergeStringMaps(endpoint.Labels, labels)
			endpoint.Annotations = mergeStringMaps(endpoint.Annotations, annotations)
			result, err := backend.SaveEndpoint(cmd.Context(), management.EndpointSaveRequest{Endpoint: endpoint, DryRun: dryRun})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&product, "product", "", "endpoint product")
	cmd.Flags().StringVar(&protocolName, "protocol", "", "endpoint protocol")
	cmd.Flags().StringVar(&source, "source", "", "endpoint source")
	cmd.Flags().StringVar(&credentialRef, "credential-ref", "", "endpoint credential ref")
	cmd.Flags().StringArrayVar(&labelValues, "label", nil, "endpoint label key=value")
	cmd.Flags().StringArrayVar(&annotationValues, "annotation", nil, "endpoint annotation key=value")
	cmd.Flags().StringVar(&input, "input", "", "endpoint JSON")
	cmd.Flags().StringVar(&inputFile, "input-file", "", "endpoint JSON file")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate without writing")
	return cmd
}

func newEndpointHealthCommand(backend management.Backend) *cobra.Command {
	var ok bool
	var method string
	var durationMS int64
	var errorMessage string
	var detailValues []string
	var metadataValues []string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "health ID",
		Short: "Store endpoint health",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			details, err := parseMetadata(detailValues)
			if err != nil {
				return err
			}
			metadata, err := parseMetadata(metadataValues)
			if err != nil {
				return err
			}
			detailValues := map[string]any{}
			for key, value := range details {
				detailValues[key] = value
			}
			result, err := backend.SaveEndpointHealth(cmd.Context(), management.EndpointHealthRequest{
				ID: args[0],
				Health: fpendpoint.Health{
					OK:         ok,
					Method:     method,
					DurationMS: durationMS,
					Error:      errorMessage,
					Details:    detailValues,
					Metadata:   metadata,
				},
				DryRun: dryRun,
			})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().BoolVar(&ok, "ok", false, "mark endpoint health as OK")
	cmd.Flags().StringVar(&method, "method", "", "health probe method")
	cmd.Flags().Int64Var(&durationMS, "duration-ms", 0, "health probe duration in milliseconds")
	cmd.Flags().StringVar(&errorMessage, "error", "", "health probe error")
	cmd.Flags().StringArrayVar(&detailValues, "detail", nil, "health detail key=value")
	cmd.Flags().StringArrayVar(&metadataValues, "metadata", nil, "health metadata key=value")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate without writing")
	return cmd
}

func newEndpointTestCommand(backend management.Backend) *cobra.Command {
	var instance string
	var failOnError bool
	cmd := &cobra.Command{
		Use:   "test ID",
		Short: "Test a stored endpoint and update health",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			got, err := backend.GetEndpoint(cmd.Context(), management.EndpointGetRequest{ID: args[0]})
			if err != nil {
				return err
			}
			if !got.Found {
				return fmt.Errorf("fluxplane-plugin: unknown endpoint %q", args[0])
			}
			record := got.Record
			if record.ID == "" {
				record.EndpointRef = got.Endpoint
			}
			result := testEndpoint(cmd.Context(), backend, instance, record)
			if _, err := backend.SaveEndpointHealth(cmd.Context(), management.EndpointHealthRequest{ID: record.ID, Health: endpointHealthFromTestResult(result)}); err != nil {
				return err
			}
			if err := printJSON(cmd.OutOrStdout(), result); err != nil {
				return err
			}
			if failOnError && !result.OK {
				if strings.TrimSpace(result.Error) != "" {
					return fmt.Errorf("fluxplane-plugin: endpoint %q test failed: %s", record.ID, result.Error)
				}
				return fmt.Errorf("fluxplane-plugin: endpoint %q test failed", record.ID)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	cmd.Flags().BoolVar(&failOnError, "fail-on-error", true, "return a non-zero exit code when the endpoint test fails")
	return cmd
}

func newEndpointDoctorCommand(backend management.Backend) *cobra.Command {
	var instance string
	var failOnError bool
	cmd := &cobra.Command{
		Use:   "doctor [PRODUCT]",
		Short: "Test stored endpoints and update health",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			product := ""
			if len(args) == 1 {
				product = strings.TrimSpace(args[0])
			}
			result, err := doctorEndpoints(cmd.Context(), backend, instance, product)
			if err != nil {
				return err
			}
			if err := printJSON(cmd.OutOrStdout(), result); err != nil {
				return err
			}
			if failOnError && result.Failed > 0 {
				return fmt.Errorf("fluxplane-plugin: %d endpoint(s) failed health checks", result.Failed)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	cmd.Flags().BoolVar(&failOnError, "fail-on-error", true, "return a non-zero exit code when any endpoint test fails")
	return cmd
}

func newEndpointImportCommand(backend management.Backend) *cobra.Command {
	var from string
	var candidateIndex int
	var id string
	var source string
	var labelValues []string
	var annotationValues []string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "import [JSON|-]",
		Short: "Import a discovered endpoint candidate",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			raw, err := endpointImportInput(cmd.InOrStdin(), from, args)
			if err != nil {
				return err
			}
			candidate, err := endpointCandidateFromImport(raw, candidateIndex)
			if err != nil {
				return err
			}
			if strings.TrimSpace(id) != "" {
				candidate.ID = strings.TrimSpace(id)
			}
			if strings.TrimSpace(source) != "" {
				candidate.Source = strings.TrimSpace(source)
			}
			labels, err := parseMetadata(labelValues)
			if err != nil {
				return err
			}
			annotations, err := parseMetadata(annotationValues)
			if err != nil {
				return err
			}
			candidate.Labels = mergeStringMaps(candidate.Labels, labels)
			candidate.Annotations = mergeStringMaps(candidate.Annotations, annotations)
			result, err := backend.SaveEndpoint(cmd.Context(), management.EndpointSaveRequest{Endpoint: candidate.EndpointRef(), DryRun: dryRun})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "read candidate JSON from file")
	cmd.Flags().IntVar(&candidateIndex, "candidate", 0, "candidate index to import from discovery output")
	cmd.Flags().StringVar(&id, "id", "", "endpoint ID override")
	cmd.Flags().StringVar(&source, "source", "", "endpoint source override")
	cmd.Flags().StringArrayVar(&labelValues, "label", nil, "endpoint label key=value")
	cmd.Flags().StringArrayVar(&annotationValues, "annotation", nil, "endpoint annotation key=value")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate without writing")
	return cmd
}

func newEndpointRemoveCommand(backend management.Backend) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:     "remove ID",
		Aliases: []string{"rm", "delete"},
		Short:   "Remove a stored endpoint",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			result, err := backend.RemoveEndpoint(cmd.Context(), management.EndpointRemoveRequest{ID: args[0], DryRun: dryRun})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate without writing")
	return cmd
}

func newEndpointDiscoverCommand(backend management.Backend) *cobra.Command {
	var instance string
	var contextName string
	var namespace string
	var limit int
	var input string
	var inputFile string
	cmd := &cobra.Command{
		Use:   "discover PLUGIN[@VERSION] [PRODUCT]",
		Short: "Discover endpoint candidates",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			payload, err := readJSONPayload(input, inputFile)
			if err != nil {
				return err
			}
			product := ""
			if len(args) > 1 {
				product = args[1]
			}
			result, err := backend.DiscoverEndpoints(cmd.Context(), management.EndpointDiscoverRequest{
				Ref:       parseRef(args[0]),
				Instance:  instance,
				Product:   product,
				Context:   contextName,
				Namespace: namespace,
				Limit:     limit,
				Input:     payload,
			})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	cmd.Flags().StringVar(&contextName, "context", "", "discovery context")
	cmd.Flags().StringVar(&namespace, "namespace", "", "discovery namespace")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum endpoint candidates to return")
	cmd.Flags().StringVar(&input, "input", "", "endpoint discovery input JSON")
	cmd.Flags().StringVar(&inputFile, "input-file", "", "endpoint discovery input JSON file")
	return cmd
}

type endpointCandidateView struct {
	Index int `json:"index,omitempty"`
	fpendpoint.Candidate
}

type endpointDiscoveryPluginView struct {
	Candidates []endpointCandidateView `json:"candidates,omitempty"`
	Error      string                  `json:"error,omitempty"`
}

type endpointDiscoveryView struct {
	Product    string                                 `json:"product,omitempty"`
	Plugin     any                                    `json:"plugin,omitempty"`
	Candidates []endpointCandidateView                `json:"candidates,omitempty"`
	Results    map[string]endpointDiscoveryPluginView `json:"results,omitempty"`
	Result     json.RawMessage                        `json:"result,omitempty"`
}

func endpointImportInput(in io.Reader, from string, args []string) ([]byte, error) {
	if strings.TrimSpace(from) != "" {
		data, err := os.ReadFile(strings.TrimSpace(from))
		if err != nil {
			return nil, fmt.Errorf("fluxplane-plugin: read endpoint import file: %w", err)
		}
		return data, nil
	}
	if len(args) > 0 && strings.TrimSpace(args[0]) != "-" {
		return []byte(strings.TrimSpace(args[0])), nil
	}
	data, err := io.ReadAll(in)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func endpointCandidateFromImport(raw []byte, selectedIndex int) (fpendpoint.Candidate, error) {
	candidates, err := endpointCandidatesFromImport(raw)
	if err != nil {
		return fpendpoint.Candidate{}, err
	}
	if len(candidates) == 0 {
		return fpendpoint.Candidate{}, errors.New("fluxplane-plugin: endpoint import JSON did not contain candidates")
	}
	if selectedIndex > 0 {
		for i, candidate := range candidates {
			if candidate.Index == selectedIndex || i+1 == selectedIndex {
				return candidate.Candidate, nil
			}
		}
		return fpendpoint.Candidate{}, fmt.Errorf("fluxplane-plugin: candidate %d not found", selectedIndex)
	}
	if len(candidates) == 1 {
		return candidates[0].Candidate, nil
	}
	return fpendpoint.Candidate{}, errors.New("fluxplane-plugin: multiple candidates found; pass --candidate")
}

func endpointCandidatesFromImport(raw []byte) ([]endpointCandidateView, error) {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return nil, errors.New("fluxplane-plugin: endpoint import input is empty")
	}
	var single endpointCandidateView
	if err := json.Unmarshal(raw, &single); err == nil && strings.TrimSpace(single.URL) != "" {
		if single.Index == 0 {
			single.Index = 1
		}
		return []endpointCandidateView{single}, nil
	}
	var array []endpointCandidateView
	if err := json.Unmarshal(raw, &array); err == nil && len(array) > 0 {
		return normalizeEndpointCandidateIndexes(array), nil
	}
	var view endpointDiscoveryView
	if err := json.Unmarshal(raw, &view); err != nil {
		return nil, fmt.Errorf("fluxplane-plugin: endpoint import input must be JSON: %w", err)
	}
	if len(view.Result) > 0 {
		fromResult, err := endpointCandidatesFromImport(view.Result)
		if err == nil && len(fromResult) > 0 {
			return normalizeEndpointCandidateIndexes(fromResult), nil
		}
	}
	out := append([]endpointCandidateView(nil), view.Candidates...)
	if len(out) == 0 {
		for _, result := range view.Results {
			out = append(out, result.Candidates...)
		}
	}
	return normalizeEndpointCandidateIndexes(out), nil
}

func normalizeEndpointCandidateIndexes(in []endpointCandidateView) []endpointCandidateView {
	seen := map[int]bool{}
	for i := range in {
		if in[i].Index == 0 || seen[in[i].Index] {
			in[i].Index = i + 1
		}
		seen[in[i].Index] = true
	}
	return in
}

type endpointTestResult struct {
	ID         string         `json:"id"`
	URL        string         `json:"url,omitempty"`
	Product    string         `json:"product,omitempty"`
	Protocol   string         `json:"protocol,omitempty"`
	OK         bool           `json:"ok"`
	CheckedAt  time.Time      `json:"checked_at"`
	Method     string         `json:"method,omitempty"`
	DurationMS int64          `json:"duration_ms,omitempty"`
	Error      string         `json:"error,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
}

type endpointDoctorResult struct {
	Product   string               `json:"product,omitempty"`
	Count     int                  `json:"count"`
	OK        int                  `json:"ok"`
	Failed    int                  `json:"failed"`
	Endpoints []endpointTestResult `json:"endpoints,omitempty"`
}

func doctorEndpoints(ctx context.Context, backend management.Backend, instance, product string) (endpointDoctorResult, error) {
	listed, err := backend.ListEndpoints(ctx, management.EndpointListRequest{Product: product})
	if err != nil {
		return endpointDoctorResult{}, err
	}
	records := endpointRecordsFromList(listed)
	result := endpointDoctorResult{Product: product, Count: len(records)}
	for _, record := range records {
		testResult := testEndpoint(ctx, backend, instance, record)
		if _, err := backend.SaveEndpointHealth(ctx, management.EndpointHealthRequest{ID: record.ID, Health: endpointHealthFromTestResult(testResult)}); err != nil {
			return endpointDoctorResult{}, err
		}
		result.Endpoints = append(result.Endpoints, testResult)
		if testResult.OK {
			result.OK++
		} else {
			result.Failed++
		}
	}
	return result, nil
}

func endpointRecordsFromList(listed management.EndpointListResult) []fpendpoint.Record {
	if len(listed.Records) > 0 {
		return append([]fpendpoint.Record(nil), listed.Records...)
	}
	records := make([]fpendpoint.Record, 0, len(listed.Endpoints))
	for _, endpoint := range listed.Endpoints {
		records = append(records, fpendpoint.Record{EndpointRef: endpoint})
	}
	return records
}

func testEndpoint(ctx context.Context, backend management.Backend, instance string, endpoint fpendpoint.Record) endpointTestResult {
	if isKubernetesEndpoint(endpoint) {
		return testPluginEndpoint(ctx, backend, endpoint, management.Ref{Name: "kubernetes"}, instance, "kubernetes.cluster.test", map[string]any{"endpoint_ref": endpoint.ID}, "kubernetes cluster test failed")
	}
	if isSQLEndpoint(endpoint) {
		return testPluginEndpoint(ctx, backend, endpoint, management.Ref{Name: "sql"}, instance, "sql.query", map[string]any{
			"endpoint_ref": endpoint.ID,
			"query":        "select 1 as ok",
			"max_rows":     1,
		}, "sql endpoint test failed")
	}
	return testTCPEndpoint(ctx, endpoint)
}

func testPluginEndpoint(ctx context.Context, backend management.Backend, endpoint fpendpoint.Record, ref management.Ref, instance, operation string, input map[string]any, defaultError string) endpointTestResult {
	start := time.Now()
	result := endpointTestResult{
		ID:        endpoint.ID,
		URL:       redactEndpointURL(endpoint.URL),
		Product:   endpoint.Product,
		Protocol:  endpoint.Protocol,
		CheckedAt: time.Now().UTC(),
		Method:    operation,
	}
	inputRaw, err := json.Marshal(input)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	resp, err := backend.InvokeOperation(ctx, management.OperationInvokeRequest{
		Ref:       ref,
		Instance:  instance,
		Operation: operation,
		Input:     inputRaw,
	})
	result.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	var details map[string]any
	if len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, &details); err != nil {
			result.Error = fmt.Sprintf("%s: decode result: %v", defaultError, err)
			return result
		}
	}
	if details == nil {
		details = map[string]any{}
	}
	delete(details, "rows")
	if rawURL, _ := details["endpoint_url"].(string); rawURL != "" {
		details["endpoint_url"] = redactEndpointURL(rawURL)
	}
	result.OK = true
	result.Details = details
	return result
}

func testTCPEndpoint(ctx context.Context, endpoint fpendpoint.Record) endpointTestResult {
	start := time.Now()
	result := endpointTestResult{
		ID:        endpoint.ID,
		URL:       redactEndpointURL(endpoint.URL),
		Product:   endpoint.Product,
		Protocol:  endpoint.Protocol,
		CheckedAt: time.Now().UTC(),
		Method:    "tcp_connect",
	}
	hostPort, err := endpointHostPort(endpoint.URL)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", hostPort)
	result.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	_ = conn.Close()
	result.OK = true
	result.Details = map[string]any{"address": hostPort}
	return result
}

func endpointHealthFromTestResult(result endpointTestResult) fpendpoint.Health {
	return fpendpoint.Health{
		OK:         result.OK,
		CheckedAt:  result.CheckedAt,
		Method:     result.Method,
		DurationMS: result.DurationMS,
		Error:      result.Error,
		Details:    result.Details,
	}
}

func isKubernetesEndpoint(endpoint fpendpoint.Record) bool {
	values := []string{endpoint.Product, endpoint.Protocol}
	if parsed, err := url.Parse(endpoint.URL); err == nil {
		values = append(values, parsed.Scheme)
	}
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "kubernetes", "k8s", "kube", "cluster":
			return true
		}
	}
	return false
}

func isSQLEndpoint(endpoint fpendpoint.Record) bool {
	values := []string{endpoint.Product, endpoint.Protocol}
	if parsed, err := url.Parse(endpoint.URL); err == nil {
		values = append(values, parsed.Scheme)
	}
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "mysql", "mariadb", "postgres", "postgresql", "pg", "sqlite":
			return true
		}
	}
	return false
}

func endpointHostPort(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", err
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("endpoint url has no host")
	}
	if _, _, err := net.SplitHostPort(parsed.Host); err == nil {
		return parsed.Host, nil
	}
	port := defaultEndpointPort(parsed.Scheme)
	if port == "" {
		return "", fmt.Errorf("endpoint url has no port")
	}
	return net.JoinHostPort(parsed.Hostname(), port), nil
}

func defaultEndpointPort(scheme string) string {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "http":
		return "80"
	case "https":
		return "443"
	case "mysql", "mariadb":
		return "3306"
	case "postgres", "postgresql", "pg":
		return "5432"
	}
	return ""
}

func redactEndpointURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.User == nil {
		return rawURL
	}
	username := parsed.User.Username()
	if _, ok := parsed.User.Password(); ok {
		parsed.User = url.UserPassword(username, "xxxxx")
	} else {
		parsed.User = url.User(username)
	}
	return parsed.String()
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

package cli

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fluxplane/fluxplane-plugin/management"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
)

type describeVersions struct {
	Installed string `json:"installed,omitempty"`
	Pinned    string `json:"pinned,omitempty"`
	Previous  string `json:"previous,omitempty"`
	Manifest  string `json:"manifest,omitempty"`
}

type describeBinary struct {
	Path     string `json:"path,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Module   string `json:"module,omitempty"`
	Version  string `json:"version,omitempty"`
	DevBuild bool   `json:"dev_build,omitempty"`
}

type describeAuth struct {
	Connected bool                   `json:"connected"`
	Methods   []management.AuthState `json:"methods,omitempty"`
}

type describeOperations struct {
	Count    int                 `json:"count"`
	ReadOnly int                 `json:"read_only"`
	Groups   map[string][]string `json:"groups,omitempty"`
	Examples bool                `json:"examples"`
}

type describeEndpoint struct {
	ID      string `json:"id"`
	URL     string `json:"url,omitempty"`
	Product string `json:"product,omitempty"`
}

type describeDatasource struct {
	Name         string   `json:"name"`
	Entity       string   `json:"entity,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

type describeResult struct {
	Plugin      management.Ref       `json:"plugin"`
	Description string               `json:"description,omitempty"`
	Aliases     []string             `json:"aliases,omitempty"`
	Installed   bool                 `json:"installed"`
	Enabled     bool                 `json:"enabled"`
	Source      string               `json:"source,omitempty"`
	Versions    describeVersions     `json:"versions"`
	Binary      *describeBinary      `json:"binary,omitempty"`
	Auth        *describeAuth        `json:"auth,omitempty"`
	Endpoints   []describeEndpoint   `json:"endpoints,omitempty"`
	Operations  *describeOperations  `json:"operations,omitempty"`
	Datasources []describeDatasource `json:"datasources,omitempty"`
	Errors      map[string]string    `json:"errors,omitempty"`
}

func newDescribeCommand(backend management.Backend) *cobra.Command {
	var instance string
	cmd := &cobra.Command{
		Use:   "describe PLUGIN",
		Short: "Aggregate everything known about an installed plugin",
		Long: "One view across plugin state: install/enable status, versions (installed/pinned/previous), " +
			"binary provenance, auth state, saved endpoints, operation summary, and datasources. " +
			"Sections that fail (e.g. an unresponsive plugin binary) are reported under \"errors\" " +
			"without failing the whole command.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			result := describePlugin(cmd.Context(), backend, parseRef(args[0]), instance)
			return printJSON(cmd.OutOrStdout(), result)
		},
		ValidArgsFunction: pluginNameCompletion(backend),
	}
	cmd.Flags().StringVar(&instance, "instance", defaultInstance(), "plugin instance to inspect")
	return cmd
}

func describePlugin(ctx context.Context, backend management.Backend, ref management.Ref, instance string) describeResult {
	result := describeResult{Plugin: management.Ref{Name: ref.Name}, Errors: map[string]string{}}

	// Status first: it anchors identity and version bookkeeping.
	status, err := backend.PluginStatus(ctx, management.StatusRequest{Ref: ref})
	if err != nil {
		result.Errors["status"] = err.Error()
	}
	var record management.Plugin
	for _, plugin := range status.Plugins {
		if strings.EqualFold(plugin.Ref.Name, ref.Name) {
			record = plugin
			break
		}
	}
	result.Plugin = record.Ref
	if result.Plugin.Name == "" {
		result.Plugin = management.Ref{Name: ref.Name}
	}
	result.Installed = record.Installed
	result.Enabled = record.Enabled
	result.Source = record.Source
	result.Description = record.Description
	result.Versions = describeVersions{Installed: record.InstalledVersion, Pinned: record.Pinned, Previous: record.PreviousVersion}
	if path := strings.TrimSpace(record.Labels["installed_binary_path"]); path != "" {
		binary := describeBinary{Path: path, Kind: record.Labels["installed_binary_kind"]}
		if info, err := inspectBinary(ctx, path); err == nil {
			binary.Module = info.Module
			binary.Version = info.Version
			binary.DevBuild = info.dev()
		}
		result.Binary = &binary
	}

	var manifest sdkmanifest.PluginManifest
	sections := []func(){
		func() {
			raw, err := backend.PluginManifest(ctx, management.ManifestRequest{Ref: ref})
			if err != nil {
				result.Errors["manifest"] = err.Error()
				return
			}
			if err := json.Unmarshal(raw.Manifest, &manifest); err != nil {
				result.Errors["manifest"] = err.Error()
			}
		},
		func() {
			auth, err := backend.AuthStatus(ctx, management.AuthStatusRequest{Ref: ref, Instance: instance})
			if err != nil {
				result.Errors["auth"] = err.Error()
				return
			}
			state := describeAuth{Methods: auth.Auth}
			for _, method := range auth.Auth {
				if method.Connected {
					state.Connected = true
				}
			}
			result.Auth = &state
		},
		func() {
			ops, err := backend.ListOperations(ctx, management.OperationListRequest{Ref: ref, Instance: instance})
			if err != nil {
				result.Errors["operations"] = err.Error()
				return
			}
			summary := describeOperations{Count: len(ops.Operations), Groups: map[string][]string{}}
			for _, op := range ops.Operations {
				if op.ReadOnly {
					summary.ReadOnly++
				}
				group := op.Name
				if idx := strings.Index(group, "."); idx > 0 {
					group = group[:idx]
				}
				summary.Groups[group] = append(summary.Groups[group], op.Name)
				if !summary.Examples && len(parseOperationInputSchema(op).Examples) > 0 {
					summary.Examples = true
				}
			}
			for _, names := range summary.Groups {
				sort.Strings(names)
			}
			result.Operations = &summary
		},
		func() {
			datasources, err := backend.ListDatasources(ctx, management.DatasourceListRequest{Ref: ref, Instance: instance})
			if err != nil {
				result.Errors["datasources"] = err.Error()
				return
			}
			for _, spec := range datasources.Datasources {
				result.Datasources = append(result.Datasources, describeDatasource{
					Name:         string(spec.Name),
					Entity:       string(spec.Entity),
					Capabilities: spec.Capabilities,
				})
			}
		},
		func() {
			endpoints, err := backend.ListEndpoints(ctx, management.EndpointListRequest{})
			if err != nil {
				result.Errors["endpoints"] = err.Error()
				return
			}
			for _, record := range endpoints.Records {
				result.Endpoints = append(result.Endpoints, describeEndpoint{ID: record.ID, URL: record.URL, Product: record.Product})
			}
		},
	}
	runConcurrent(len(sections), 0, func(i int) { sections[i]() })

	result.Aliases = manifest.Aliases
	result.Versions.Manifest = manifest.Version
	if result.Description == "" {
		result.Description = manifest.Description
	}
	// Keep only endpoints whose product the plugin declares (or that carry the
	// plugin's name as product) — the endpoint store is shared across plugins.
	products := map[string]bool{strings.ToLower(ref.Name): true}
	for _, spec := range manifest.Endpoints {
		for _, product := range spec.Products {
			products[strings.ToLower(strings.TrimSpace(product))] = true
		}
	}
	filtered := result.Endpoints[:0]
	for _, endpoint := range result.Endpoints {
		if products[strings.ToLower(endpoint.Product)] {
			filtered = append(filtered, endpoint)
		}
	}
	result.Endpoints = filtered
	sort.Slice(result.Endpoints, func(i, j int) bool { return result.Endpoints[i].ID < result.Endpoints[j].ID })
	if len(result.Errors) == 0 {
		result.Errors = nil
	}
	return result
}

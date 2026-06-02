package pluginbinding

import (
	"strings"

	manifest "github.com/fluxplane/fluxplane-plugin/manifest"
	"github.com/fluxplane/fluxplane-plugin/protocol"
)

type PluginOption func(*Plugin)

func Define(spec ManifestSpec, options ...PluginOption) *Plugin {
	plugin := New(Manifest(spec))
	if text := authConnectText(plugin.manifest); text != "" {
		plugin.AuthConnectText(text)
	}
	for _, option := range options {
		if option != nil {
			option(plugin)
		}
	}
	return plugin
}

func WithSecretGetter(getter SecretGetter) PluginOption {
	return func(plugin *Plugin) {
		plugin.WithSecretGetter(getter)
	}
}

func WithAuthConnectText(text string) PluginOption {
	return func(plugin *Plugin) {
		plugin.AuthConnectText(text)
	}
}

func WithAuthTestOperation(name string) PluginOption {
	return func(plugin *Plugin) {
		plugin.AuthTestOperation(name)
	}
}

func WithHostManagedAuthTest(product string) PluginOption {
	return func(plugin *Plugin) {
		plugin.Command(protocol.CommandAuthTest, func(Context) protocol.Response {
			name := plugin.manifest.Name
			label := strings.TrimSpace(product)
			if label == "" {
				label = name
			}
			return OKText(label+" auth is host-managed; use dex auth status "+name, map[string]any{"status": "host_managed"})
		})
	}
}

func WithIndexBuildOperation(name string) PluginOption {
	return func(plugin *Plugin) {
		plugin.IndexBuildOperation(name)
	}
}

func WithHostOwnedIndexStatus(product string) PluginOption {
	return func(plugin *Plugin) {
		plugin.HostOwnedIndexStatus(product)
	}
}

func RegisterOperation[I any, O any](spec manifest.OperationSpec, handler OperationHandler[I, O]) PluginOption {
	return func(plugin *Plugin) {
		Operation(plugin, spec, handler)
	}
}

func RegisterDatasourceSearch[I any, O any](spec manifest.DatasourceSpec, handler DatasourceHandler[I, O]) PluginOption {
	return func(plugin *Plugin) {
		DatasourceHandlerFor(plugin, spec, CapabilitySearch, handler)
	}
}

func RegisterDatasourceLookup[I any, O any](spec manifest.DatasourceSpec, handler DatasourceHandler[I, O]) PluginOption {
	return func(plugin *Plugin) {
		DatasourceHandlerFor(plugin, spec, CapabilityLookup, handler)
	}
}

func RegisterDatasourceGet[I any, O any](spec manifest.DatasourceSpec, handler DatasourceHandler[I, O]) PluginOption {
	return func(plugin *Plugin) {
		DatasourceHandlerFor(plugin, spec, CapabilityGet, handler)
	}
}

func RegisterContextProvider(spec manifest.ContextSpec, handler ContextProviderHandler) PluginOption {
	return func(plugin *Plugin) {
		ContextProvider(plugin, spec, handler)
	}
}

func TypedOperationSpec[I any, O any](name, description string, options ...OperationSpecOption) manifest.OperationSpec {
	spec := OperationSpec(name, description, options...)
	if len(spec.Input) == 0 {
		spec.Input = MustSchemaFor[I]()
	}
	if len(spec.Output) == 0 {
		spec.Output = MustSchemaFor[O]()
	}
	return spec
}

func TypedDatasourceSpec[I any, O any](name, entity, description string, capabilities []string, options ...DatasourceSpecOption) manifest.DatasourceSpec {
	spec := Datasource(name, entity, description, capabilities...)
	for _, option := range options {
		option(&spec)
	}
	if len(spec.Input) == 0 {
		spec.Input = MustSchemaFor[I]()
	}
	if len(spec.Output) == 0 {
		spec.Output = MustSchemaFor[O]()
	}
	return NormalizeDatasourceSpec(spec)
}

func NotImplementedOperation[I any, O any](message string) OperationHandler[I, O] {
	return func(ctx Context, _ I) (O, error) {
		var zero O
		text := strings.TrimSpace(message)
		if text == "" {
			text = ctx.Call.Name + " is not implemented"
		} else if !strings.Contains(text, ctx.Call.Name) {
			text = ctx.Call.Name + " " + text
		}
		return zero, Fail("not_implemented", text)
	}
}

func authConnectText(manifest manifest.PluginManifest) string {
	var fields []string
	seen := map[string]bool{}
	for _, method := range manifest.Auth {
		for _, field := range method.Fields {
			name := strings.TrimSpace(field.Name)
			if name == "" || !field.Required || seen[name] {
				continue
			}
			seen[name] = true
			fields = append(fields, name)
		}
	}
	if len(fields) == 0 {
		return ""
	}
	parts := []string{"Use dex auth connect", manifest.Name}
	for _, field := range fields {
		parts = append(parts, "--field", field+"=<value>")
	}
	return strings.Join(parts, " ")
}

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/spf13/cobra"

	"github.com/fluxplane/fluxplane-plugin/management"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
)

// defaultSkillName is the skill identity used when no --name is supplied. It
// controls the SKILL.md front-matter name, the reference directory, and the
// ~/.claude/skills/<name> link target.
const defaultSkillName = "fluxplane-plugin"

// skillsDirEnv overrides the root directory under which skills are written and
// discovered for refresh. Defaults to ~/.fluxplane/skills.
const skillsDirEnv = "FLUXPLANE_PLUGIN_SKILLS_DIR"

// skillData is the template input for the generated agent skill.
type skillData struct {
	Name        string            `json:"name"`
	GeneratedAt string            `json:"generated_at"`
	Instance    string            `json:"instance"`
	Plugins     []skillPlugin     `json:"plugins"`
	Available   []availablePlugin `json:"available,omitempty"`
}

// skillPlugin describes one installed plugin (enabled or disabled) for the
// skill templates.
type skillPlugin struct {
	Name               string            `json:"name"`
	Version            string            `json:"version,omitempty"`
	Description        string            `json:"description,omitempty"`
	Reference          string            `json:"reference,omitempty"`
	ManifestError      string            `json:"manifest_error,omitempty"`
	Enabled            bool              `json:"enabled"`
	EnableExample      string            `json:"enable_example,omitempty"`
	HasAuth            bool              `json:"has_auth"`
	UsesEndpoints      bool              `json:"uses_endpoints,omitempty"`
	AuthConnected      bool              `json:"auth_connected"`
	AuthRequired       bool              `json:"auth_required"`
	AuthMethodsText    string            `json:"auth_methods_connected,omitempty"`
	RequiredAuthText   string            `json:"required_auth,omitempty"`
	ConnectAutoExample string            `json:"connect_auto_example,omitempty"`
	AuthStatusExample  string            `json:"auth_status_example,omitempty"`
	AuthTestExample    string            `json:"auth_test_example,omitempty"`
	AuthMethods        []skillAuthMethod `json:"auth_method_specs,omitempty"`
	Operations         []skillOperation  `json:"operations,omitempty"`
	Datasources        []skillDatasource `json:"datasources,omitempty"`
	Context            []skillContext    `json:"context,omitempty"`
}

// availablePlugin is a marketplace plugin that is not currently installed.
type availablePlugin struct {
	Name           string `json:"name"`
	Description    string `json:"description,omitempty"`
	InstallExample string `json:"install_example"`
	GoInstall      string `json:"go_install,omitempty"`
}

type skillAuthMethod struct {
	Name           string           `json:"name,omitempty"`
	Kind           string           `json:"kind,omitempty"`
	Description    string           `json:"description,omitempty"`
	EnvText        string           `json:"env,omitempty"`
	ConnectExample string           `json:"connect_example,omitempty"`
	Fields         []skillAuthField `json:"fields,omitempty"`
}

type skillAuthField struct {
	Name      string `json:"name"`
	Required  bool   `json:"required,omitempty"`
	Sensitive bool   `json:"sensitive,omitempty"`
	EnvText   string `json:"env,omitempty"`
}

type skillOperation struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	ReadOnly     bool   `json:"read_only,omitempty"`
	RequiredText string `json:"required,omitempty"`
	Example      string `json:"example"`
}

type skillDatasource struct {
	Name             string `json:"name"`
	Entity           string `json:"entity,omitempty"`
	Description      string `json:"description,omitempty"`
	CapabilitiesText string `json:"capabilities,omitempty"`
	SearchExample    string `json:"search_example,omitempty"`
}

type skillContext struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type skillInstallResult struct {
	Name       string   `json:"name"`
	Dir        string   `json:"dir"`
	Main       string   `json:"main"`
	References []string `json:"references,omitempty"`
	ClaudeLink string   `json:"claude_link,omitempty"`
	Linked     bool     `json:"linked,omitempty"`
}

type skillRefreshResult struct {
	Refreshed []string          `json:"refreshed,omitempty"`
	Errors    map[string]string `json:"errors,omitempty"`
}

func newSkillCommand(backend management.Backend) *cobra.Command {
	var (
		name         string
		instance     string
		templatePath string
	)
	cmd := &cobra.Command{
		Use:   "skill [OUTPUT.md]",
		Short: "Render an agent skill for installed and discoverable plugins",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			data, err := buildSkillData(cmd.Context(), backend, skillName(name), instance)
			if err != nil {
				return err
			}
			rendered, err := renderSkill("skill", combinedSkillTemplate, templatePath, data)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				_, err = fmt.Fprint(cmd.OutOrStdout(), rendered)
				return err
			}
			path := strings.TrimSpace(args[0])
			if path == "" {
				return fmt.Errorf("fluxplane-plugin: skill output path is empty")
			}
			if dir := filepath.Dir(path); dir != "." {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					return err
				}
			}
			return os.WriteFile(path, []byte(rendered), 0o600)
		},
	}
	cmd.Flags().StringVar(&name, "name", defaultSkillName, "skill name used in front matter")
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	cmd.Flags().StringVar(&templatePath, "template", "", "Go text/template file for the combined skill markdown")
	cmd.AddCommand(newSkillInstallCommand(backend))
	cmd.AddCommand(newSkillRefreshCommand(backend))
	return cmd
}

func newSkillInstallCommand(backend management.Backend) *cobra.Command {
	var (
		name           string
		outputDir      string
		instance       string
		mainTemplate   string
		pluginTemplate string
		noClaudeLink   bool
		references     bool
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Write SKILL.md (+ references) and link it into ~/.claude/skills",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			resolvedName := skillName(name)
			dir := strings.TrimSpace(outputDir)
			if dir == "" {
				resolved, err := defaultSkillDir(resolvedName)
				if err != nil {
					return err
				}
				dir = resolved
			}
			result, err := writeSkillInstall(cmd.Context(), backend, skillInstallOptions{
				name:           resolvedName,
				dir:            dir,
				instance:       instance,
				mainTemplate:   mainTemplate,
				pluginTemplate: pluginTemplate,
				references:     references,
				linkClaude:     !noClaudeLink,
			})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&name, "name", defaultSkillName, "skill name (front matter, reference dir, and ~/.claude/skills link)")
	cmd.Flags().StringVar(&outputDir, "output-dir", "", "skill output directory (default: ~/.fluxplane/skills/<name>)")
	cmd.Flags().StringVar(&instance, "instance", management.DefaultInstance, "plugin instance")
	cmd.Flags().StringVar(&mainTemplate, "template", "", "Go text/template file for SKILL.md")
	cmd.Flags().StringVar(&pluginTemplate, "plugin-template", "", "Go text/template file for reference pages")
	cmd.Flags().BoolVar(&noClaudeLink, "no-claude-link", false, "do not link ~/.claude/skills/<name> to the output directory")
	cmd.Flags().BoolVar(&references, "references", true, "write a reference page per plugin")
	return cmd
}

func newSkillRefreshCommand(backend management.Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Regenerate all installed skills in place (after upgrading plugins or the CLI)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), refreshInstalledSkills(cmd.Context(), backend))
		},
	}
	return cmd
}

func skillName(name string) string {
	if name = strings.TrimSpace(name); name != "" {
		return name
	}
	return defaultSkillName
}

func normalizeInstance(instance string) string {
	if instance = strings.TrimSpace(instance); instance != "" {
		return instance
	}
	return management.DefaultInstance
}

func buildSkillData(ctx context.Context, backend management.Backend, name, instance string) (skillData, error) {
	instance = normalizeInstance(instance)
	data := skillData{
		Name:        name,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Instance:    instance,
	}
	installed, err := backend.ListPlugins(ctx, management.ListRequest{All: true})
	if err != nil {
		return skillData{}, err
	}
	installedNames := map[string]bool{}
	for _, plugin := range installed {
		if !plugin.Installed {
			continue
		}
		installedNames[strings.TrimSpace(plugin.Ref.Name)] = true
		data.Plugins = append(data.Plugins, buildSkillPlugin(ctx, backend, instance, plugin))
	}
	// Discoverable-but-not-installed plugins from the marketplace catalog.
	// Best-effort: a registry error must not fail skill generation.
	if catalog, err := backend.SearchPlugins(ctx, management.SearchRequest{}); err == nil {
		for _, plugin := range catalog.Plugins {
			candidate := strings.TrimSpace(plugin.Ref.Name)
			if candidate == "" || plugin.Installed || installedNames[candidate] {
				continue
			}
			data.Available = append(data.Available, availablePlugin{
				Name:           candidate,
				Description:    strings.TrimSpace(plugin.Description),
				InstallExample: "fluxplane-plugin install " + candidate,
				GoInstall:      strings.TrimSpace(plugin.Labels["go_install"]),
			})
		}
	}
	sort.Slice(data.Plugins, func(i, j int) bool { return data.Plugins[i].Name < data.Plugins[j].Name })
	sort.Slice(data.Available, func(i, j int) bool { return data.Available[i].Name < data.Available[j].Name })
	return data, nil
}

func buildSkillPlugin(ctx context.Context, backend management.Backend, instance string, plugin management.Plugin) skillPlugin {
	name := strings.TrimSpace(plugin.Ref.Name)
	out := skillPlugin{
		Name:               name,
		Description:        strings.TrimSpace(plugin.Description),
		Reference:          filepath.ToSlash(filepath.Join("references", skillReferenceFileName(name))),
		Enabled:            plugin.Enabled,
		ConnectAutoExample: fmt.Sprintf("fluxplane-plugin auth connect auto %s", name),
		AuthStatusExample:  fmt.Sprintf("fluxplane-plugin auth status %s", name),
		AuthTestExample:    fmt.Sprintf("fluxplane-plugin auth test %s", name),
	}
	if !plugin.Enabled {
		out.EnableExample = fmt.Sprintf("fluxplane-plugin enable %s", name)
	}

	result, err := backend.PluginManifest(ctx, management.ManifestRequest{Ref: plugin.Ref})
	if err != nil {
		out.ManifestError = err.Error()
		return out
	}
	var m sdkmanifest.PluginManifest
	if err := json.Unmarshal(result.Manifest, &m); err != nil {
		out.ManifestError = "decode manifest: " + err.Error()
		return out
	}
	if manifestName := strings.TrimSpace(m.Name); manifestName != "" {
		out.Name = manifestName
	}
	if out.Description == "" {
		out.Description = strings.TrimSpace(m.Description)
	}
	out.Version = strings.TrimSpace(m.Version)

	var requiredAll []string
	for _, method := range m.Auth {
		methodName := strings.TrimSpace(method.Name)
		entry := skillAuthMethod{
			Name:        methodName,
			Kind:        strings.TrimSpace(string(method.Kind)),
			Description: strings.TrimSpace(method.Description),
			EnvText:     strings.Join(method.Env, ", "),
		}
		var fieldParts []string
		var requiredParts []string
		for _, field := range method.Fields {
			fieldName := strings.TrimSpace(field.Name)
			if fieldName == "" {
				continue
			}
			env := field.Env
			if len(env) == 0 {
				env = method.Env
			}
			entry.Fields = append(entry.Fields, skillAuthField{
				Name:      fieldName,
				Required:  field.Required,
				Sensitive: field.Sensitive || field.Secret,
				EnvText:   strings.Join(env, ", "),
			})
			fieldParts = append(fieldParts, fmt.Sprintf("--field %s=<value>", fieldName))
			if field.Required {
				requiredAll = append(requiredAll, fieldName)
				requiredParts = append(requiredParts, fmt.Sprintf("--field %s=<value>", fieldName))
			}
		}
		connect := "fluxplane-plugin auth connect " + name
		if methodName != "" {
			connect += " --method " + methodName
		}
		switch {
		case len(requiredParts) > 0:
			connect += " " + strings.Join(requiredParts, " ")
		case len(fieldParts) > 0:
			connect += " " + strings.Join(fieldParts, " ")
		}
		entry.ConnectExample = connect
		out.AuthMethods = append(out.AuthMethods, entry)
	}
	out.HasAuth = len(out.AuthMethods) > 0
	out.RequiredAuthText = strings.Join(dedupeSortedStrings(requiredAll), ", ")
	out.AuthRequired = len(requiredAll) > 0

	if status, err := backend.AuthStatus(ctx, management.AuthStatusRequest{Ref: plugin.Ref, Instance: instance}); err == nil {
		var methods []string
		for _, state := range status.Auth {
			if !state.Connected {
				continue
			}
			out.AuthConnected = true
			if method := strings.TrimSpace(state.Method); method != "" {
				methods = append(methods, method)
			}
		}
		out.AuthMethodsText = strings.Join(methods, ", ")
	}

	for _, op := range m.Operations {
		opName := strings.TrimSpace(op.Name)
		if opName == "" {
			continue
		}
		schema := parseOperationInputSchema(op)
		if _, ok := schema.Properties["endpoint_ref"]; ok {
			out.UsesEndpoints = true
		}
		out.Operations = append(out.Operations, skillOperation{
			Name:         opName,
			Description:  strings.TrimSpace(op.Description),
			ReadOnly:     op.ReadOnly,
			RequiredText: strings.Join(schema.Required, ", "),
			Example:      operationExample(out.Name, opName, schema),
		})
	}
	sort.Slice(out.Operations, func(i, j int) bool { return out.Operations[i].Name < out.Operations[j].Name })

	for _, ds := range m.Datasources {
		dsName := strings.TrimSpace(ds.Name)
		if dsName == "" {
			continue
		}
		entry := skillDatasource{
			Name:             dsName,
			Entity:           strings.TrimSpace(ds.Entity),
			Description:      strings.TrimSpace(ds.Description),
			CapabilitiesText: strings.Join(ds.Capabilities, ", "),
		}
		if hasCapability(ds.Capabilities, "search") {
			entry.SearchExample = datasourceSearchExample(out.Name, entry.Entity)
		}
		out.Datasources = append(out.Datasources, entry)
	}
	sort.Slice(out.Datasources, func(i, j int) bool { return out.Datasources[i].Name < out.Datasources[j].Name })

	for _, provider := range m.Context {
		providerName := strings.TrimSpace(string(provider.Name))
		if providerName == "" {
			continue
		}
		out.Context = append(out.Context, skillContext{Name: providerName, Description: strings.TrimSpace(provider.Description)})
	}
	sort.Slice(out.Context, func(i, j int) bool { return out.Context[i].Name < out.Context[j].Name })

	return out
}

func dedupeSortedStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range in {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func operationExample(plugin, operation string, schema operationInputSchema) string {
	return fmt.Sprintf("fluxplane-plugin operation invoke %s %s --input '%s'", plugin, operation, sampleInputJSON(schema))
}

func datasourceSearchExample(plugin, entity string) string {
	if entity != "" {
		return fmt.Sprintf("fluxplane-plugin datasource search %s --input '{\"entity\":\"%s\",\"query\":\"...\"}'", plugin, entity)
	}
	return fmt.Sprintf("fluxplane-plugin datasource search %s --input '{\"query\":\"...\"}'", plugin)
}

// operationInputSchema is the minimal JSON-schema shape needed to surface an
// operation's required input fields and field types in the skill.
type operationInputSchema struct {
	Required   []string                       `json:"required"`
	Properties map[string]operationInputField `json:"properties"`
	// Examples are JSON Schema example objects. When an operation declares one,
	// it is used verbatim for the skill's invocation example — the only way to
	// produce a runnable example for operations with one-of input requirements
	// (e.g. provide exactly one of transition_id / transition_name) that a flat
	// required list cannot express.
	Examples []map[string]any `json:"examples"`
}

type operationInputField struct {
	Type        any    `json:"type"`
	Description string `json:"description"`
}

func parseOperationInputSchema(op sdkmanifest.OperationSpec) operationInputSchema {
	var schema operationInputSchema
	_ = json.Unmarshal(op.Input, &schema)
	if schema.Properties == nil {
		schema.Properties = map[string]operationInputField{}
	}
	return schema
}

func sampleInputJSON(schema operationInputSchema) string {
	// A schema-declared example is authoritative: it is the only form that can
	// express one-of input requirements as a runnable invocation.
	for _, example := range schema.Examples {
		if len(example) > 0 {
			return compactJSON(example)
		}
	}
	obj := map[string]any{}
	for _, field := range schema.Required {
		obj[field] = samplePlaceholder(schemaFieldType(schema.Properties[field]))
	}
	// Many operations resolve a target instance from endpoint_ref even when the
	// advertised schema does not mark it required; surface it so examples work.
	if _, ok := schema.Properties["endpoint_ref"]; ok {
		if _, set := obj["endpoint_ref"]; !set {
			obj["endpoint_ref"] = "<endpoint_ref>"
		}
	}
	return compactJSON(obj)
}

// compactJSON marshals to compact JSON without HTML-escaping so placeholders
// like <endpoint_ref> render literally instead of as <.
func compactJSON(v any) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "{}"
	}
	return strings.TrimRight(b.String(), "\n")
}

func samplePlaceholder(fieldType string) any {
	switch fieldType {
	case "integer", "number":
		return 0
	case "boolean":
		return false
	case "array":
		return []any{}
	case "object":
		return map[string]any{}
	default:
		return ""
	}
}

func schemaFieldType(spec operationInputField) string {
	switch value := spec.Type.(type) {
	case string:
		return strings.TrimSpace(value)
	case []any:
		for _, item := range value {
			if text, ok := item.(string); ok && text != "null" {
				return strings.TrimSpace(text)
			}
		}
	}
	return "string"
}

type skillInstallOptions struct {
	name           string
	dir            string
	instance       string
	mainTemplate   string
	pluginTemplate string
	references     bool
	linkClaude     bool
}

func writeSkillInstall(ctx context.Context, backend management.Backend, opts skillInstallOptions) (skillInstallResult, error) {
	dir := strings.TrimSpace(opts.dir)
	if dir == "" {
		return skillInstallResult{}, fmt.Errorf("fluxplane-plugin: skill output dir is empty")
	}
	data, err := buildSkillData(ctx, backend, opts.name, opts.instance)
	if err != nil {
		return skillInstallResult{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return skillInstallResult{}, err
	}
	result := skillInstallResult{Name: opts.name, Dir: dir, Main: filepath.Join(dir, "SKILL.md")}
	main, err := renderSkill("skill-main", mainSkillTemplate, opts.mainTemplate, data)
	if err != nil {
		return result, err
	}
	if err := os.WriteFile(result.Main, []byte(main), 0o600); err != nil {
		return result, err
	}
	if opts.references {
		refsDir := filepath.Join(dir, "references")
		if err := os.MkdirAll(refsDir, 0o700); err != nil {
			return result, err
		}
		written := map[string]bool{}
		for _, plugin := range data.Plugins {
			rendered, err := renderSkill("skill-reference", pluginReferenceTemplate, opts.pluginTemplate, plugin)
			if err != nil {
				return result, err
			}
			fileName := skillReferenceFileName(plugin.Name)
			path := filepath.Join(refsDir, fileName)
			if err := os.WriteFile(path, []byte(rendered), 0o600); err != nil {
				return result, err
			}
			written[fileName] = true
			result.References = append(result.References, path)
		}
		// Prune references for plugins that are no longer installed so an
		// uninstalled plugin's usage detail vanishes (it remains discoverable
		// via the "available to install" list in SKILL.md).
		pruneStaleReferences(refsDir, written)
	}
	if opts.linkClaude {
		link, linked, err := linkClaudeSkill(opts.name, dir)
		if err != nil {
			return result, err
		}
		result.ClaudeLink = link
		result.Linked = linked
	}
	return result, nil
}

// pruneStaleReferences removes *.md files in refsDir that are not in the keep
// set. It only touches markdown files in that directory and is best-effort.
func pruneStaleReferences(refsDir string, keep map[string]bool) {
	entries, err := os.ReadDir(refsDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".md") || keep[name] {
			continue
		}
		_ = os.Remove(filepath.Join(refsDir, name))
	}
}

// refreshInstalledSkills regenerates every skill found under skillsRoot in
// place (SKILL.md + references), without re-linking. A missing root is a no-op.
// It never returns an error: per-skill failures are collected for reporting.
func refreshInstalledSkills(ctx context.Context, backend management.Backend) skillRefreshResult {
	res := skillRefreshResult{}
	root, err := skillsRoot()
	if err != nil {
		res.Errors = map[string]string{"": err.Error()}
		return res
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return res
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if info, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil || info.IsDir() {
			continue
		}
		if _, err := writeSkillInstall(ctx, backend, skillInstallOptions{
			name:       entry.Name(),
			dir:        dir,
			instance:   management.DefaultInstance,
			references: true,
			linkClaude: false,
		}); err != nil {
			if res.Errors == nil {
				res.Errors = map[string]string{}
			}
			res.Errors[dir] = err.Error()
			continue
		}
		res.Refreshed = append(res.Refreshed, dir)
	}
	return res
}

// afterStateChange regenerates installed skills following a successful,
// non-dry-run state change and reports the outcome on stderr. It never affects
// the host command's stdout result or exit status.
func afterStateChange(cmd *cobra.Command, backend management.Backend) {
	res := refreshInstalledSkills(cmd.Context(), backend)
	w := cmd.ErrOrStderr()
	for _, dir := range res.Refreshed {
		fmt.Fprintf(w, "skill: refreshed %s\n", dir)
	}
	for dir, msg := range res.Errors {
		fmt.Fprintf(w, "skill: refresh failed %s: %s\n", dir, msg)
	}
}

// skillStateChangingCommands are the command paths after which installed skills
// should be regenerated, so the skill always reflects plugin and auth changes.
var skillStateChangingCommands = map[string]bool{
	"fluxplane-plugin install":           true,
	"fluxplane-plugin update":            true,
	"fluxplane-plugin remove":            true,
	"fluxplane-plugin enable":            true,
	"fluxplane-plugin disable":           true,
	"fluxplane-plugin auth connect":      true,
	"fluxplane-plugin auth connect auto": true,
	"fluxplane-plugin auth test":         true,
	"fluxplane-plugin auth disconnect":   true,
	"fluxplane-plugin dev sync":          true,
}

// refreshSkillsAfterStateChange regenerates installed skills after a successful,
// non-dry-run state-changing command. Wired as the root PersistentPostRunE it
// runs only when RunE succeeded; refresh output goes to stderr and never affects
// the command's stdout result or exit status.
func refreshSkillsAfterStateChange(cmd *cobra.Command, backend management.Backend) {
	if backend == nil || !skillStateChangingCommands[cmd.CommandPath()] {
		return
	}
	if dry, err := cmd.Flags().GetBool("dry-run"); err == nil && dry {
		return
	}
	afterStateChange(cmd, backend)
}

func renderSkill(name, defaultSource, path string, data any) (string, error) {
	source := defaultSource
	if path = strings.TrimSpace(path); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		source = string(raw)
	}
	tmpl, err := template.New(name).Funcs(skillFuncs()).Parse(source)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	if err := tmpl.Execute(&out, data); err != nil {
		return "", err
	}
	return out.String(), nil
}

// skillFuncs exposes a single helper so templates can stay free of literal
// backticks (which a Go raw string literal cannot contain).
func skillFuncs() template.FuncMap {
	return template.FuncMap{
		"code": func(s string) string { return "`" + s + "`" },
	}
}

func skillsRoot() (string, error) {
	if override := strings.TrimSpace(os.Getenv(skillsDirEnv)); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".fluxplane", "skills"), nil
}

func defaultSkillDir(name string) (string, error) {
	root, err := skillsRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, name), nil
}

func skillReferenceFileName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var out strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			out.WriteRune(r)
		default:
			out.WriteRune('-')
		}
	}
	if strings.TrimSpace(out.String()) == "" {
		return "plugin.md"
	}
	return out.String() + ".md"
}

// linkClaudeSkill points ~/.claude/skills/<name> at the generated skill
// directory. It replaces an existing symlink but refuses to clobber a real
// directory. It is a no-op (no error) when ~/.claude is absent.
func linkClaudeSkill(name, target string) (string, bool, error) {
	target, err := filepath.Abs(strings.TrimSpace(target))
	if err != nil {
		return "", false, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	claudeDir := filepath.Join(home, ".claude")
	if stat, err := os.Stat(claudeDir); err != nil || !stat.IsDir() {
		return "", false, nil
	}
	skillsDir := filepath.Join(claudeDir, "skills")
	if err := os.MkdirAll(skillsDir, 0o700); err != nil {
		return "", false, err
	}
	link := filepath.Join(skillsDir, name)
	if info, err := os.Lstat(link); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return link, false, fmt.Errorf("%s exists and is not a symlink", link)
		}
		if current, err := os.Readlink(link); err == nil && current == target {
			return link, true, nil
		}
		if err := os.Remove(link); err != nil {
			return link, false, err
		}
	} else if !os.IsNotExist(err) {
		return link, false, err
	}
	if err := os.Symlink(target, link); err != nil {
		return link, false, err
	}
	return link, true, nil
}

const skillCheatSheet = `## Core commands

- {{code "fluxplane-plugin list"}} / {{code "fluxplane-plugin status"}} — installed plugins and instance state.
- {{code "fluxplane-plugin search"}} — discover plugins in the marketplace catalog.
- {{code "fluxplane-plugin install <plugin>"}} — install a discoverable plugin.
- {{code "fluxplane-plugin operation list <plugin>"}} — operations a plugin exposes (with input schema).
- {{code "fluxplane-plugin operation invoke <plugin> <operation> --input {...}"}} — call an operation; input is a JSON object.
- {{code "fluxplane-plugin datasource search-all <query>"}} — search every searchable datasource at once.
- {{code "fluxplane-plugin lookup <text-or-url>"}} — resolve a URL or name to a canonical record.
- {{code "fluxplane-plugin context build-all <query>"}} — gather context from context providers.
- {{code "fluxplane-plugin manifest <plugin>"}} — full plugin manifest.
- {{code "fluxplane-plugin auth status <plugin>"}} / {{code "fluxplane-plugin auth connect <plugin> --field <key>=<value>"}} — auth.
- {{code "fluxplane-plugin index build <plugin>"}} — refresh a plugin's local search index.

Run {{code "fluxplane-plugin <command> --help"}} for current flags. Output is JSON.`

const skillAuthWorkflow = `## Authenticating a plugin

Auth is per instance (default {{code "default"}}). For any installed plugin:

1. Check current state: {{code "fluxplane-plugin auth status <plugin>"}} (and {{code "fluxplane-plugin auth methods <plugin>"}} for accepted methods and fields).
2. Try automatic: {{code "fluxplane-plugin auth connect auto <plugin>"}} — imports the plugin's declared environment variables and reports which fields were saved and which are still missing.
3. Otherwise connect explicitly: {{code "fluxplane-plugin auth connect <plugin> --field <key>=<value>"}} — the plugin's reference page lists each required field and the env var names it accepts. Gather those values from the user when they are not already known.
4. Verify: {{code "fluxplane-plugin auth test <plugin>"}}.
5. Last resort: set the documented environment variables, then re-run step 2.`

const skillEndpointGuidance = `## Endpoints

Some plugins target a specific instance (a GitLab/Jira/Prometheus/... server) via an {{code "endpoint_ref"}}. If a call returns {{code "endpoint_ref is required"}}:

1. List configured endpoints: {{code "fluxplane-plugin endpoint list"}} — note the id you want.
2. Or discover them from a plugin: {{code "fluxplane-plugin endpoint discover <plugin> <product>"}}, then import a candidate with {{code "fluxplane-plugin endpoint import -"}}.
3. Set that id as the {{code "endpoint_ref"}} field in the input. The per-plugin operation examples include an {{code "<endpoint_ref>"}} placeholder where it applies.

Connecting auth often registers an endpoint automatically — check {{code "fluxplane-plugin endpoint list"}} first.`

const skillRuntimeNotes = `## Runtime notes

- Empty {{code "null"}}, {{code "[]"}}, or {{code "{}"}} responses can be valid; read command errors before retrying.
- Secret material in resolved output is redacted (commonly {{code "xxxxx"}}).
- This skill regenerates automatically when you install, update, remove, enable/disable, or connect/disconnect auth for a plugin. After upgrading the {{code "fluxplane-plugin"}} binary itself, run {{code "fluxplane-plugin skill refresh"}}.`

const combinedSkillTemplate = `---
name: {{ .Name }}
description: Access engineering integrations through the fluxplane-plugin CLI, locally installed plugins, and the discoverable marketplace.
---

# Engineering integrations via fluxplane-plugin

Use {{code "fluxplane-plugin"}} for plugin-backed access to engineering systems such as GitLab, Jira, Slack, Kubernetes, Prometheus, Loki, and SQL.

Generated: {{ .GeneratedAt }}
Instance: {{code .Instance}}

` + skillCheatSheet + `

` + skillAuthWorkflow + `

` + skillEndpointGuidance + `

## Installed plugins
{{- if not .Plugins }}

No plugins are installed yet. Install one from the catalog below, then authenticate it.
{{- end }}
{{- range .Plugins }}

### {{ .Name }}{{ if .Version }} ({{ .Version }}){{ end }} — {{ if .Enabled }}enabled{{ else }}disabled{{ end }}{{ if .ManifestError }}, manifest unavailable{{ else if not .HasAuth }}, no auth required{{ else if .AuthConnected }}, auth connected{{ else }}, needs auth{{ end }}
{{- if .Description }}

{{ .Description }}
{{- end }}
{{- if .EnableExample }}

Disabled — enable with {{ code .EnableExample }}.
{{- end }}
{{- if .ManifestError }}

Manifest unavailable when generated: {{ .ManifestError }}
{{- else }}
{{- if .HasAuth }}

Auth: {{ if .AuthConnected }}connected{{ if .AuthMethodsText }} ({{ .AuthMethodsText }}){{ end }}{{ else }}not connected — {{ code .ConnectAutoExample }}{{ if .RequiredAuthText }} or connect with required fields: {{ .RequiredAuthText }}{{ end }}{{ end }}.
{{- end }}
{{- if .Operations }}

Operations:
{{- range .Operations }}
- {{ code .Name }}{{ if .ReadOnly }} (read-only){{ end }}{{ if .Description }} — {{ .Description }}{{ end }}{{ if .RequiredText }} (required: {{ .RequiredText }}){{ end }}
  Example: {{ code .Example }}
{{- end }}
{{- end }}
{{- if .Datasources }}

Datasources:
{{- range .Datasources }}
- {{ code .Name }}{{ if .Entity }} ({{ code .Entity }}){{ end }}{{ if .CapabilitiesText }} — capabilities: {{ .CapabilitiesText }}{{ end }}
{{- end }}
{{- end }}
{{- if .Context }}

Context providers:
{{- range .Context }}
- {{ code .Name }}{{ if .Description }} — {{ .Description }}{{ end }}
{{- end }}
{{- end }}
{{- end }}
{{- end }}

## Available to install
{{- if not .Available }}

No additional plugins were found in the marketplace catalog.
{{- end }}
{{- range .Available }}
- {{ .Name }}{{ if .Description }} — {{ .Description }}{{ end }}
  Install: {{ code .InstallExample }}
{{- end }}

` + skillRuntimeNotes + `
`

const mainSkillTemplate = `---
name: {{ .Name }}
description: Access engineering integrations through the fluxplane-plugin CLI, locally installed plugins, and the discoverable marketplace.
---

# Engineering integrations via fluxplane-plugin

Use {{code "fluxplane-plugin"}} for plugin-backed access to engineering systems. See {{code "references/"}} for each installed integration's operations, datasources, and auth.

Generated: {{ .GeneratedAt }}
Instance: {{code .Instance}}

` + skillCheatSheet + `

` + skillAuthWorkflow + `

` + skillEndpointGuidance + `

## Installed integrations
{{ if .Plugins }}
{{- range .Plugins }}
- [{{ .Name }}]({{ .Reference }}){{ if .Description }} — {{ .Description }}{{ end }} ({{ if .Enabled }}enabled{{ else }}disabled{{ end }}; {{ if .ManifestError }}manifest unavailable{{ else if not .HasAuth }}no auth required{{ else if .AuthConnected }}auth connected{{ else }}needs auth{{ end }})
{{- end }}
{{ else }}
No plugins are installed yet. Install one from the catalog below, then authenticate it.
{{ end }}
## Available to install
{{ if .Available }}
{{- range .Available }}
- {{ .Name }}{{ if .Description }} — {{ .Description }}{{ end }}
  Install: {{ code .InstallExample }}
{{- end }}
{{ else }}
No additional plugins were found in the marketplace catalog.
{{ end }}
` + skillRuntimeNotes + `
`

const pluginReferenceTemplate = `# {{ .Name }}{{ if .Version }} ({{ .Version }}){{ end }}
{{ if .Description }}
{{ .Description }}
{{ end }}
Invoke through {{code "fluxplane-plugin"}}; output is JSON.
{{- if not .Enabled }}

Status: disabled — enable with {{ code .EnableExample }} before use.
{{- end }}
{{- if .ManifestError }}

Manifest was unavailable when this reference was generated: {{ .ManifestError }}

Install and authenticate the plugin, then re-run {{code "fluxplane-plugin skill refresh"}}.
{{- else }}

## Auth
{{ if .HasAuth }}
Status: {{ if .AuthConnected }}connected{{ if .AuthMethodsText }} (methods: {{ .AuthMethodsText }}){{ end }}{{ else }}not connected{{ end }}. Check with {{ code .AuthStatusExample }}; verify with {{ code .AuthTestExample }}.

Automatic (imports declared environment variables): {{ code .ConnectAutoExample }}

Methods:
{{- range .AuthMethods }}
- {{ if .Name }}{{ code .Name }}{{ else }}default{{ end }}{{ if .Kind }} ({{ .Kind }}){{ end }}{{ if .Description }} — {{ .Description }}{{ end }}
  Connect: {{ code .ConnectExample }}
{{- if .EnvText }}
  Env vars: {{ .EnvText }}
{{- end }}
{{- range .Fields }}
  - field {{ code .Name }}{{ if .Required }} (required){{ end }}{{ if .Sensitive }} (sensitive){{ end }}{{ if .EnvText }} — env: {{ .EnvText }}{{ end }}
{{- end }}
{{- end }}

Last resort: set the environment variables listed above, then run {{ code .ConnectAutoExample }}.
{{ else }}
No authentication required.
{{ end }}
## Operations
{{ if .UsesEndpoints }}Operations target an instance via {{code "endpoint_ref"}}; list ids with {{code "fluxplane-plugin endpoint list"}} and include the chosen one in the input.

{{ end }}{{ if .Operations }}
{{- range .Operations }}
### {{ .Name }}{{ if .ReadOnly }} (read-only){{ end }}
{{ if .Description }}
{{ .Description }}
{{ end }}{{ if .RequiredText }}Required input: {{ .RequiredText }}
{{ end }}Example: {{ code .Example }}
{{- end }}
{{ else }}
This plugin exposes no operations.
{{ end }}
## Datasources
{{ if .Datasources }}
{{- range .Datasources }}
- {{ code .Name }}{{ if .Entity }} ({{ code .Entity }}){{ end }}{{ if .CapabilitiesText }} — capabilities: {{ .CapabilitiesText }}{{ end }}{{ if .Description }}; {{ .Description }}{{ end }}
{{- if .SearchExample }}
  Example: {{ code .SearchExample }}
{{- end }}
{{- end }}

Search every datasource at once with {{code "fluxplane-plugin datasource search-all <query>"}}.
{{ else }}
This plugin exposes no datasources.
{{ end }}
{{- if .Context }}
## Context providers
{{ range .Context }}
- {{ code .Name }}{{ if .Description }} — {{ .Description }}{{ end }}
{{- end }}
{{- end }}
{{- end }}
`

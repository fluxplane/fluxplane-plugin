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

// binaryInfo is the provenance extracted from `go version -m <binary>`.
type binaryInfo struct {
	GoVersion string `json:"go_version,omitempty"`
	Module    string `json:"module,omitempty"`
	Version   string `json:"version,omitempty"`
	Revision  string `json:"revision,omitempty"`
	Modified  bool   `json:"modified,omitempty"`
}

// dev reports whether the binary was built from an unpublished or dirty tree —
// the silent-drift signal: a local `go build`, a `(devel)` pseudo-version, or a
// modified working tree at build time.
func (i binaryInfo) dev() bool {
	return i.Modified ||
		i.Version == "" ||
		i.Version == "(devel)" ||
		strings.Contains(i.Version, "+dirty") ||
		strings.Contains(i.Version, "devel")
}

// parseGoVersionMod extracts module provenance from `go version -m` output.
func parseGoVersionMod(out string) binaryInfo {
	var info binaryInfo
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "\t") {
			if idx := strings.Index(line, ": go"); idx >= 0 {
				info.GoVersion = strings.TrimSpace(line[idx+2:])
			}
			continue
		}
		fields := strings.Split(strings.TrimSpace(line), "\t")
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "mod":
			info.Module = fields[1]
			if len(fields) >= 3 {
				info.Version = fields[2]
			}
		case "build":
			switch {
			case fields[1] == "vcs.modified=true":
				info.Modified = true
			case strings.HasPrefix(fields[1], "vcs.revision="):
				info.Revision = strings.TrimPrefix(fields[1], "vcs.revision=")
			}
		}
	}
	return info
}

// inspectBinary runs `go version -m` and parses the result. Overridable in tests.
var inspectBinary = func(ctx context.Context, path string) (binaryInfo, error) {
	cmd := exec.CommandContext(ctx, "go", "version", "-m", path)
	out, err := cmd.Output()
	if err != nil {
		return binaryInfo{}, err
	}
	return parseGoVersionMod(string(out)), nil
}

// resolveLatestVersion queries the latest published version of a module.
// Overridable in tests.
var resolveLatestVersion = func(ctx context.Context, module string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-f", "{{.Version}}", module+"@latest")
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOPRIVATE=github.com/fluxplane/*", "GONOSUMDB=github.com/fluxplane/*")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

type doctorPluginReport struct {
	Plugin       management.Ref         `json:"plugin"`
	BinaryPath   string                 `json:"binary_path,omitempty"`
	BinaryExists bool                   `json:"binary_exists"`
	Binary       *binaryInfo            `json:"binary,omitempty"`
	Source       string                 `json:"source,omitempty"`
	Latest       string                 `json:"latest_version,omitempty"`
	Drift        bool                   `json:"drift,omitempty"`
	DevBuild     bool                   `json:"dev_build,omitempty"`
	Auth         []management.AuthState `json:"auth,omitempty"`
	Warnings     []string               `json:"warnings,omitempty"`
	OK           bool                   `json:"ok"`
}

type doctorResult struct {
	Healthy bool                 `json:"healthy"`
	Plugins []doctorPluginReport `json:"plugins"`
}

func newDoctorCommand(backend management.Backend) *cobra.Command {
	var checkLatest bool
	var checkAuth bool
	cmd := &cobra.Command{
		Use:   "doctor [plugin...]",
		Short: "Diagnose installed plugin binary provenance and auth health",
		Long: "Reports, per installed plugin, whether its binary is a published artifact or a drifted/dev " +
			"build (a local `go build`, a (devel) version, or a dirty tree at build time), and its stored " +
			"auth state.\n\n" +
			"--latest additionally resolves each module's newest published version and flags version drift " +
			"(network). --check-auth runs each plugin's live auth.test (network).",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			plugins, err := backend.ListPlugins(cmd.Context(), management.ListRequest{All: true})
			if err != nil {
				return err
			}
			want := map[string]bool{}
			for _, name := range args {
				if name = strings.TrimSpace(name); name != "" {
					want[name] = true
				}
			}
			var eligible []management.Plugin
			for _, plugin := range plugins {
				name := strings.TrimSpace(plugin.Ref.Name)
				if name == "" || (len(want) > 0 && !want[name]) {
					continue
				}
				eligible = append(eligible, plugin)
			}
			reports := make([]doctorPluginReport, len(eligible))
			runConcurrent(len(eligible), 0, func(i int) {
				reports[i] = diagnosePlugin(cmd.Context(), backend, eligible[i], checkLatest, checkAuth)
			})
			result := doctorResult{Healthy: true}
			for _, report := range reports {
				if !report.OK {
					result.Healthy = false
				}
				result.Plugins = append(result.Plugins, report)
			}
			sort.Slice(result.Plugins, func(i, j int) bool { return result.Plugins[i].Plugin.Name < result.Plugins[j].Plugin.Name })
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().BoolVar(&checkLatest, "latest", false, "resolve each module's newest published version and flag drift (network)")
	cmd.Flags().BoolVar(&checkAuth, "check-auth", false, "run each plugin's live auth.test (network)")
	return cmd
}

func diagnosePlugin(ctx context.Context, backend management.Backend, plugin management.Plugin, checkLatest, checkAuth bool) doctorPluginReport {
	report := doctorPluginReport{Plugin: plugin.Ref, OK: true}
	report.Source = strings.TrimSpace(plugin.Labels["go_install"])
	binPath := strings.TrimSpace(plugin.Labels["installed_binary_path"])
	report.BinaryPath = binPath

	if binPath == "" {
		report.Warnings = append(report.Warnings, "no installed binary path recorded (stdio/dev runtime?)")
	} else if _, err := os.Stat(binPath); err != nil {
		report.Warnings = append(report.Warnings, "installed binary is missing at "+binPath)
		report.OK = false
	} else {
		report.BinaryExists = true
		if info, err := inspectBinary(ctx, binPath); err == nil {
			report.Binary = &info
			report.DevBuild = info.dev()
			if report.DevBuild {
				report.Warnings = append(report.Warnings, "binary is a dev/dirty build, not a published artifact ("+versionLabel(info)+")")
				report.OK = false
			}
			if checkLatest && info.Module != "" {
				if latest, err := resolveLatestVersion(ctx, info.Module); err == nil && latest != "" {
					report.Latest = latest
					if !report.DevBuild && info.Version != latest {
						report.Drift = true
						report.OK = false
						report.Warnings = append(report.Warnings, "version drift: installed "+info.Version+", latest "+latest)
					}
				}
			}
		} else {
			report.Warnings = append(report.Warnings, "could not read binary provenance: "+err.Error())
		}
	}

	if status, err := backend.AuthStatus(ctx, management.AuthStatusRequest{Ref: plugin.Ref}); err == nil {
		report.Auth = status.Auth
		connected := false
		for _, a := range status.Auth {
			if a.Connected {
				connected = true
			}
		}
		if len(status.Auth) > 0 && !connected {
			report.Warnings = append(report.Warnings, "no connected auth method")
		}
	}

	if checkAuth {
		if res, err := backend.AuthTest(ctx, management.AuthTestRequest{Ref: plugin.Ref}); err != nil {
			report.Warnings = append(report.Warnings, "live auth test failed: "+err.Error())
			report.OK = false
		} else if !res.Connected {
			report.Warnings = append(report.Warnings, "live auth test reported not connected: "+res.Message)
			report.OK = false
		}
	}

	return report
}

func versionLabel(info binaryInfo) string {
	if strings.TrimSpace(info.Version) != "" {
		return info.Version
	}
	return "unknown version"
}

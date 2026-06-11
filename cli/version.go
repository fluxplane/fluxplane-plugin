package cli

import (
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

type versionInfo struct {
	Version  string `json:"version"`
	Module   string `json:"module,omitempty"`
	Revision string `json:"revision,omitempty"`
	Modified bool   `json:"modified,omitempty"`
	Go       string `json:"go,omitempty"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
}

// cliVersionInfo derives the CLI's own version from the binary's embedded
// module build info (the same stamp doctor reads from plugin binaries).
func cliVersionInfo() versionInfo {
	info := versionInfo{Version: "(devel)", Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH}
	build, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	info.Module = build.Main.Path
	if version := strings.TrimSpace(build.Main.Version); version != "" {
		info.Version = version
	}
	for _, setting := range build.Settings {
		switch setting.Key {
		case "vcs.revision":
			info.Revision = setting.Value
		case "vcs.modified":
			info.Modified = setting.Value == "true"
		}
	}
	return info
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the fluxplane-plugin CLI version (include this in bug reports)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return printJSON(cmd.OutOrStdout(), cliVersionInfo())
		},
	}
}

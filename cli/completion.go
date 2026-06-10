package cli

import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fluxplane/fluxplane-plugin/management"
)

// pluginNameCompletion completes installed plugin names from local state — a
// cheap state.json read, safe for shell completion.
func pluginNameCompletion(backend management.Backend) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if backend == nil || len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		plugins, err := backend.ListPlugins(cmd.Context(), management.ListRequest{All: true})
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var names []cobra.Completion
		for _, plugin := range plugins {
			name := strings.TrimSpace(plugin.Ref.Name)
			if name == "" || !strings.HasPrefix(name, toComplete) {
				continue
			}
			names = append(names, cobra.CompletionWithDesc(name, plugin.Description))
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

// wirePluginNameCompletion attaches plugin-name completion to every command
// whose first positional argument is a plugin name and that doesn't already
// define its own completion. Centralized so command constructors stay focused.
func wirePluginNameCompletion(root *cobra.Command, backend management.Backend) {
	complete := pluginNameCompletion(backend)
	operations := operationNameCompletion(backend)
	paths := []string{
		"install", "update", "remove", "enable", "disable", "status", "manifest", "run", "doctor", "selftest",
		"auth status", "auth methods", "auth connect", "auth test", "auth disconnect", "auth auto",
		"operation list", "operation search",
		"datasource list", "context list", "evidence list",
		"index build", "index status",
		"endpoint discover", "dev sync",
	}
	for _, path := range paths {
		if cmd := findCommand(root, strings.Fields(path)); cmd != nil && cmd.ValidArgsFunction == nil {
			cmd.ValidArgsFunction = complete
		}
	}
	if cmd := findCommand(root, []string{"operation", "describe"}); cmd != nil && cmd.ValidArgsFunction == nil {
		cmd.ValidArgsFunction = operations
	}
}

func findCommand(root *cobra.Command, path []string) *cobra.Command {
	current := root
	for _, name := range path {
		var next *cobra.Command
		for _, sub := range current.Commands() {
			if sub.Name() == name {
				next = sub
				break
			}
		}
		if next == nil {
			return nil
		}
		current = next
	}
	return current
}

// operationNameCompletion completes operation IDs for `operation invoke PLUGIN
// <op>`. Served from the backend's operations cache when the plugin binary is
// unchanged; bounded so a wedged plugin can never hang shell completion.
func operationNameCompletion(backend management.Backend) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		switch len(args) {
		case 0:
			return pluginNameCompletion(backend)(cmd, args, toComplete)
		case 1:
		default:
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		if backend == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Second)
		defer cancel()
		ops, err := backend.ListOperations(ctx, management.OperationListRequest{Ref: parseRef(args[0])})
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var ids []cobra.Completion
		for _, op := range ops.Operations {
			name := strings.TrimSpace(op.Name)
			if name == "" || !strings.HasPrefix(name, toComplete) {
				continue
			}
			ids = append(ids, cobra.CompletionWithDesc(name, op.Description))
		}
		return ids, cobra.ShellCompDirectiveNoFileComp
	}
}

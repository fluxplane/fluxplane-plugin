package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fluxplane/fluxplane-plugin/management"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
)

type operationMatch struct {
	Plugin       string                  `json:"plugin"`
	Operation    string                  `json:"operation"`
	Description  string                  `json:"description,omitempty"`
	Required     []string                `json:"required,omitempty"`
	ReadOnly     bool                    `json:"read_only,omitempty"`
	MatchedTerms int                     `json:"matched_terms,omitempty"`
	Fields       []operationFieldSummary `json:"input_fields,omitempty"`
	Example      string                  `json:"example,omitempty"`
	score        int
}

type operationSearchResult struct {
	Query   string            `json:"query"`
	Matches []operationMatch  `json:"matches"`
	Errors  map[string]string `json:"errors,omitempty"`
}

func newOperationSearchCommand(backend management.Backend) *cobra.Command {
	var instance string
	var pluginFilter []string
	var limit int
	var readOnlyOnly bool
	var asJSON bool
	var full bool
	cmd := &cobra.Command{
		Use:   "search QUERY",
		Short: "Find operations across installed plugins by name and description",
		Long: "Searches installed, enabled plugins for operations whose name or description match ANY " +
			"of the whitespace-separated terms, ranked best-first (name hits and full term coverage " +
			"rank higher; matched_terms shows partial matches). Use --plugin to restrict which " +
			"plugins are queried, --read-only to list only read-only operations.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			query := strings.TrimSpace(strings.Join(args, " "))
			plugins, err := backend.ListPlugins(cmd.Context(), management.ListRequest{All: true})
			if err != nil {
				return err
			}
			want := map[string]bool{}
			for _, name := range pluginFilter {
				if name = strings.TrimSpace(name); name != "" {
					want[name] = true
				}
			}
			var eligible []management.Plugin
			for _, plugin := range plugins {
				name := strings.TrimSpace(plugin.Ref.Name)
				if name == "" || !plugin.Installed || !plugin.Enabled {
					continue
				}
				if len(want) > 0 && !want[name] {
					continue
				}
				eligible = append(eligible, plugin)
			}
			// Fan out across plugins concurrently; each writes only its own slot.
			perMatches := make([][]operationMatch, len(eligible))
			perErr := make([]string, len(eligible))
			runConcurrent(len(eligible), 0, func(i int) {
				plugin := eligible[i]
				list, err := backend.ListOperations(cmd.Context(), management.OperationListRequest{Ref: plugin.Ref, Instance: instance})
				if err != nil {
					perErr[i] = err.Error()
					return
				}
				perMatches[i] = rankOperationMatches(query, strings.TrimSpace(plugin.Ref.Name), list.Operations, readOnlyOnly, full)
			})
			result := operationSearchResult{Query: query, Errors: map[string]string{}}
			for i, plugin := range eligible {
				if perErr[i] != "" {
					result.Errors[strings.TrimSpace(plugin.Ref.Name)] = perErr[i]
					continue
				}
				result.Matches = append(result.Matches, perMatches[i]...)
			}
			sort.SliceStable(result.Matches, func(i, j int) bool {
				if result.Matches[i].score != result.Matches[j].score {
					return result.Matches[i].score > result.Matches[j].score
				}
				if result.Matches[i].Plugin != result.Matches[j].Plugin {
					return result.Matches[i].Plugin < result.Matches[j].Plugin
				}
				return result.Matches[i].Operation < result.Matches[j].Operation
			})
			if limit > 0 && len(result.Matches) > limit {
				result.Matches = result.Matches[:limit]
			}
			if len(result.Errors) == 0 {
				result.Errors = nil
			}
			if asJSON {
				return printJSON(cmd.OutOrStdout(), result)
			}
			return renderOperationMatches(cmd, result)
		},
	}
	cmd.Flags().StringVar(&instance, "instance", defaultInstance(), "plugin instance")
	cmd.Flags().StringArrayVar(&pluginFilter, "plugin", nil, "restrict the search to these plugins (repeatable)")
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum matches to return")
	cmd.Flags().BoolVar(&readOnlyOnly, "read-only", false, "only list read-only operations")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit matches as JSON")
	cmd.Flags().BoolVar(&full, "full", false, "include each match's input fields and a runnable example (invoke without a separate describe)")
	return cmd
}

// rankOperationMatches scores each operation against the query. Every
// whitespace-separated term must appear somewhere in name+description, else the
// operation is dropped. Name hits rank above description hits.
func rankOperationMatches(query, plugin string, ops []sdkmanifest.OperationSpec, readOnlyOnly, full bool) []operationMatch {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return nil
	}
	var out []operationMatch
	for _, op := range ops {
		name := strings.TrimSpace(op.Name)
		if name == "" || (readOnlyOnly && !op.ReadOnly) {
			continue
		}
		lowerName := strings.ToLower(name)
		lowerDesc := strings.ToLower(strings.TrimSpace(op.Description))
		// OR semantics: any matching term keeps the operation; ranking rewards
		// name hits over description hits and full term coverage over partial,
		// so not knowing the plugin's exact noun still finds the operation.
		matched := 0
		score := 0
		for _, term := range terms {
			switch {
			case strings.Contains(lowerName, term):
				matched++
				score += 15
			case strings.Contains(lowerDesc, term):
				matched++
				score += 5
			}
		}
		if matched == 0 {
			continue
		}
		if matched == len(terms) {
			score += 20
		}
		joined := strings.Join(terms, " ")
		switch {
		case lowerName == joined:
			score = 100
		case strings.HasPrefix(lowerName, joined):
			score = max(score, 60)
		case strings.Contains(lowerName, joined):
			score = max(score, 40)
		}
		schema := parseOperationInputSchema(op)
		match := operationMatch{
			Plugin:       plugin,
			Operation:    name,
			Description:  strings.TrimSpace(op.Description),
			Required:     schema.Required,
			ReadOnly:     op.ReadOnly,
			MatchedTerms: matched,
			score:        score,
		}
		if full {
			// Fold in enough to invoke without a separate `describe` round-trip.
			match.Fields = summarizeOperationInput(schema)
			match.Example = operationExample(plugin, name, schema)
		}
		out = append(out, match)
	}
	return out
}

func renderOperationMatches(cmd *cobra.Command, result operationSearchResult) error {
	w := cmd.OutOrStdout()
	if len(result.Matches) == 0 {
		fmt.Fprintf(w, "no operations match %q\n", result.Query)
	}
	for _, m := range result.Matches {
		line := m.Plugin + "/" + m.Operation
		if m.ReadOnly {
			line += " (read-only)"
		}
		if m.Description != "" {
			line += " — " + m.Description
		}
		if len(m.Required) > 0 {
			line += "  [required: " + strings.Join(m.Required, ", ") + "]"
		}
		fmt.Fprintln(w, line)
		if m.Example != "" {
			fmt.Fprintln(w, "    example: "+m.Example)
		}
	}
	for plugin, msg := range result.Errors {
		fmt.Fprintf(cmd.ErrOrStderr(), "search: %s: %s\n", plugin, msg)
	}
	return nil
}

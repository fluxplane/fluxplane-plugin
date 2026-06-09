package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/fluxplane/fluxplane-plugin/management"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
)

func TestRankOperationMatches(t *testing.T) {
	ops := []sdkmanifest.OperationSpec{
		{Name: "jira.issue.comment.add", Description: "Add a comment to an issue."},
		{Name: "jira.issue.comment.list", Description: "List comments.", ReadOnly: true},
		{Name: "jira.issue.create", Description: "Create an issue."},
	}
	matches := rankOperationMatches("comment", "jira", ops, false)
	if len(matches) != 2 {
		t.Fatalf("expected 2 comment matches, got %d: %#v", len(matches), matches)
	}

	// read-only filter
	ro := rankOperationMatches("comment", "jira", ops, true)
	if len(ro) != 1 || ro[0].Operation != "jira.issue.comment.list" {
		t.Fatalf("read-only filter = %#v", ro)
	}

	// multi-term AND: both terms must appear
	multi := rankOperationMatches("create issue", "jira", ops, false)
	if len(multi) != 1 || multi[0].Operation != "jira.issue.create" {
		t.Fatalf("multi-term = %#v", multi)
	}

	// no match
	if m := rankOperationMatches("kubernetes", "jira", ops, false); len(m) != 0 {
		t.Fatalf("expected no match, got %#v", m)
	}
}

// searchBackend serves different ops per plugin and records which refs were queried.
type searchBackend struct {
	*fakeBackend
	byPlugin map[string][]sdkmanifest.OperationSpec
}

func (b *searchBackend) ListPlugins(context.Context, management.ListRequest) ([]management.Plugin, error) {
	return []management.Plugin{
		{Ref: management.Ref{Name: "jira"}, Installed: true, Enabled: true},
		{Ref: management.Ref{Name: "gitlab"}, Installed: true, Enabled: true},
		{Ref: management.Ref{Name: "disabled"}, Installed: true, Enabled: false},
	}, nil
}

func (b *searchBackend) ListOperations(_ context.Context, req management.OperationListRequest) (management.OperationListResult, error) {
	b.fakeBackend.listOpsReqs = append(b.fakeBackend.listOpsReqs, req)
	return management.OperationListResult{Plugin: req.Ref, Operations: b.byPlugin[req.Ref.Name]}, nil
}

func TestOperationSearchAcrossPlugins(t *testing.T) {
	backend := &searchBackend{
		fakeBackend: &fakeBackend{},
		byPlugin: map[string][]sdkmanifest.OperationSpec{
			"jira":   {{Name: "jira.issue.comment.add", Description: "Add a comment."}},
			"gitlab": {{Name: "gitlab.note.create", Description: "Create a comment note."}},
		},
	}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"operation", "search", "comment", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("search: %v", err)
	}
	var res operationSearchResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if len(res.Matches) != 2 {
		t.Fatalf("expected 2 matches across plugins, got %#v", res.Matches)
	}
	// disabled plugin must not be queried
	for _, r := range backend.fakeBackend.listOpsReqs {
		if r.Ref.Name == "disabled" {
			t.Fatalf("disabled plugin should not be queried")
		}
	}
}

func TestOperationSearchPluginFilter(t *testing.T) {
	backend := &searchBackend{
		fakeBackend: &fakeBackend{},
		byPlugin: map[string][]sdkmanifest.OperationSpec{
			"jira":   {{Name: "jira.issue.comment.add", Description: "Add a comment."}},
			"gitlab": {{Name: "gitlab.note.create", Description: "Create a comment note."}},
		},
	}
	cmd := New(Options{Backend: backend, Out: &bytes.Buffer{}})
	cmd.SetArgs([]string{"operation", "search", "comment", "--plugin", "jira"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, r := range backend.fakeBackend.listOpsReqs {
		if r.Ref.Name != "jira" {
			t.Fatalf("--plugin jira should only query jira, got %q", r.Ref.Name)
		}
	}
	if len(backend.fakeBackend.listOpsReqs) != 1 {
		t.Fatalf("expected exactly one plugin queried, got %d", len(backend.fakeBackend.listOpsReqs))
	}
}

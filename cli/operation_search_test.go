package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
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
	matches := rankOperationMatches("comment", "jira", ops, false, false)
	if len(matches) != 2 {
		t.Fatalf("expected 2 comment matches, got %d: %#v", len(matches), matches)
	}

	// read-only filter
	ro := rankOperationMatches("comment", "jira", ops, true, false)
	if len(ro) != 1 || ro[0].Operation != "jira.issue.comment.list" {
		t.Fatalf("read-only filter = %#v", ro)
	}

	// multi-term OR with ranking: every issue-op matches, but the operation
	// covering both terms ranks first with full matched_terms.
	multi := rankOperationMatches("create issue", "jira", ops, false, false)
	if len(multi) != 3 {
		t.Fatalf("multi-term = %#v, want all issue ops", multi)
	}
	sort.SliceStable(multi, func(i, j int) bool { return multi[i].score > multi[j].score })
	if multi[0].Operation != "jira.issue.create" || multi[0].MatchedTerms != 2 {
		t.Fatalf("multi-term ranking = %#v", multi)
	}

	// no match
	if m := rankOperationMatches("kubernetes", "jira", ops, false, false); len(m) != 0 {
		t.Fatalf("expected no match, got %#v", m)
	}
}

func TestRankOperationMatchesFull(t *testing.T) {
	ops := []sdkmanifest.OperationSpec{{
		Name:        "jira.issue.comment.add",
		Description: "Add a comment.",
		Input:       json.RawMessage(`{"required":["key","body_markdown"],"properties":{"key":{"type":"string"},"body_markdown":{"type":"string"}}}`),
	}}
	// Without --full: no fields/example.
	lean := rankOperationMatches("comment", "jira", ops, false, false)
	if len(lean) != 1 || len(lean[0].Fields) != 0 || lean[0].Example != "" {
		t.Fatalf("lean match should omit fields/example: %#v", lean[0])
	}
	// With --full: fields + runnable example folded in.
	full := rankOperationMatches("comment", "jira", ops, false, true)
	if len(full) != 1 || len(full[0].Fields) != 2 || full[0].Example == "" {
		t.Fatalf("full match should include fields + example: %#v", full[0])
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
	b.fakeBackend.recordListOps(req)
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

func TestOperationSearchRanksPartialTermMatches(t *testing.T) {
	backend := &searchBackend{
		fakeBackend: &fakeBackend{},
		byPlugin: map[string][]sdkmanifest.OperationSpec{
			"jira": {
				{Name: "slack.thread", Description: "View a Slack thread."},
				{Name: "slack.message.list", Description: "Read recent messages from a Slack channel."},
			},
		},
	}
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	// The report's exact failing query: only one of three terms appears in
	// the operation — OR semantics must still surface it, ranked first.
	cmd.SetArgs([]string{"operation", "search", "thread", "replies", "history", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("search: %v", err)
	}
	var res operationSearchResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if len(res.Matches) == 0 || res.Matches[0].Operation != "slack.thread" {
		t.Fatalf("matches = %#v, want slack.thread first", res.Matches)
	}
	if res.Matches[0].MatchedTerms != 1 {
		t.Fatalf("matched_terms = %d, want 1", res.Matches[0].MatchedTerms)
	}
}

package nameguess

import (
	"strings"
	"testing"
)

func TestNearestFieldNames(t *testing.T) {
	fields := []string{"key", "id", "jql", "project", "status", "fields", "order_by", "limit"}
	cases := map[string]string{
		"issue_key":   "key",   // token containment
		"isue_key":    "key",   // token containment despite typo'd sibling token
		"project_key": "key",   // overlapping token wins over distant names
		"projet":      "project", // plain typo
		"statu":       "status",
		"order":       "order_by",
	}
	for got, want := range cases {
		if best := Nearest(got, fields); best != want {
			t.Fatalf("Nearest(%q) = %q, want %q", got, best, want)
		}
	}
	// Semantically-related-but-textually-unrelated names must NOT produce a
	// confident wrong guess: the caller falls back to listing valid fields.
	if best := Nearest("max_results", []string{"jql", "project", "limit"}); best != "" {
		t.Fatalf("Nearest(max_results) = %q, want no suggestion", best)
	}
}

func TestNearestOperationNames(t *testing.T) {
	ops := []string{
		"jira.issue.show", "jira.issue.list", "jira.issue.create",
		"jira.issue.comment.add", "jira.test", "jira.project.list",
	}
	cases := map[string]string{
		"jira.issue.get":  "jira.issue.show", // verb synonym
		"jira.issue.shwo": "jira.issue.show", // typo
		"issue.show":      "jira.issue.show", // missing plugin prefix
	}
	for got, want := range cases {
		if best := Nearest(got, ops); best != want {
			t.Fatalf("Nearest(%q) = %q, want %q", got, best, want)
		}
	}
}

func TestCloseMatchesBoundedAndDeterministic(t *testing.T) {
	ops := []string{"loki.query", "loki.labels", "loki.recent_logs", "loki.test"}
	matches := CloseMatches("loki.health", ops, 3)
	if len(matches) == 0 || len(matches) > 3 {
		t.Fatalf("matches = %v", matches)
	}
	again := CloseMatches("loki.health", ops, 3)
	if strings.Join(matches, ",") != strings.Join(again, ",") {
		t.Fatalf("non-deterministic: %v vs %v", matches, again)
	}
}

func TestNoSuggestionForUnrelatedNames(t *testing.T) {
	if best := Nearest("zzzz", []string{"key", "project"}); best != "" {
		t.Fatalf("Nearest(zzzz) = %q, want none", best)
	}
	if got := CloseMatches("", []string{"a"}, 3); got != nil {
		t.Fatalf("CloseMatches with empty input = %v", got)
	}
}

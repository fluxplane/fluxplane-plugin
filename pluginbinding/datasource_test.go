package pluginbinding

import (
	"strings"
	"testing"
)

func TestLookupMatchesScoresSortsDedupesAndLimits(t *testing.T) {
	source := LookupSource{Source: "live", Plugin: "test", Instance: "work", Index: "test.users"}
	candidates := []LookupCandidate{
		NewLookupCandidate(source, "test.user", "u2", map[string]string{"id": "u2"}, map[string]string{"id": "u2", "record.name": "Other"}),
		NewLookupCandidate(source, "test.user", "u1", map[string]string{"id": "u1"}, map[string]string{"id": "u1", "record.name": "Timo Friedl"}),
		NewExactLookupCandidate(source, "test.user", "u1", 1200, []string{"id"}, map[string]string{"id": "u1"}, map[string]string{"id": "u1", "record.name": "Timo Friedl"}),
	}

	matches := LookupMatches(DatasourceLookupInput{Text: "ask timo", Limit: 1}, candidates)
	if len(matches) != 1 {
		t.Fatalf("matches = %#v", matches)
	}
	if matches[0].ID != "u1" || matches[0].Score != 1200 || !containsField(matches[0].MatchedFields, "record.name") || !containsField(matches[0].MatchedFields, "id") {
		t.Fatalf("match = %#v", matches[0])
	}
	if matches[0].Source.Plugin != "test" || matches[0].Source.Instance != "work" || matches[0].Source.Index != "test.users" {
		t.Fatalf("source = %#v", matches[0].Source)
	}
}

func TestDatasourceLookupResultFromCandidatesUsesExactCandidate(t *testing.T) {
	source := LookupSource{Source: "live", Plugin: "test", Instance: "work", Index: "test.items"}
	result := NewDatasourceLookupResultFromCandidates("test", DatasourceLookupInput{Text: "external ref"}, []LookupCandidate{
		NewExactLookupCandidate(source, "test.item", "i1", 1200, []string{"provider.ref"}, map[string]string{"id": "i1"}, nil),
	})
	if result.Source != "test" || result.Count != 1 || result.Matches[0].ID != "i1" {
		t.Fatalf("result = %#v", result)
	}
	if result.Matches[0].MatchedFields[0] != "provider.ref" {
		t.Fatalf("fields = %#v", result.Matches[0].MatchedFields)
	}
}

func TestLookupLimitAndFilteredTerms(t *testing.T) {
	input := DatasourceLookupInput{Text: "open https://example.com/group/project and timo", Limit: 500}
	if got := LookupLimit(input, 20, 100); got != 100 {
		t.Fatalf("limit = %d", got)
	}
	terms := FilterLookupTerms(input, 2, func(term string) bool {
		return !strings.Contains(term, "://")
	})
	if len(terms) != 1 || terms[0] != "timo" {
		t.Fatalf("terms = %#v", terms)
	}
}

func containsField(fields []string, candidate string) bool {
	for _, field := range fields {
		if field == candidate {
			return true
		}
	}
	return false
}

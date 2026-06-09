package pluginbinding

import (
	"strings"
	"testing"
)

func TestVerifyAppliedWarning(t *testing.T) {
	// All requested fields stuck -> no warning.
	if w := VerifyAppliedWarning(
		FieldCheck{Field: "summary", Requested: "Fix bug", Applied: "Fix bug"},
		FieldCheck{Field: "parent", Requested: "EPIC-1", Applied: "epic-1", IgnoreCase: true},
		FieldCheck{Field: "assignee", Requested: "", Applied: ""}, // not requested
	); w != "" {
		t.Fatalf("expected no warning, got %q", w)
	}

	// A dropped field -> named warning.
	w := VerifyAppliedWarning(
		FieldCheck{Field: "parent", Requested: "EPIC-1", Applied: "", IgnoreCase: true},
		FieldCheck{Field: "summary", Requested: "New", Applied: "New"},
	)
	if !strings.Contains(w, "parent") || !strings.Contains(w, "EPIC-1") || !strings.Contains(w, "(unset)") {
		t.Fatalf("warning = %q", w)
	}
	if strings.Contains(w, "summary") {
		t.Fatalf("applied field should not be reported: %q", w)
	}
}

func TestUnappliedFieldsSortedAndCaseSensitive(t *testing.T) {
	got := UnappliedFields(
		FieldCheck{Field: "summary", Requested: "A", Applied: "a"},                       // case-sensitive mismatch
		FieldCheck{Field: "assignee", Requested: "acct-1", Applied: "acct-2"},            // mismatch
		FieldCheck{Field: "labels", Requested: "x", Applied: "x"},                        // applied
		FieldCheck{Field: "key", Requested: "DEV-1", Applied: "dev-1", IgnoreCase: true}, // applied (ci)
	)
	if len(got) != 2 {
		t.Fatalf("expected 2 unapplied, got %v", got)
	}
	// sorted: "assignee ..." before "summary ..."
	if !strings.HasPrefix(got[0], "assignee") || !strings.HasPrefix(got[1], "summary") {
		t.Fatalf("unsorted: %v", got)
	}
}

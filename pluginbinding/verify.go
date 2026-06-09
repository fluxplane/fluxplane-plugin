package pluginbinding

import (
	"fmt"
	"sort"
	"strings"
)

// FieldCheck is one requested-vs-applied comparison for detecting a silent
// no-op write: a backend that accepts a write (HTTP 2xx) yet does not actually
// apply a field — because the field is not on an edit screen, is unsupported by
// the target type, or the caller lacks permission. These are reported as
// success by the transport, so the only reliable signal is to re-read the
// entity and compare what was asked against what stuck.
//
// A check whose Requested value is empty means "nothing was asked for this
// field" and is never reported.
type FieldCheck struct {
	// Field is the name surfaced to the caller, e.g. "parent" or "assignee".
	Field string
	// Requested is the value the caller asked to set ("" = not requested).
	Requested string
	// Applied is the value present on the re-read entity after the write.
	Applied string
	// IgnoreCase compares case-insensitively (e.g. issue keys, emails).
	IgnoreCase bool
}

func (c FieldCheck) unapplied() bool {
	req := strings.TrimSpace(c.Requested)
	if req == "" {
		return false
	}
	got := strings.TrimSpace(c.Applied)
	if c.IgnoreCase {
		return !strings.EqualFold(got, req)
	}
	return got != req
}

// UnappliedFields returns a "field (requested X, applied Y)" description for
// every requested field that did not stick. Checks with an empty Requested are
// ignored. Output is sorted by description for stable, testable results.
func UnappliedFields(checks ...FieldCheck) []string {
	var out []string
	for _, c := range checks {
		if c.unapplied() {
			out = append(out, fmt.Sprintf("%s (requested %q, applied %q)", c.Field, strings.TrimSpace(c.Requested), orUnset(c.Applied)))
		}
	}
	sort.Strings(out)
	return out
}

// VerifyAppliedWarning returns a ready-to-surface warning naming every requested
// field a write silently dropped, or "" when every requested field is present
// on the re-read entity. Attach the result to a mutation's warning field so that
// "ok": true never masks a change that never happened.
func VerifyAppliedWarning(checks ...FieldCheck) string {
	unapplied := UnappliedFields(checks...)
	if len(unapplied) == 0 {
		return ""
	}
	return "the backend accepted the write but these fields are not set afterward: " +
		strings.Join(unapplied, "; ") +
		". The field may be unsupported by the target, not on its edit/create screen, or blocked by permissions."
}

func orUnset(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(unset)"
	}
	return value
}

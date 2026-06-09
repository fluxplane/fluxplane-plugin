package pluginbinding

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNewListResultIsComplete(t *testing.T) {
	r := NewListResult([]int{1, 2, 3})
	if r.Count != 3 || r.HasMore || r.NextPageToken != "" || r.Total != 0 {
		t.Fatalf("plain list result = %#v", r)
	}
	// Pagination fields are omitempty -> absent for a complete result.
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), "has_more") || strings.Contains(string(raw), "next_page_token") {
		t.Fatalf("complete result should omit pagination fields: %s", raw)
	}
}

func TestNewPagedListResultSignalsTruncation(t *testing.T) {
	// Token present -> HasMore.
	r := NewPagedListResult([]int{1, 2}, 0, "tok-2")
	if !r.HasMore || r.NextPageToken != "tok-2" {
		t.Fatalf("token paged = %#v", r)
	}
	// Total > count -> HasMore even without a token.
	r = NewPagedListResult([]int{1, 2}, 10, "")
	if !r.HasMore || r.Total != 10 {
		t.Fatalf("total paged = %#v", r)
	}
	// Last page (count == total, no token) -> not truncated.
	r = NewPagedListResult([]int{1, 2}, 2, "")
	if r.HasMore {
		t.Fatalf("last page should not be truncated: %#v", r)
	}
}

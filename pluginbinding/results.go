package pluginbinding

import "strings"

// ListResult is the standard shape for list-style operation output. Count is the
// number of items in this page; Total/HasMore/NextPageToken signal whether the
// list was truncated so a caller (agent) can tell a complete result from a
// partial one and fetch the rest. The pagination fields are omitempty so
// non-paginated results stay unchanged.
type ListResult[T any] struct {
	Items         []T    `json:"items"`
	Count         int    `json:"count"`
	Total         int    `json:"total,omitempty"`
	HasMore       bool   `json:"has_more,omitempty"`
	NextPageToken string `json:"next_page_token,omitempty"`
}

type ShowResult[T any] struct {
	Record   T              `json:"record"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// NewListResult builds a complete (non-truncated) list result.
func NewListResult[T any](items []T) ListResult[T] {
	return ListResult[T]{Items: items, Count: len(items)}
}

// NewPagedListResult builds a list result that signals truncation. Pass the
// known total (0 if unknown) and a nextPageToken to fetch the following page
// ("" if this is the last page). HasMore is derived from either signal.
func NewPagedListResult[T any](items []T, total int, nextPageToken string) ListResult[T] {
	token := strings.TrimSpace(nextPageToken)
	result := ListResult[T]{Items: items, Count: len(items), Total: total, NextPageToken: token}
	result.HasMore = token != "" || (total > 0 && len(items) < total)
	return result
}

func NewShowResult[T any](record T, metadata map[string]any) ShowResult[T] {
	return ShowResult[T]{Record: record, Metadata: cloneAnyMap(metadata)}
}

func cloneAnyMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

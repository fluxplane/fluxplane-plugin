package pluginbinding

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

type IndexBuildResult struct {
	Index   string        `json:"index,omitempty"`
	Records any           `json:"records,omitempty"`
	Count   int           `json:"count"`
	Indexes []IndexResult `json:"indexes,omitempty"`
}

type IndexBuildInput struct {
	Index    string `json:"index,omitempty" jsonschema:"description=Index selector"`
	Indexes  string `json:"indexes,omitempty" jsonschema:"description=Comma-separated index selectors"`
	Entity   string `json:"entity,omitempty" jsonschema:"description=Entity selector"`
	Entities string `json:"entities,omitempty" jsonschema:"description=Comma-separated entity selectors"`
}

type ListInput struct {
	Limit   int    `json:"limit,omitempty" jsonschema:"description=Maximum results to return"`
	Search  string `json:"search,omitempty" jsonschema:"description=Search text"`
	Query   string `json:"query,omitempty" jsonschema:"description=Alias for search"`
	OrderBy string `json:"order_by,omitempty" jsonschema:"description=Order by value"`
	Sort    string `json:"sort,omitempty" jsonschema:"description=Sort direction,enum=asc,enum=desc"`
}

type IndexResult struct {
	Index    string         `json:"index"`
	Records  any            `json:"records"`
	Count    int            `json:"count"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type IndexJob struct {
	Index string
	run   func(Context) (IndexResult, error)
}

type IndexSelector struct {
	indexes map[string]bool
}

func InputMap(input any) map[string]any {
	raw, err := json.Marshal(input)
	if err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func NewIndexResult(index string, records any, metadata map[string]any) IndexResult {
	return IndexResult{
		Index:    index,
		Records:  records,
		Count:    valueLen(records),
		Metadata: cloneAnyMap(metadata),
	}
}

func NewIndexBuildResult(indexes ...IndexResult) IndexBuildResult {
	result := IndexBuildResult{Indexes: append([]IndexResult(nil), indexes...)}
	if len(indexes) > 0 {
		result.Index = indexes[0].Index
		result.Records = indexes[0].Records
		result.Count = indexes[0].Count
	}
	return result
}

func NewIndexJob[T any, R any](index, entity, operation string, fetch func() ([]T, error), normalize func(DatasourceSource, T) (R, bool), metadata map[string]any) IndexJob {
	return NewDynamicIndexJob(index, entity, operation, fetch, normalize, func() map[string]any {
		return metadata
	})
}

func NewDynamicIndexJob[T any, R any](index, entity, operation string, fetch func() ([]T, error), normalize func(DatasourceSource, T) (R, bool), metadata func() map[string]any) IndexJob {
	return IndexJob{
		Index: index,
		run: func(ctx Context) (IndexResult, error) {
			items, err := fetch()
			if err != nil {
				return IndexResult{}, err
			}
			source := ctx.DatasourceSource()
			records := make([]R, 0, len(items))
			for _, item := range items {
				record, ok := normalize(source, item)
				if ok {
					records = append(records, record)
				}
			}
			var meta map[string]any
			if metadata != nil {
				meta = metadata()
			}
			return NewIndexResult(index, records, IndexBuildMetadata(entity, operation, meta)), nil
		},
	}
}

func NewRequiredIndexJob[T any, R any](index, entity, operation string, fetch func() ([]T, error), normalize func(DatasourceSource, T) R, metadata map[string]any) IndexJob {
	return NewIndexJob(index, entity, operation, fetch, func(source DatasourceSource, item T) (R, bool) {
		return normalize(source, item), true
	}, metadata)
}

func RunIndexJobs(ctx Context, selector IndexSelector, errorCode string, jobs ...IndexJob) (IndexBuildResult, error) {
	results := make([]IndexResult, 0, len(jobs))
	for _, job := range jobs {
		if strings.TrimSpace(job.Index) == "" || job.run == nil || !selector.Includes(job.Index) {
			continue
		}
		result, err := job.run(ctx)
		if err != nil {
			var pluginErr Error
			if errors.As(err, &pluginErr) {
				return IndexBuildResult{}, err
			}
			if strings.TrimSpace(errorCode) == "" {
				errorCode = "plugin_error"
			}
			return IndexBuildResult{}, Errorf(errorCode, "%s", err)
		}
		results = append(results, result)
	}
	return NewIndexBuildResult(results...), nil
}

func IndexBuildMetadata(entity, operation string, input map[string]any) map[string]any {
	metadata := map[string]any{
		"entity":     entity,
		"source":     operation,
		"fetch_mode": "all_pages",
	}
	for key, value := range input {
		metadata[key] = value
	}
	return metadata
}

func NewIndexSelector(input map[string]any, known map[string]string, label string) (IndexSelector, error) {
	values := SplitSelectorValues(FirstString(input, "index", "indexes"), FirstString(input, "entity", "entities"))
	if len(values) == 0 {
		return IndexSelector{}, nil
	}
	selector := IndexSelector{indexes: map[string]bool{}}
	for _, value := range values {
		index, ok := known[value]
		if !ok {
			return IndexSelector{}, fmt.Errorf("unknown %s index/entity selector %q", label, value)
		}
		selector.indexes[index] = true
	}
	return selector, nil
}

func (s IndexSelector) Includes(index string) bool {
	if len(s.indexes) == 0 {
		return true
	}
	return s.indexes[index]
}

func SplitSelectorValues(values ...string) []string {
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.ToLower(strings.TrimSpace(part))
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func FirstString(input map[string]any, keys ...string) string {
	for _, key := range keys {
		switch value := input[key].(type) {
		case string:
			if strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		case float64:
			if value != 0 {
				return strconv.FormatInt(int64(value), 10)
			}
		case int:
			if value != 0 {
				return strconv.Itoa(value)
			}
		}
	}
	return ""
}

func IntFromInput(input map[string]any, key string, fallback int) int {
	switch value := input[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			return parsed
		}
	}
	return fallback
}

func BoundedIntFromInput(input map[string]any, key string, fallback, max int) int {
	value := IntFromInput(input, key, fallback)
	if value <= 0 {
		value = fallback
	}
	if max > 0 && value > max {
		value = max
	}
	return value
}

func StringFromInput(input map[string]any, keys ...string) string {
	return strings.TrimSpace(FirstString(input, keys...))
}

func DefaultStringFromInput(input map[string]any, fallback string, keys ...string) string {
	value := StringFromInput(input, keys...)
	if value == "" {
		return fallback
	}
	return value
}

func BoolPtrFromInput(input map[string]any, key string, fallback bool) *bool {
	value := fallback
	if raw, ok := input[key].(bool); ok {
		value = raw
	}
	return &value
}

func valueLen(value any) int {
	if value == nil {
		return 0
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Array, reflect.Slice, reflect.Map, reflect.String:
		return rv.Len()
	default:
		return 0
	}
}

package pluginbinding

import (
	"strings"

	sdkdatasource "github.com/fluxplane/fluxplane-plugin/datasource"
)

type DatasourceSource = sdkdatasource.Source
type DatasourceRecord = sdkdatasource.Record
type LookupSource = sdkdatasource.LookupSource
type LookupMatch[R any] = sdkdatasource.LookupMatch[R]
type LookupCandidate = sdkdatasource.LookupCandidate
type DatasourceSearchInput = sdkdatasource.SearchInput
type DatasourceLookupInput = sdkdatasource.LookupInput
type DatasourceGetInput = sdkdatasource.GetInput
type DatasourceSearchResult[T any] = sdkdatasource.SearchResult[T]
type DatasourceError = sdkdatasource.Error
type DatasourceLookupResult[T any] = sdkdatasource.LookupResult[T]
type DatasourceGetResult[T any] = sdkdatasource.GetResult[T]
type DatasourceRecordOption = sdkdatasource.RecordOption

func (ctx Context) DatasourceSource() DatasourceSource {
	plugin := strings.TrimSpace(ctx.Request.Plugin)
	if plugin == "" && ctx.plugin != nil {
		plugin = ctx.plugin.manifest.Name
	}
	return DatasourceSource{Plugin: plugin, Instance: strings.TrimSpace(ctx.Request.Instance)}
}

func NewDatasourceRecord(source DatasourceSource, entity, id string, options ...DatasourceRecordOption) DatasourceRecord {
	return sdkdatasource.NewRecord(source, entity, id, options...)
}

func NewDatasourceSearchResult[T any](source, query string, records []T) DatasourceSearchResult[T] {
	return sdkdatasource.NewSearchResult(source, query, records)
}

func NewDatasourceLookupResult[T any](source, text string, terms []string, matches []T) DatasourceLookupResult[T] {
	return sdkdatasource.NewLookupResult(source, text, terms, matches)
}

func NewDatasourceGetResult[T any](source string, record T) DatasourceGetResult[T] {
	return sdkdatasource.NewGetResult(source, record)
}

func (ctx Context) LookupSource(source, index string) LookupSource {
	origin := LookupSource{Source: strings.TrimSpace(source), Instance: strings.TrimSpace(ctx.Request.Instance), Index: strings.TrimSpace(index)}
	origin.Plugin = strings.TrimSpace(ctx.Request.Plugin)
	if origin.Plugin == "" && ctx.plugin != nil {
		origin.Plugin = ctx.plugin.manifest.Name
	}
	return origin
}

func NewLookupMatch[R any](source LookupSource, entity, id string, score int, matchedFields []string, record R) LookupMatch[R] {
	return sdkdatasource.NewLookupMatch(source, entity, id, score, matchedFields, record)
}

func NewLookupCandidate(source LookupSource, entity, id string, record any, values map[string]string) LookupCandidate {
	return sdkdatasource.NewLookupCandidate(source, entity, id, record, values)
}

func NewExactLookupCandidate(source LookupSource, entity, id string, score int, matchedFields []string, record any, values map[string]string) LookupCandidate {
	return sdkdatasource.NewExactLookupCandidate(source, entity, id, score, matchedFields, record, values)
}

func NewDatasourceLookupResultFromCandidates(source string, input DatasourceLookupInput, candidates []LookupCandidate) DatasourceLookupResult[LookupMatch[any]] {
	return sdkdatasource.NewLookupResultFromCandidates(source, input, candidates)
}

func LookupMatches(input DatasourceLookupInput, candidates []LookupCandidate) []LookupMatch[any] {
	return sdkdatasource.LookupMatches(input, candidates)
}

func SortLookupMatches[R any](matches []LookupMatch[R]) {
	sdkdatasource.SortLookupMatches(matches)
}

func LookupTerms(input DatasourceLookupInput) []string {
	return sdkdatasource.LookupTerms(input)
}

func LookupLimit(input DatasourceLookupInput, fallback, max int) int {
	return sdkdatasource.LookupLimit(input, fallback, max)
}

func FilterLookupTerms(input DatasourceLookupInput, max int, keep func(string) bool) []string {
	return sdkdatasource.FilterLookupTerms(input, max, keep)
}

func LookupTermsFrom(text string, explicit []string) []string {
	return sdkdatasource.LookupTermsFrom(text, explicit)
}

func ScoreLookupValues(input DatasourceLookupInput, values map[string]string, exactScore int) (int, []string) {
	return sdkdatasource.ScoreLookupValues(input, values, exactScore)
}

func RecordTitle(title string) DatasourceRecordOption {
	return sdkdatasource.RecordTitle(title)
}

func RecordLink(name, url string) DatasourceRecordOption {
	return sdkdatasource.RecordLink(name, url)
}

func RecordMetadata(metadata map[string]any) DatasourceRecordOption {
	return sdkdatasource.RecordMetadata(metadata)
}

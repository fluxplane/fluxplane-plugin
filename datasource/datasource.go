// Package datasource exposes plugin SDK datasource contracts and helpers.
package datasource

import fpdatasource "github.com/fluxplane/fluxplane-datasource"

const (
	// ViewCompact is the compact datasource record view.
	ViewCompact = fpdatasource.DeclarationViewCompact
	// ViewDetail is the detailed datasource record view.
	ViewDetail = fpdatasource.DeclarationViewDetail
	// ViewLookup is the lookup-oriented datasource record view.
	ViewLookup = fpdatasource.DeclarationViewLookup
	// ViewTable is the table datasource record view.
	ViewTable = fpdatasource.DeclarationViewTable
)

type Source = fpdatasource.Source
type Record = fpdatasource.RecordBase
type LookupSource = fpdatasource.LookupSource
type LookupMatch[R any] = fpdatasource.LookupMatch[R]
type LookupCandidate = fpdatasource.LookupCandidate
type SearchInput = fpdatasource.SearchInput
type LookupInput = fpdatasource.LookupInput
type GetInput = fpdatasource.GetInput
type SearchResult[T any] = fpdatasource.SearchOutput[T]
type LookupResult[T any] = fpdatasource.LookupOutput[T]
type GetResult[T any] = fpdatasource.GetOutput[T]
type Error = fpdatasource.Error
type RecordOption = fpdatasource.RecordOption

func NewRecord(source Source, entity, id string, options ...RecordOption) Record {
	return fpdatasource.NewRecord(source, entity, id, options...)
}

func NewSearchResult[T any](source, query string, records []T) SearchResult[T] {
	return fpdatasource.NewSearchOutput(source, query, records)
}

func NewLookupResult[T any](source, text string, terms []string, matches []T) LookupResult[T] {
	return fpdatasource.NewLookupOutput(source, text, terms, matches)
}

func NewGetResult[T any](source string, record T) GetResult[T] {
	return fpdatasource.NewGetOutput(source, record)
}

func NewLookupMatch[R any](source LookupSource, entity, id string, score int, matchedFields []string, record R) LookupMatch[R] {
	return fpdatasource.NewLookupMatch(source, entity, id, score, matchedFields, record)
}

func NewLookupCandidate(source LookupSource, entity, id string, record any, values map[string]string) LookupCandidate {
	return fpdatasource.NewLookupCandidate(source, entity, id, record, values)
}

func NewExactLookupCandidate(source LookupSource, entity, id string, score int, matchedFields []string, record any, values map[string]string) LookupCandidate {
	return fpdatasource.NewExactLookupCandidate(source, entity, id, score, matchedFields, record, values)
}

func NewLookupResultFromCandidates(source string, input LookupInput, candidates []LookupCandidate) LookupResult[LookupMatch[any]] {
	return fpdatasource.NewLookupOutputFromCandidates(source, input, candidates)
}

func LookupMatches(input LookupInput, candidates []LookupCandidate) []LookupMatch[any] {
	return fpdatasource.LookupMatches(input, candidates)
}

func SortLookupMatches[R any](matches []LookupMatch[R]) {
	fpdatasource.SortLookupMatches(matches)
}

func LookupTerms(input LookupInput) []string {
	return fpdatasource.LookupTerms(input)
}

func LookupLimit(input LookupInput, fallback, max int) int {
	return fpdatasource.LookupLimit(input, fallback, max)
}

func FilterLookupTerms(input LookupInput, max int, keep func(string) bool) []string {
	return fpdatasource.FilterLookupTerms(input, max, keep)
}

func LookupTermsFrom(text string, explicit []string) []string {
	return fpdatasource.LookupTermsFrom(text, explicit)
}

func ScoreLookupValues(input LookupInput, values map[string]string, exactScore int) (int, []string) {
	return fpdatasource.ScoreLookupValues(input, values, exactScore)
}

func RecordTitle(title string) RecordOption {
	return fpdatasource.RecordTitle(title)
}

func RecordLink(name, url string) RecordOption {
	return fpdatasource.RecordLink(name, url)
}

func RecordMetadata(metadata map[string]any) RecordOption {
	return fpdatasource.RecordMetadata(metadata)
}

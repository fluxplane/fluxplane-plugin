package datasource

import "testing"

func TestLookupHelpersScoreSortAndLimit(t *testing.T) {
	input := LookupInput{Text: "Engineering channel", Terms: []string{"engineering"}, Limit: 1}
	source := LookupSource{Plugin: "slack", Instance: "default", Source: "slack.channels", Index: "channels"}
	candidates := []LookupCandidate{
		NewLookupCandidate(source, "slack.channel", "C2", map[string]any{"name": "random"}, map[string]string{"name": "random"}),
		NewLookupCandidate(source, "slack.channel", "C1", map[string]any{"name": "engineering"}, map[string]string{"name": "engineering"}),
	}
	matches := LookupMatches(input, candidates)
	if len(matches) != 1 {
		t.Fatalf("matches = %#v", matches)
	}
	if matches[0].ID != "C1" {
		t.Fatalf("match = %#v", matches[0])
	}
}

func TestRecordOptions(t *testing.T) {
	record := NewRecord(Source{Plugin: "gitlab", Instance: "default"}, "gitlab.project", "1", RecordTitle("fluxplane"), RecordLink("web", "https://example.test"), RecordMetadata(map[string]any{"visibility": "private"}))
	if record.Title != "fluxplane" {
		t.Fatalf("title = %q", record.Title)
	}
	if len(record.Links) != 1 || record.Links["web"] != "https://example.test" {
		t.Fatalf("links = %#v", record.Links)
	}
	if record.Metadata["visibility"] != "private" {
		t.Fatalf("metadata = %#v", record.Metadata)
	}
}

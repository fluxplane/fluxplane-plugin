package pluginbinding

import (
	"testing"

	"github.com/fluxplane/fluxplane-plugin/protocol"
)

func TestNewIndexBuildResultUsesFirstIndexAsLegacyTopLevel(t *testing.T) {
	users := []DatasourceRecord{
		NewDatasourceRecord(DatasourceSource{Plugin: "test", Instance: "default"}, "test.user", "u1", RecordTitle("User One"), RecordLink("self", "https://example.com/u1")),
	}
	channels := []DatasourceRecord{
		NewDatasourceRecord(DatasourceSource{Plugin: "test", Instance: "default"}, "test.channel", "c1"),
		NewDatasourceRecord(DatasourceSource{Plugin: "test", Instance: "default"}, "test.channel", "c2"),
	}
	result := NewIndexBuildResult(
		NewIndexResult("test.users", users, IndexBuildMetadata("test.user", "test.index.build", map[string]any{"limit": 100})),
		NewIndexResult("test.channels", channels, nil),
	)
	if result.Index != "test.users" || result.Count != 1 || len(result.Indexes) != 2 {
		t.Fatalf("result = %#v", result)
	}
	if result.Indexes[1].Count != 2 {
		t.Fatalf("second index = %#v", result.Indexes[1])
	}
	if result.Indexes[0].Metadata["entity"] != "test.user" || result.Indexes[0].Metadata["source"] != "test.index.build" {
		t.Fatalf("metadata = %#v", result.Indexes[0].Metadata)
	}
}

func TestRunIndexJobsUsesDynamicMetadataAndSource(t *testing.T) {
	plugin := Define(ManifestSpec{Name: "test"})
	selector, err := NewIndexSelector(map[string]any{}, nil, "Test")
	if err != nil {
		t.Fatal(err)
	}
	tokenSource := "user_token"
	result, err := RunIndexJobs(
		Context{Request: protocolRequest("test", "work"), plugin: plugin},
		selector,
		"test",
		NewDynamicIndexJob("test.users", "test.user", "test.index.build", func() ([]string, error) {
			return []string{"u1"}, nil
		}, func(source DatasourceSource, id string) (DatasourceRecord, bool) {
			return NewDatasourceRecord(source, "test.user", id, RecordTitle("User One")), true
		}, func() map[string]any {
			return map[string]any{"token_source": tokenSource}
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	records, ok := result.Records.([]DatasourceRecord)
	if !ok || len(records) != 1 {
		t.Fatalf("records = %#v", result.Records)
	}
	if records[0].Source.Plugin != "test" || records[0].Source.Instance != "work" {
		t.Fatalf("record source = %#v", records[0].Source)
	}
	if result.Indexes[0].Metadata["token_source"] != "user_token" {
		t.Fatalf("metadata = %#v", result.Indexes[0].Metadata)
	}
}

func TestIndexSelectorUsesAliases(t *testing.T) {
	selector, err := NewIndexSelector(
		map[string]any{"entity": "user,channels"},
		map[string]string{"user": "test.users", "channels": "test.channels"},
		"Test",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !selector.Includes("test.users") || !selector.Includes("test.channels") || selector.Includes("test.groups") {
		t.Fatalf("selector = %#v", selector)
	}
}

func TestInputDefaultHelpers(t *testing.T) {
	input := map[string]any{"limit": float64(500), "sort": "asc", "enabled": false}
	if got := BoundedIntFromInput(input, "limit", 20, 100); got != 100 {
		t.Fatalf("bounded limit = %d", got)
	}
	if got := DefaultStringFromInput(input, "desc", "sort"); got != "asc" {
		t.Fatalf("sort = %q", got)
	}
	if got := DefaultStringFromInput(input, "updated_at", "order_by"); got != "updated_at" {
		t.Fatalf("order_by = %q", got)
	}
	if got := BoolPtrFromInput(input, "enabled", true); got == nil || *got {
		t.Fatalf("bool ptr = %#v", got)
	}
}

func protocolRequest(plugin, instance string) protocol.Request {
	return protocol.Request{Plugin: plugin, Instance: instance}
}

func TestIndexSelectorRejectsUnknownAliases(t *testing.T) {
	_, err := NewIndexSelector(map[string]any{"index": "missing"}, map[string]string{}, "Test")
	if err == nil {
		t.Fatalf("expected error")
	}
}

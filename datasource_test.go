package fluxplaneplugin

import (
	"encoding/json"
	"testing"

	coredatasource "github.com/fluxplane/fluxplane-datasource"
)

func TestDecodeRecordsUnwrapsDatasourceGetRecord(t *testing.T) {
	raw := json.RawMessage(`{"source":"jira.issues","record":{"id":"DEV-479","title":"Fix upload","key":"DEV-479","summary":"Fix upload"}}`)

	records := decodeRecords(raw, coredatasource.Name("jira"), coredatasource.EntityType("jira.issue"))
	if len(records) != 1 {
		t.Fatalf("records len = %d", len(records))
	}
	if records[0].ID != "DEV-479" || records[0].Title != "Fix upload" {
		t.Fatalf("record = %#v", records[0])
	}
}

func TestDexAccessorAppliesEndpointRefFromSpecConfig(t *testing.T) {
	accessor := &dexAccessor{spec: coredatasource.Spec{Config: map[string]string{"endpoint_ref": "jira-prod"}}}
	payload := map[string]any{"id": "DEV-479"}

	accessor.applySpecConfig(payload)

	if payload["endpoint_ref"] != "jira-prod" {
		t.Fatalf("payload = %#v", payload)
	}
}

package protocol

import "testing"

func TestV2FrameSemantics(t *testing.T) {
	req, err := NewRequestFrame("1", TargetPlugin, CommandOperationsCall, OperationCall{Name: "slack.info"})
	if err != nil {
		t.Fatal(err)
	}
	if req.Type != FrameRequest || req.Target != TargetPlugin || req.Command != CommandOperationsCall {
		t.Fatalf("request frame = %#v", req)
	}
	resp := FrameResponseValue("1", map[string]string{"ok": "true"})
	if resp.Type != FrameResponse || resp.ID != "1" || !resp.OK {
		t.Fatalf("response frame = %#v", resp)
	}
	event, err := NewEventFrame("event-1", TargetHost, "status", map[string]string{"message": "working"})
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != FrameEvent || event.Target != TargetHost || event.Command != "status" {
		t.Fatalf("event frame = %#v", event)
	}
}

func TestRequestRoundTripPayload(t *testing.T) {
	req, err := NewRequest(CommandOperationsCall, "gitlab", OperationCall{Name: "gitlab.project.list"})
	if err != nil {
		t.Fatal(err)
	}
	if req.Protocol != Version {
		t.Fatalf("protocol = %q, want %q", req.Protocol, Version)
	}
	call, err := DecodePayload[OperationCall](req.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if call.Name != "gitlab.project.list" {
		t.Fatalf("operation = %q", call.Name)
	}
}

func TestFailResponse(t *testing.T) {
	resp := Fail("bad_input", "missing name")
	if resp.OK {
		t.Fatal("failure response reported OK")
	}
	if resp.Protocol != Version {
		t.Fatalf("protocol = %q", resp.Protocol)
	}
	if resp.Error == nil || resp.Error.Message != "missing name" {
		t.Fatalf("unexpected error payload: %#v", resp.Error)
	}
}

func TestBatchPayloadRoundTrip(t *testing.T) {
	req, err := NewRequest(CommandOperationsBatch, "gitlab", OperationBatch{Calls: []OperationCall{
		{ID: "a", Name: "gitlab.index.build"},
		{ID: "b", Name: "gitlab.mr.show"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := DecodePayload[OperationBatch](req.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(batch.Calls))
	}
	if batch.Calls[1].ID != "b" {
		t.Fatalf("second call id = %q", batch.Calls[1].ID)
	}
}

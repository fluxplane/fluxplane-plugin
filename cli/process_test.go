package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdkhost "github.com/fluxplane/fluxplane-plugin/host"
)

// processFakeBackend satisfies management.ProcessManager over a fixed record set.
type processFakeBackend struct {
	*fakeBackend
	records  []sdkhost.ProcessRecord
	listReqs []sdkhost.ProcessListRequest
	stopReqs []sdkhost.ProcessStopRequest
}

func (b *processFakeBackend) ListProcesses(_ context.Context, req sdkhost.ProcessListRequest) (sdkhost.ProcessListResponse, error) {
	b.listReqs = append(b.listReqs, req)
	out := sdkhost.ProcessListResponse{Processes: []sdkhost.ProcessRecord{}}
	for _, record := range b.records {
		if req.Group != "" && record.Group != req.Group {
			continue
		}
		if req.Plugin != "" && record.Plugin != req.Plugin {
			continue
		}
		out.Processes = append(out.Processes, record)
	}
	out.Count = len(out.Processes)
	return out, nil
}

func (b *processFakeBackend) StopProcess(_ context.Context, req sdkhost.ProcessStopRequest) (sdkhost.ProcessStopResponse, error) {
	b.stopReqs = append(b.stopReqs, req)
	return sdkhost.ProcessStopResponse{ID: req.ID, Stopped: true, Signal: req.Signal}, nil
}

func newProcessBackend(records ...sdkhost.ProcessRecord) *processFakeBackend {
	return &processFakeBackend{fakeBackend: &fakeBackend{}, records: records}
}

func TestProcessListFiltersAndPrints(t *testing.T) {
	backend := newProcessBackend(
		sdkhost.ProcessRecord{ID: "proc-1", Plugin: "kubernetes", Group: "kubernetes.portforward", Label: "svc/homer 19080:80", Alive: true},
		sdkhost.ProcessRecord{ID: "proc-2", Plugin: "asterisk", Group: "asterisk.tunnel"},
	)
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"process", "list", "--group", "kubernetes.portforward"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result sdkhost.ProcessListResponse
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if result.Count != 1 || result.Processes[0].ID != "proc-1" || !result.Processes[0].Alive {
		t.Fatalf("result = %#v", result)
	}
	if len(backend.listReqs) != 1 || backend.listReqs[0].Group != "kubernetes.portforward" {
		t.Fatalf("listReqs = %#v", backend.listReqs)
	}
}

func TestProcessLogsTailsLastLines(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "proc.log")
	if err := os.WriteFile(logPath, []byte("one\ntwo\nthree\nfour\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := newProcessBackend(sdkhost.ProcessRecord{ID: "proc-1", LogPath: logPath})
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"process", "logs", "proc-1", "-n", "2"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.String() != "three\nfour\n" {
		t.Fatalf("logs = %q, want last two lines", out.String())
	}
}

func TestProcessStopResolvesPrefix(t *testing.T) {
	backend := newProcessBackend(sdkhost.ProcessRecord{ID: "proc-abcdef", Plugin: "kubernetes"})
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"process", "stop", "proc-abc"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(backend.stopReqs) != 1 || backend.stopReqs[0].ID != "proc-abcdef" || backend.stopReqs[0].Signal != "SIGTERM" {
		t.Fatalf("stopReqs = %#v", backend.stopReqs)
	}
}

func TestProcessStopUnknownIDFails(t *testing.T) {
	backend := newProcessBackend()
	var out bytes.Buffer
	cmd := New(Options{Backend: backend, Out: &out})
	cmd.SetArgs([]string{"process", "stop", "nope"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "no background process") {
		t.Fatalf("err = %v, want unknown-process error", err)
	}
}

func TestProcessRequiresProcessManagerBackend(t *testing.T) {
	var out bytes.Buffer
	cmd := New(Options{Backend: &fakeBackend{}, Out: &out})
	cmd.SetArgs([]string{"process", "list"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "does not support process management") {
		t.Fatalf("err = %v, want unsupported-backend error", err)
	}
}

package pluginruntime

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	manifest "github.com/fluxplane/fluxplane-plugin/manifest"
	"github.com/fluxplane/fluxplane-plugin/pluginbinding"
	"github.com/fluxplane/fluxplane-plugin/protocol"
)

type echoInput struct {
	Text string `json:"text"`
}

type echoOutput struct {
	Text string `json:"text"`
}

func TestHostInvokesDirectPlugin(t *testing.T) {
	host, err := NewHost(Direct(testPlugin()))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := host.CallOperation(context.Background(), "echo", protocol.OperationCall{Name: "echo", Input: mustJSON(t, echoInput{Text: "direct"})})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("operation failed: %#v", resp.Error)
	}
	var out echoOutput
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	if out.Text != "direct" {
		t.Fatalf("unexpected output %q", out.Text)
	}
}

func TestHostReadsDirectManifest(t *testing.T) {
	host, err := NewHost(Direct(testPlugin()))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := host.Manifest(context.Background(), "echo")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Name != "echo" {
		t.Fatalf("unexpected manifest name %q", manifest.Name)
	}
	if len(manifest.Operations) != 1 || manifest.Operations[0].Name != "echo" {
		t.Fatalf("unexpected operations: %#v", manifest.Operations)
	}
}

func TestHostInvokesStdioPlugin(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	stdio := Stdio("echo", exe, "-test.run=TestStdioPluginHelper")
	stdio.Env = []string{"FLUXPLANE_PLUGINRUNTIME_HELPER=1"}
	host, err := NewHost(stdio)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := host.CallOperation(context.Background(), "echo", protocol.OperationCall{Name: "echo", Input: mustJSON(t, echoInput{Text: "stdio"})})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("operation failed: %#v", resp.Error)
	}
	var out echoOutput
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	if out.Text != "stdio" {
		t.Fatalf("unexpected output %q", out.Text)
	}
}

func TestStdioPluginHelper(t *testing.T) {
	if os.Getenv("FLUXPLANE_PLUGINRUNTIME_HELPER") != "1" {
		return
	}
	pluginbinding.Serve(testPlugin())
	os.Exit(0)
}

func testPlugin() *pluginbinding.Plugin {
	return pluginbinding.Define(
		pluginbinding.ManifestSpec{
			Name:        "echo",
			Description: "Echo test plugin.",
		},
		pluginbinding.RegisterOperation[echoInput, echoOutput](
			manifest.OperationSpec{Name: "echo", Description: "Echo text."},
			func(_ pluginbinding.Context, input echoInput) (echoOutput, error) {
				return echoOutput{Text: input.Text}, nil
			},
		),
	)
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

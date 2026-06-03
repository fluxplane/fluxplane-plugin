package host

import (
	"encoding/json"
	"testing"

	"github.com/fluxplane/fluxplane-plugin/protocol"
)

type callerFunc func(string, any) (json.RawMessage, error)

func (f callerFunc) CallHost(command string, input any) (json.RawMessage, error) {
	return f(command, input)
}
func (f callerFunc) EmitHostEvent(string, any) error {
	return nil
}

func TestClientSecretTrimsPurposeAndPreservesCommand(t *testing.T) {
	var seenCommand string
	var seenInput any
	client := NewClient(callerFunc(func(command string, input any) (json.RawMessage, error) {
		seenCommand = command
		seenInput = input
		return json.RawMessage(`{"value":"secret"}`), nil
	}))
	material, err := client.Secret(" token ")
	if err != nil {
		t.Fatalf("Secret: %v", err)
	}
	if seenCommand != SecretGetCommand {
		t.Fatalf("command = %q", seenCommand)
	}
	input := seenInput.(map[string]any)
	if input["purpose"] != "token" {
		t.Fatalf("input = %#v", input)
	}
	if material.Value != "secret" || material.Purpose != "token" {
		t.Fatalf("material = %#v", material)
	}
}

func TestClientHTTPUsesProtocolCapabilityCommand(t *testing.T) {
	var seenCommand string
	client := NewClient(callerFunc(func(command string, input any) (json.RawMessage, error) {
		seenCommand = command
		return json.RawMessage(`{"url":"https://example.test","status_code":200}`), nil
	}))
	resp, err := client.HTTP(HTTPRequest{URL: "https://example.test"})
	if err != nil {
		t.Fatalf("HTTP: %v", err)
	}
	if seenCommand != protocol.HostCapabilityHTTPDo {
		t.Fatalf("command = %q", seenCommand)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("resp = %#v", resp)
	}
}

func TestClientProcessRunUsesProtocolCapabilityCommand(t *testing.T) {
	var seenCommand string
	var seenInput any
	client := NewClient(callerFunc(func(command string, input any) (json.RawMessage, error) {
		seenCommand = command
		seenInput = input
		return json.RawMessage(`{"command":"git","exit_code":0,"stdout":"ok"}`), nil
	}))
	resp, err := client.ProcessRun(ProcessRunRequest{Command: "git", Args: []string{"status"}, TimeoutMS: 1000})
	if err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}
	if seenCommand != protocol.HostCapabilityProcessRun {
		t.Fatalf("command = %q", seenCommand)
	}
	input := seenInput.(ProcessRunRequest)
	if input.Command != "git" || len(input.Args) != 1 || input.Args[0] != "status" || input.TimeoutMS != 1000 {
		t.Fatalf("input = %#v", input)
	}
	if resp.Command != "git" || resp.ExitCode != 0 || resp.Stdout != "ok" {
		t.Fatalf("resp = %#v", resp)
	}
}

func TestUnavailableClient(t *testing.T) {
	_, err := NewClient(nil).Secret("token")
	if err == nil {
		t.Fatal("expected unavailable error")
	}
}

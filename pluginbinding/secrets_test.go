package pluginbinding

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	manifest "github.com/fluxplane/fluxplane-plugin/manifest"
	"github.com/fluxplane/fluxplane-plugin/protocol"
)

type secretInput struct {
	Purpose string `json:"purpose,omitempty"`
}

type secretOutput struct {
	Value string `json:"value,omitempty"`
	OK    bool   `json:"ok,omitempty"`
}

func TestContextSecretUsesGetterAndCachesBatch(t *testing.T) {
	calls := 0
	plugin := New(manifest.PluginManifest{Name: "test"}).WithSecretGetter(func(ctx Context, purpose string) (SecretMaterial, error) {
		calls++
		if ctx.Request.Plugin != "test" || ctx.Request.Instance != "work" || ctx.Request.Grant != "grant" {
			t.Fatalf("request scope = %#v", ctx.Request)
		}
		return SecretMaterial{Value: "secret-" + purpose, Source: "test"}, nil
	})
	Operation(plugin, manifest.OperationSpec{Name: "test.secret"}, func(ctx Context, input secretInput) (secretOutput, error) {
		first, err := ctx.Secret(input.Purpose)
		if err != nil {
			return secretOutput{}, err
		}
		second, err := ctx.Secret(input.Purpose)
		if err != nil {
			return secretOutput{}, err
		}
		if first.Value != second.Value {
			t.Fatalf("cache returned different values: %#v %#v", first, second)
		}
		return secretOutput{Value: first.Value}, nil
	})

	req := request(t, protocol.CommandOperationsBatch, protocol.OperationBatch{Calls: []protocol.OperationCall{
		operationCall(t, "test.secret", map[string]any{"purpose": "token"}),
		operationCall(t, "test.secret", map[string]any{"purpose": "token"}),
	}})
	req.Instance = "work"
	req.Grant = "grant"
	resp := plugin.Handle(req)
	if !resp.OK {
		t.Fatalf("batch failed: %#v", resp.Error)
	}
	if calls != 1 {
		t.Fatalf("secret getter calls = %d", calls)
	}
}

func TestContextOptionalSecretReturnsFalseForMissingOrEmpty(t *testing.T) {
	plugin := New(manifest.PluginManifest{Name: "test"}).WithSecretGetter(func(Context, string) (SecretMaterial, error) {
		return SecretMaterial{}, errors.New("missing")
	})
	Operation(plugin, manifest.OperationSpec{Name: "test.optional"}, func(ctx Context, _ struct{}) (secretOutput, error) {
		_, ok := ctx.OptionalSecret("token")
		return secretOutput{OK: ok}, nil
	})

	resp := plugin.Handle(request(t, protocol.CommandOperationsCall, protocol.OperationCall{Name: "test.optional"}))
	if !resp.OK {
		t.Fatalf("operation failed: %#v", resp.Error)
	}
	var out secretOutput
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	if out.OK {
		t.Fatalf("optional secret should be unavailable")
	}
}

func TestContextRequiredSecretsRejectsEmptyValue(t *testing.T) {
	plugin := New(manifest.PluginManifest{Name: "test"}).WithSecretGetter(func(Context, string) (SecretMaterial, error) {
		return SecretMaterial{}, nil
	})
	Operation(plugin, manifest.OperationSpec{Name: "test.required"}, func(ctx Context, _ struct{}) (secretOutput, error) {
		_, err := ctx.RequiredSecrets("token")
		return secretOutput{}, err
	})

	resp := plugin.Handle(request(t, protocol.CommandOperationsCall, protocol.OperationCall{Name: "test.required"}))
	if resp.OK || resp.Error == nil || resp.Error.Code != "secret" || resp.Error.Message != "token is empty" {
		t.Fatalf("response = %#v", resp)
	}
}

func TestReadWithPreferredSecretsFallsBackOnAllowedErrors(t *testing.T) {
	plugin := New(manifest.PluginManifest{Name: "test"}).WithSecretGetter(func(_ Context, purpose string) (SecretMaterial, error) {
		return SecretMaterial{Purpose: purpose, Value: purpose}, nil
	})
	ctx := Context{Request: protocol.Request{Plugin: "test", Instance: "default"}, Cache: NewCache(), plugin: plugin}
	var opened []string
	value, source, err := ReadWithPreferredSecrets[string, string](
		ctx,
		[]string{"user_token", "bot_token"},
		func(material SecretMaterial) (string, error) {
			opened = append(opened, material.Purpose)
			return material.Purpose, nil
		},
		func(client string, _ string) (string, error) {
			if client == "user_token" {
				return "", errors.New("missing_scope")
			}
			return "ok", nil
		},
		func(error) bool { return true },
	)
	if err != nil {
		t.Fatal(err)
	}
	if value != "ok" || source != "bot_token" || len(opened) != 2 {
		t.Fatalf("value=%q source=%q opened=%#v", value, source, opened)
	}
}

func TestReadWithPreferredSecretsStopsOnNonFallbackError(t *testing.T) {
	plugin := New(manifest.PluginManifest{Name: "test"}).WithSecretGetter(func(_ Context, purpose string) (SecretMaterial, error) {
		return SecretMaterial{Purpose: purpose, Value: purpose}, nil
	})
	ctx := Context{Request: protocol.Request{Plugin: "test", Instance: "default"}, Cache: NewCache(), plugin: plugin}
	_, source, err := ReadWithPreferredSecrets[string, string](
		ctx,
		[]string{"user_token", "bot_token"},
		func(material SecretMaterial) (string, error) { return material.Purpose, nil },
		func(string, string) (string, error) { return "", errors.New("network down") },
		func(error) bool { return false },
	)
	if err == nil || source != "user_token" {
		t.Fatalf("source=%q err=%v", source, err)
	}
}

func TestReadWithPreferredAuthPurposesDoesNotReadSecrets(t *testing.T) {
	var opened []string
	value, source, err := ReadWithPreferredAuthPurposes[string, string](
		[]string{"user_token", "bot_token"},
		func(purpose string) (string, error) {
			opened = append(opened, purpose)
			return purpose, nil
		},
		func(client string, _ string) (string, error) {
			if client == "user_token" {
				return "", errors.New("missing_scope")
			}
			return "ok", nil
		},
		func(error) bool { return true },
	)
	if err != nil {
		t.Fatal(err)
	}
	if value != "ok" || source != "bot_token" {
		t.Fatalf("value=%q source=%q", value, source)
	}
	if strings.Join(opened, ",") != "user_token,bot_token" {
		t.Fatalf("opened=%#v", opened)
	}
}

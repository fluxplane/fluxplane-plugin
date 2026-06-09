package plugintest_test

import (
	"testing"

	"github.com/fluxplane/fluxplane-plugin/pluginbinding"
	"github.com/fluxplane/fluxplane-plugin/pluginbinding/plugintest"
)

func TestFieldErrorPropagatesStructuredDetail(t *testing.T) {
	spec := pluginbinding.TypedOperationSpec[struct{}, output]("test.fieldfail", "Fail with fields.", pluginbinding.ReadOnly())
	plugin := pluginbinding.Define(pluginbinding.ManifestSpec{Name: "test"},
		pluginbinding.RegisterOperation(spec, func(pluginbinding.Context, struct{}) (output, error) {
			return output{}, pluginbinding.FieldError(
				"invalid_input", "validation failed",
				map[string]string{"parent": "does not exist", "summary": "required"},
				"overall problem",
			)
		}),
	)

	err := plugintest.RunError(t, plugin, "test.fieldfail", map[string]any{})
	if err.Code != "invalid_input" || err.Message != "validation failed" {
		t.Fatalf("error = %#v", err)
	}
	if err.Fields["parent"] != "does not exist" || err.Fields["summary"] != "required" {
		t.Fatalf("field detail lost: %#v", err.Fields)
	}
	if len(err.Details) != 1 || err.Details[0] != "overall problem" {
		t.Fatalf("details lost: %#v", err.Details)
	}
}

func TestPlainErrorHasNoFields(t *testing.T) {
	spec := pluginbinding.TypedOperationSpec[struct{}, output]("test.plainfail", "Fail plainly.", pluginbinding.ReadOnly())
	plugin := pluginbinding.Define(pluginbinding.ManifestSpec{Name: "test"},
		pluginbinding.RegisterOperation(spec, func(pluginbinding.Context, struct{}) (output, error) {
			return output{}, pluginbinding.Fail("bad_input", "nope")
		}),
	)
	err := plugintest.RunError(t, plugin, "test.plainfail", map[string]any{})
	if err.Code != "bad_input" || len(err.Fields) != 0 || len(err.Details) != 0 {
		t.Fatalf("plain error should carry no structured detail: %#v", err)
	}
}

package cli

import (
	"testing"

	"github.com/fluxplane/fluxplane-plugin/management"
)

func TestDefaultInstance(t *testing.T) {
	if got := defaultInstance(); got != management.DefaultInstance {
		t.Fatalf("default = %q, want %q", got, management.DefaultInstance)
	}
	t.Setenv("FLUXPLANE_PLUGIN_INSTANCE", "work")
	if got := defaultInstance(); got != "work" {
		t.Fatalf("env override = %q, want work", got)
	}
}

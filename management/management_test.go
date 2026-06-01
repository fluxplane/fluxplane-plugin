package management

import "testing"

func TestRefJSONShape(t *testing.T) {
	ref := Ref{Name: "gitlab", Version: "v1.0.0"}
	if ref.Name != "gitlab" || ref.Version != "v1.0.0" {
		t.Fatalf("ref = %#v", ref)
	}
}

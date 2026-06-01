package fluxplaneplugin

import (
	"testing"

	"github.com/fluxplane/fluxplane-dex/core/pluginbinding"
)

func TestSystemHTTPRequestURLCombinesPathAndQuery(t *testing.T) {
	got, err := systemHTTPRequestURL(pluginbinding.HTTPRequest{
		URL:   "https://api.atlassian.com/ex/jira/cloud-123",
		Path:  "/rest/api/3/issue/DEV-479",
		Query: map[string][]string{"fields": {"summary", "status"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "https://api.atlassian.com/ex/jira/cloud-123/rest/api/3/issue/DEV-479?fields=summary&fields=status"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

func TestSystemHTTPRequestURLRejectsUnsafeURLs(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "relative", url: "/rest/api/3/myself"},
		{name: "missing host", url: "https:///rest/api/3/myself"},
		{name: "non http scheme", url: "file:///etc/passwd"},
		{name: "userinfo", url: "https://user:secret@example.com/rest/api/3/myself"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := systemHTTPRequestURL(pluginbinding.HTTPRequest{URL: tt.url}); err == nil {
				t.Fatalf("systemHTTPRequestURL(%q) = %q, want error", tt.url, got)
			}
		})
	}
}

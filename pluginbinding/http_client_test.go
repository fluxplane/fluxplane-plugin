package pluginbinding

import (
	"io"
	"net/http"
	"strings"
	"testing"

	manifest "github.com/fluxplane/fluxplane-plugin/manifest"
)

func TestHostHTTPClientUsesHostHTTP(t *testing.T) {
	host := &hostHTTPClientTestHost{}
	client := HostHTTPClient(host,
		HostHTTPClientAuth(HTTPAuthRequest{BearerTokenPurpose: "api_token"}),
		HostHTTPClientTimeout(1234),
		HostHTTPClientMaxBytes(5678),
	)
	req, err := http.NewRequest("POST", "https://gitlab.example.com/api/v4/user", strings.NewReader(`{"ok":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusAccepted || string(body) != `{"accepted":true}` {
		t.Fatalf("response = %d %s", resp.StatusCode, string(body))
	}
	if host.request.URL != req.URL.String() || host.request.Method != "POST" || string(host.request.Body) != `{"ok":true}` {
		t.Fatalf("request = %#v", host.request)
	}
	if host.request.Headers["Content-Type"] != "application/json" {
		t.Fatalf("headers = %#v", host.request.Headers)
	}
	if host.request.Auth == nil || host.request.Auth.BearerTokenPurpose != "api_token" {
		t.Fatalf("auth = %#v", host.request.Auth)
	}
	if host.request.TimeoutMS != 1234 || host.request.MaxBytes != 5678 {
		t.Fatalf("limits = %#v", host.request)
	}
}

func TestHostHTTPClientPreservesEndpointEscapedPath(t *testing.T) {
	host := &hostHTTPClientTestHost{}
	client := HostHTTPClient(host, HostHTTPClientEndpointRef("gitlab-dev"))
	req, err := http.NewRequest("GET", "https://gitlab.endpoint.local/api/v4/projects/group%2Frepo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(req); err != nil {
		t.Fatal(err)
	}
	if host.request.Path != "/api/v4/projects/group%2Frepo" {
		t.Fatalf("path = %q", host.request.Path)
	}
}

func TestHostHTTPClientCanRouteThroughEndpointRef(t *testing.T) {
	host := &hostHTTPClientTestHost{}
	client := HostHTTPClient(host,
		HostHTTPClientEndpointRef("gitlab-dev"),
		HostHTTPClientAuth(HTTPAuthRequest{BearerTokenPurpose: "access_token"}),
	)
	req, err := http.NewRequest("GET", "https://gitlab.endpoint.local/api/v4/user?sudo=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(req); err != nil {
		t.Fatal(err)
	}
	if host.request.URL != "" || host.request.EndpointRef != "gitlab-dev" {
		t.Fatalf("request endpoint = %#v", host.request)
	}
	if host.request.Path != "/api/v4/user" || host.request.Query["sudo"][0] != "1" {
		t.Fatalf("request target = %#v", host.request)
	}
}

type hostHTTPClientTestHost struct {
	request HTTPRequest
}

func (h *hostHTTPClientTestHost) Secret(string) (SecretMaterial, error) {
	return SecretMaterial{}, nil
}

func (h *hostHTTPClientTestHost) Lookup(DatasourceLookupInput) (DatasourceLookupResult[LookupMatch[any]], error) {
	return DatasourceLookupResult[LookupMatch[any]]{}, nil
}

func (h *hostHTTPClientTestHost) Search(DatasourceSearchInput) (DatasourceSearchResult[any], error) {
	return DatasourceSearchResult[any]{}, nil
}

func (h *hostHTTPClientTestHost) Get(DatasourceGetInput) (DatasourceGetResult[any], error) {
	return DatasourceGetResult[any]{}, nil
}

func (h *hostHTTPClientTestHost) ResolveEndpoint(string) (manifest.EndpointRef, error) {
	return manifest.EndpointRef{}, nil
}

func (h *hostHTTPClientTestHost) HTTP(input HTTPRequest) (HTTPResponse, error) {
	h.request = input
	return HTTPResponse{
		Status:     "202 Accepted",
		StatusCode: http.StatusAccepted,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"accepted":true}`),
	}, nil
}

func (h *hostHTTPClientTestHost) BlobRead(BlobReadRequest) (BlobReadResponse, error) {
	return BlobReadResponse{}, nil
}

func (h *hostHTTPClientTestHost) BlobWrite(BlobWriteRequest) (BlobRef, error) {
	return BlobRef{}, nil
}

func (h *hostHTTPClientTestHost) BlobInfo(BlobInfoRequest) (BlobRef, error) {
	return BlobRef{}, nil
}

func (h *hostHTTPClientTestHost) EnvLookup(string) (EnvLookupResponse, error) {
	return EnvLookupResponse{}, nil
}

func (h *hostHTTPClientTestHost) ProcessRun(ProcessRunRequest) (ProcessRunResponse, error) {
	return ProcessRunResponse{}, nil
}

func (h *hostHTTPClientTestHost) CapabilityCall(ProviderCallRequest) (ProviderCallResponse, error) {
	return ProviderCallResponse{}, nil
}

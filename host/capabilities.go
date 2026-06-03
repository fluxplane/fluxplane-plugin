// Package host exposes plugin SDK host-capability contracts.
package host

import "encoding/json"

const (
	CapabilityHTTP      = "http"
	CapabilityBlobRead  = "blob.read"
	CapabilityBlobWrite = "blob.write"
	CapabilityEnvLookup = "env.lookup"
	CapabilityProcess   = "process.run"
	CapabilityProvider  = "provider.call"
)

type HTTPRequest struct {
	URL         string              `json:"url,omitempty"`
	EndpointRef string              `json:"endpoint_ref,omitempty"`
	Path        string              `json:"path,omitempty"`
	Query       map[string][]string `json:"query,omitempty"`
	Method      string              `json:"method,omitempty"`
	Headers     map[string]string   `json:"headers,omitempty"`
	Body        []byte              `json:"body,omitempty"`
	Auth        *HTTPAuthRequest    `json:"auth,omitempty"`
	TimeoutMS   int                 `json:"timeout_ms,omitempty"`
	MaxBytes    int                 `json:"max_bytes,omitempty"`
	UserAgent   string              `json:"user_agent,omitempty"`
}

type HTTPAuthRequest struct {
	BearerTokenPurpose string            `json:"bearer_token_purpose,omitempty"`
	UsernamePurpose    string            `json:"username_purpose,omitempty"`
	PasswordPurpose    string            `json:"password_purpose,omitempty"`
	HeaderPurposes     map[string]string `json:"header_purposes,omitempty"`
}

type HTTPResponse struct {
	URL         string              `json:"url"`
	FinalURL    string              `json:"final_url,omitempty"`
	Method      string              `json:"method,omitempty"`
	Status      string              `json:"status,omitempty"`
	StatusCode  int                 `json:"status_code,omitempty"`
	Headers     map[string][]string `json:"headers,omitempty"`
	ContentType string              `json:"content_type,omitempty"`
	Body        []byte              `json:"body,omitempty"`
	Truncated   bool                `json:"truncated,omitempty"`
	DurationMS  int64               `json:"duration_ms,omitempty"`
}

type BlobRef struct {
	Ref       string            `json:"ref"`
	Path      string            `json:"path,omitempty"`
	MediaType string            `json:"media_type,omitempty"`
	Filename  string            `json:"filename,omitempty"`
	Size      int64             `json:"size,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type BlobReadRequest struct {
	Ref      string `json:"ref,omitempty"`
	Path     string `json:"path,omitempty"`
	MaxBytes int64  `json:"max_bytes,omitempty"`
}

type BlobReadResponse struct {
	Blob      BlobRef `json:"blob"`
	Content   []byte  `json:"content,omitempty"`
	Truncated bool    `json:"truncated,omitempty"`
}

type BlobWriteRequest struct {
	Ref       string            `json:"ref,omitempty"`
	Path      string            `json:"path,omitempty"`
	Content   []byte            `json:"content,omitempty"`
	MediaType string            `json:"media_type,omitempty"`
	Filename  string            `json:"filename,omitempty"`
	Overwrite bool              `json:"overwrite,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type BlobInfoRequest struct {
	Ref  string `json:"ref,omitempty"`
	Path string `json:"path,omitempty"`
}

type EnvLookupRequest struct {
	Key string `json:"key"`
}

type EnvLookupResponse struct {
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
	Found bool   `json:"found"`
}

type ProcessRunRequest struct {
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Workdir   string            `json:"workdir,omitempty"`
	Env       []string          `json:"env,omitempty"`
	TimeoutMS int               `json:"timeout_ms,omitempty"`
	MaxStdout int64             `json:"max_stdout,omitempty"`
	MaxStderr int64             `json:"max_stderr,omitempty"`
	Label     string            `json:"label,omitempty"`
	Group     string            `json:"group,omitempty"`
	Tags      []string          `json:"tags,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type ProcessRunResponse struct {
	Command         string   `json:"command"`
	Args            []string `json:"args,omitempty"`
	Workdir         string   `json:"workdir,omitempty"`
	ExitCode        int      `json:"exit_code"`
	TimedOut        bool     `json:"timed_out,omitempty"`
	DurationMS      int64    `json:"duration_ms,omitempty"`
	Stdout          string   `json:"stdout,omitempty"`
	Stderr          string   `json:"stderr,omitempty"`
	StdoutTruncated bool     `json:"stdout_truncated,omitempty"`
	StderrTruncated bool     `json:"stderr_truncated,omitempty"`
}

type ProviderCallRequest struct {
	Provider string          `json:"provider"`
	Action   string          `json:"action"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

type ProviderCallResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
}

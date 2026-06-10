// Package host exposes plugin SDK host-capability contracts.
package host

import (
	"encoding/json"
	"time"
)

const (
	CapabilityHTTP      = "http"
	CapabilityBlobRead  = "blob.read"
	CapabilityBlobWrite = "blob.write"
	CapabilityEnvLookup = "env.lookup"
	CapabilityProcess   = "process.run"
	CapabilityProvider  = "provider.call"
	CapabilityConn      = "conn.dial"
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

type ProcessStartRequest struct {
	ID        string            `json:"id,omitempty"`
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Workdir   string            `json:"workdir,omitempty"`
	Env       []string          `json:"env,omitempty"`
	Label     string            `json:"label,omitempty"`
	Group     string            `json:"group,omitempty"`
	Tags      []string          `json:"tags,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	LogPath   string            `json:"log_path,omitempty"`
	StartedOK string            `json:"started_ok,omitempty"`
	TimeoutMS int               `json:"timeout_ms,omitempty"`
}

type ProcessStartResponse struct {
	ID           string    `json:"id"`
	Command      string    `json:"command"`
	Args         []string  `json:"args,omitempty"`
	Workdir      string    `json:"workdir,omitempty"`
	PID          int       `json:"pid,omitempty"`
	ProcessGroup int       `json:"process_group,omitempty"`
	LogPath      string    `json:"log_path,omitempty"`
	StartedAt    time.Time `json:"started_at,omitempty"`
}

type ProcessStopRequest struct {
	ID           string `json:"id,omitempty"`
	PID          int    `json:"pid,omitempty"`
	ProcessGroup int    `json:"process_group,omitempty"`
	Signal       string `json:"signal,omitempty"`
}

type ProcessStopResponse struct {
	ID      string `json:"id,omitempty"`
	Stopped bool   `json:"stopped"`
	Signal  string `json:"signal,omitempty"`
	Error   string `json:"error,omitempty"`
}

// ProcessListRequest filters the host's started-process records. Empty fields
// match everything; Group/Label match exactly.
type ProcessListRequest struct {
	Group string `json:"group,omitempty"`
	Label string `json:"label,omitempty"`
}

// ProcessRecord is one host-managed background process started via
// ProcessStart. Alive reports whether the PID still exists at list time, so a
// caller can tell a running forward/tunnel from a dead record.
type ProcessRecord struct {
	ID        string            `json:"id"`
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Workdir   string            `json:"workdir,omitempty"`
	PID       int               `json:"pid,omitempty"`
	Group     string            `json:"group,omitempty"`
	Label     string            `json:"label,omitempty"`
	Tags      []string          `json:"tags,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	LogPath   string            `json:"log_path,omitempty"`
	StartedAt time.Time         `json:"started_at,omitempty"`
	Alive     bool              `json:"alive"`
}

type ProcessListResponse struct {
	Processes []ProcessRecord `json:"processes"`
	Count     int             `json:"count"`
}

type ProviderCallRequest struct {
	Provider string          `json:"provider"`
	Action   string          `json:"action"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

type ProviderCallResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
}

// ConnTLS configures optional host-terminated TLS for a dialed connection. When
// Enabled, the host wraps the raw connection in a TLS client and the plugin
// reads/writes plaintext. Most plugins leave this nil and let their own library
// (database/sql driver, client-go, docker SDK) negotiate TLS over the raw stream.
type ConnTLS struct {
	Enabled            bool   `json:"enabled,omitempty"`
	ServerName         string `json:"server_name,omitempty"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty"`
}

// ConnDialRequest asks the host to open a byte stream to a tcp host:port or a
// unix socket path. The host owns the actual syscall so every connection crosses
// the safety boundary; the plugin speaks its own protocol over the returned
// handle. Address may be supplied directly or resolved from a registered
// endpoint via EndpointRef (never from the environment at call time).
type ConnDialRequest struct {
	Network     string   `json:"network"`           // "tcp" | "unix"
	Address     string   `json:"address,omitempty"` // host:port or socket path
	EndpointRef string   `json:"endpoint_ref,omitempty"`
	TLS         *ConnTLS `json:"tls,omitempty"`
	TimeoutMS   int      `json:"timeout_ms,omitempty"`
}

type ConnDialResponse struct {
	ID         string `json:"id"`
	Network    string `json:"network,omitempty"`
	LocalAddr  string `json:"local_addr,omitempty"`
	RemoteAddr string `json:"remote_addr,omitempty"`
}

type ConnReadRequest struct {
	ID        string `json:"id"`
	MaxBytes  int    `json:"max_bytes,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type ConnReadResponse struct {
	Data []byte `json:"data,omitempty"`
	EOF  bool   `json:"eof,omitempty"`
}

type ConnWriteRequest struct {
	ID        string `json:"id"`
	Data      []byte `json:"data,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type ConnWriteResponse struct {
	Written int `json:"written"`
}

type ConnCloseRequest struct {
	ID string `json:"id"`
}

type ConnCloseResponse struct {
	Closed bool `json:"closed"`
}

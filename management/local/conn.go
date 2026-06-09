package local

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	sdkhost "github.com/fluxplane/fluxplane-plugin/host"
)

const (
	connDialDefaultTimeout = 30 * time.Second
	connReadDefaultBytes   = 64 * 1024
	connReadMaxBytes       = 4 * 1024 * 1024
)

// connRegistry holds the live connections a plugin opened through the conn
// capability during a single invocation. It is created per invokePlugin and
// closed when the invocation returns, so no socket outlives the plugin call.
type connRegistry struct {
	mu    sync.Mutex
	seq   int
	conns map[string]net.Conn
}

func newConnRegistry() *connRegistry {
	return &connRegistry{conns: map[string]net.Conn{}}
}

func (r *connRegistry) add(conn net.Conn) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	id := "conn-" + strconv.Itoa(r.seq)
	r.conns[id] = conn
	return id
}

func (r *connRegistry) get(id string) (net.Conn, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	conn, ok := r.conns[id]
	return conn, ok
}

func (r *connRegistry) remove(id string) (net.Conn, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	conn, ok := r.conns[id]
	if ok {
		delete(r.conns, id)
	}
	return conn, ok
}

func (r *connRegistry) closeAll() {
	if r == nil {
		return
	}
	r.mu.Lock()
	conns := r.conns
	r.conns = map[string]net.Conn{}
	r.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func (h cliHost) connDial(req sdkhost.ConnDialRequest) (json.RawMessage, error) {
	if h.conns == nil {
		return nil, fmt.Errorf("conn capability is unavailable")
	}
	network := strings.TrimSpace(req.Network)
	if network == "" {
		network = "tcp"
	}
	switch network {
	case "tcp", "tcp4", "tcp6", "unix":
	default:
		return nil, fmt.Errorf("unsupported conn network %q", network)
	}
	address := strings.TrimSpace(req.Address)
	if address == "" && strings.TrimSpace(req.EndpointRef) != "" {
		resolved, err := h.dialAddressFromEndpoint(req.EndpointRef)
		if err != nil {
			return nil, err
		}
		address = resolved
	}
	if address == "" {
		return nil, fmt.Errorf("conn dial requires an address")
	}
	timeout := connDialDefaultTimeout
	if req.TimeoutMS > 0 {
		timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	conn, err := net.DialTimeout(network, address, timeout)
	if err != nil {
		return nil, err
	}
	if req.TLS != nil && req.TLS.Enabled {
		serverName := strings.TrimSpace(req.TLS.ServerName)
		if serverName == "" {
			if host, _, splitErr := net.SplitHostPort(address); splitErr == nil {
				serverName = host
			}
		}
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName:         serverName,
			InsecureSkipVerify: req.TLS.InsecureSkipVerify, //nolint:gosec // opt-in per dial request
		})
		if err := tlsConn.SetDeadline(time.Now().Add(timeout)); err == nil {
			if hErr := tlsConn.Handshake(); hErr != nil {
				_ = tlsConn.Close()
				return nil, hErr
			}
			_ = tlsConn.SetDeadline(time.Time{})
		}
		conn = tlsConn
	}
	id := h.conns.add(conn)
	resp := sdkhost.ConnDialResponse{ID: id, Network: network}
	if la := conn.LocalAddr(); la != nil {
		resp.LocalAddr = la.String()
	}
	if ra := conn.RemoteAddr(); ra != nil {
		resp.RemoteAddr = ra.String()
	}
	return json.Marshal(resp)
}

// dialAddressFromEndpoint turns a registered endpoint ref into a host:port (or
// socket path) suitable for dialing. Resolution comes from stored state, never
// the environment.
func (h cliHost) dialAddressFromEndpoint(ref string) (string, error) {
	endpoint, err := h.endpointRef(strings.TrimSpace(ref))
	if err != nil {
		return "", err
	}
	raw := strings.TrimSpace(endpoint.URL)
	if raw == "" {
		return "", fmt.Errorf("endpoint %q has no url to dial", ref)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return raw, nil // treat as a bare host:port or socket path
	}
	if parsed.Port() != "" {
		return parsed.Host, nil
	}
	port := "443"
	if strings.EqualFold(parsed.Scheme, "http") {
		port = "80"
	}
	return net.JoinHostPort(parsed.Hostname(), port), nil
}

func (h cliHost) connRead(req sdkhost.ConnReadRequest) (json.RawMessage, error) {
	conn, ok := h.conns.get(strings.TrimSpace(req.ID))
	if !ok {
		return nil, fmt.Errorf("conn %q is not open", req.ID)
	}
	max := req.MaxBytes
	if max <= 0 {
		max = connReadDefaultBytes
	}
	if max > connReadMaxBytes {
		max = connReadMaxBytes
	}
	if req.TimeoutMS > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(time.Duration(req.TimeoutMS) * time.Millisecond))
	} else {
		_ = conn.SetReadDeadline(time.Time{})
	}
	buf := make([]byte, max)
	n, err := conn.Read(buf)
	resp := sdkhost.ConnReadResponse{Data: buf[:n]}
	if err != nil {
		if err == io.EOF {
			resp.EOF = true
			return json.Marshal(resp)
		}
		if n > 0 {
			// Return the bytes we got; the next read surfaces the error.
			return json.Marshal(resp)
		}
		return nil, err
	}
	return json.Marshal(resp)
}

func (h cliHost) connWrite(req sdkhost.ConnWriteRequest) (json.RawMessage, error) {
	conn, ok := h.conns.get(strings.TrimSpace(req.ID))
	if !ok {
		return nil, fmt.Errorf("conn %q is not open", req.ID)
	}
	if req.TimeoutMS > 0 {
		_ = conn.SetWriteDeadline(time.Now().Add(time.Duration(req.TimeoutMS) * time.Millisecond))
	} else {
		_ = conn.SetWriteDeadline(time.Time{})
	}
	n, err := conn.Write(req.Data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(sdkhost.ConnWriteResponse{Written: n})
}

func (h cliHost) connClose(req sdkhost.ConnCloseRequest) (json.RawMessage, error) {
	conn, ok := h.conns.remove(strings.TrimSpace(req.ID))
	if !ok {
		return json.Marshal(sdkhost.ConnCloseResponse{Closed: false})
	}
	err := conn.Close()
	if err != nil {
		return nil, err
	}
	return json.Marshal(sdkhost.ConnCloseResponse{Closed: true})
}

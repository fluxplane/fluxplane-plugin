package pluginbinding

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// HostDialFunc matches the custom-dialer signature accepted by database/sql
// drivers, client-go's rest.Config.Dial, and the docker SDK's WithDialContext.
type HostDialFunc = func(ctx context.Context, network, address string) (net.Conn, error)

// HostDialer returns a dialer that opens connections through the host's conn
// capability, so a plugin can hand it to any library accepting a custom dialer
// (database/sql, client-go, docker SDK, an AMI client) without performing raw
// network IO itself — every byte still crosses the host safety boundary. When
// the host does not advertise the conn capability the returned dialer fails on
// use rather than at construction, mirroring how HostHTTPClient degrades.
func HostDialer(host HostClient) HostDialFunc {
	dialer, ok := host.(ConnDialer)
	if !ok || host == nil {
		return func(context.Context, string, string) (net.Conn, error) {
			return nil, fmt.Errorf("host does not support the conn dial capability")
		}
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		return DialHostConn(ctx, dialer, ConnDialRequest{Network: network, Address: address})
	}
}

// DialHostConn opens a connection described by req through the host and returns
// it as a net.Conn. Use this directly when a plugin needs endpoint-ref-based
// addressing or host-terminated TLS (ConnDialRequest.TLS); otherwise prefer
// HostDialer for the common (network, address) dialer shape.
func DialHostConn(ctx context.Context, dialer ConnDialer, req ConnDialRequest) (net.Conn, error) {
	if dialer == nil {
		return nil, fmt.Errorf("host does not support the conn dial capability")
	}
	if req.TimeoutMS == 0 {
		if deadline, ok := ctx.Deadline(); ok {
			if ms := int(time.Until(deadline) / time.Millisecond); ms > 0 {
				req.TimeoutMS = ms
			}
		}
	}
	resp, err := dialer.ConnDial(req)
	if err != nil {
		return nil, err
	}
	network := resp.Network
	if network == "" {
		network = req.Network
	}
	return &hostConn{
		dialer:     dialer,
		id:         resp.ID,
		localAddr:  connAddr{network: network, addr: resp.LocalAddr},
		remoteAddr: connAddr{network: network, addr: addrOr(resp.RemoteAddr, req.Address)},
	}, nil
}

func addrOr(primary, fallback string) string {
	if primary != "" {
		return primary
	}
	return fallback
}

// hostConn is a net.Conn backed by the host conn capability. Reads and writes
// are individual host round-trips, so it models request/response protocols
// (HTTP, SQL wire, AMI) well; it is not intended for high-throughput duplex
// streaming.
type hostConn struct {
	dialer     ConnDialer
	id         string
	localAddr  net.Addr
	remoteAddr net.Addr

	mu       sync.Mutex
	readDDL  time.Time
	writeDDL time.Time
	closed   bool
}

func (c *hostConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, net.ErrClosed
	}
	timeout, expired := deadlineTimeoutMS(c.readDDL)
	c.mu.Unlock()
	if expired {
		return 0, timeoutError{}
	}
	resp, err := c.dialer.ConnRead(ConnReadRequest{ID: c.id, MaxBytes: len(p), TimeoutMS: timeout})
	if err != nil {
		return 0, err
	}
	n := copy(p, resp.Data)
	if n < len(resp.Data) {
		// Defensive: host honored MaxBytes, so this should not happen. Surface
		// a clear error rather than silently dropping bytes.
		return n, fmt.Errorf("host conn read returned %d bytes for a %d-byte buffer", len(resp.Data), len(p))
	}
	if resp.EOF && n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

func (c *hostConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, net.ErrClosed
	}
	timeout, expired := deadlineTimeoutMS(c.writeDDL)
	c.mu.Unlock()
	if expired {
		return 0, timeoutError{}
	}
	resp, err := c.dialer.ConnWrite(ConnWriteRequest{ID: c.id, Data: p, TimeoutMS: timeout})
	if err != nil {
		return resp.Written, err
	}
	if resp.Written < len(p) {
		return resp.Written, io.ErrShortWrite
	}
	return resp.Written, nil
}

func (c *hostConn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	_, err := c.dialer.ConnClose(ConnCloseRequest{ID: c.id})
	return err
}

func (c *hostConn) LocalAddr() net.Addr  { return c.localAddr }
func (c *hostConn) RemoteAddr() net.Addr { return c.remoteAddr }

func (c *hostConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDDL = t
	c.writeDDL = t
	c.mu.Unlock()
	return nil
}

func (c *hostConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDDL = t
	c.mu.Unlock()
	return nil
}

func (c *hostConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.writeDDL = t
	c.mu.Unlock()
	return nil
}

// deadlineTimeoutMS converts an absolute deadline into a per-call timeout in
// milliseconds. A zero deadline means "no timeout" (timeout 0). expired is true
// when the deadline is already in the past.
func deadlineTimeoutMS(deadline time.Time) (timeout int, expired bool) {
	if deadline.IsZero() {
		return 0, false
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0, true
	}
	ms := int(remaining / time.Millisecond)
	if ms <= 0 {
		ms = 1
	}
	return ms, false
}

type connAddr struct {
	network string
	addr    string
}

func (a connAddr) Network() string { return a.network }
func (a connAddr) String() string  { return a.addr }

type timeoutError struct{}

func (timeoutError) Error() string   { return "host conn deadline exceeded" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

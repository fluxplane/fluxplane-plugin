package pluginbinding

import (
	"context"
	"io"
	"net"
	"testing"
)

// fakeDialer implements ConnDialer by delegating to a real net.Conn, so the
// hostConn net.Conn wrapper is exercised over an actual loopback socket.
type fakeDialer struct{ conn net.Conn }

func (f *fakeDialer) ConnDial(ConnDialRequest) (ConnDialResponse, error) {
	return ConnDialResponse{ID: "conn-1", Network: "tcp"}, nil
}

func (f *fakeDialer) ConnRead(req ConnReadRequest) (ConnReadResponse, error) {
	buf := make([]byte, req.MaxBytes)
	n, err := f.conn.Read(buf)
	resp := ConnReadResponse{Data: buf[:n]}
	if err == io.EOF {
		resp.EOF = true
		return resp, nil
	}
	if err != nil && n == 0 {
		return ConnReadResponse{}, err
	}
	return resp, nil
}

func (f *fakeDialer) ConnWrite(req ConnWriteRequest) (ConnWriteResponse, error) {
	n, err := f.conn.Write(req.Data)
	return ConnWriteResponse{Written: n}, err
}

func (f *fakeDialer) ConnClose(ConnCloseRequest) (ConnCloseResponse, error) {
	return ConnCloseResponse{Closed: true}, f.conn.Close()
}

func TestDialHostConnRoundTrip(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = io.Copy(c, c)
	}()

	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial backing conn: %v", err)
	}

	conn, err := DialHostConn(context.Background(), &fakeDialer{conn: raw}, ConnDialRequest{Network: "tcp", Address: ln.Addr().String()})
	if err != nil {
		t.Fatalf("DialHostConn: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != "hello" {
		t.Fatalf("got %q, want %q", buf, "hello")
	}
}

func TestHostDialerWithoutCapability(t *testing.T) {
	// A HostClient that does not implement ConnDialer must yield a failing dialer.
	dialer := HostDialer(noConnHost{})
	if _, err := dialer(context.Background(), "tcp", "127.0.0.1:1"); err == nil {
		t.Fatal("expected dial to fail when host lacks conn capability")
	}
}

// noConnHost satisfies HostClient via the unavailable client's surface but is a
// distinct type that does NOT implement ConnDialer.
type noConnHost struct{ HostClient }

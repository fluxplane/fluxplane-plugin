package local

import (
	"encoding/json"
	"io"
	"net"
	"testing"

	sdkhost "github.com/fluxplane/fluxplane-plugin/host"
)

// TestCliHostConnRoundTrip exercises the conn capability end to end against a
// loopback echo server: dial, write, read back, close.
func TestCliHostConnRoundTrip(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(conn, conn) // echo
	}()

	h := cliHost{conns: newConnRegistry()}
	defer h.conns.closeAll()

	dialRaw, err := h.connDial(sdkhost.ConnDialRequest{Network: "tcp", Address: ln.Addr().String()})
	if err != nil {
		t.Fatalf("connDial: %v", err)
	}
	var dial sdkhost.ConnDialResponse
	if err := json.Unmarshal(dialRaw, &dial); err != nil {
		t.Fatalf("decode dial: %v", err)
	}
	if dial.ID == "" {
		t.Fatal("expected a conn id")
	}

	payload := []byte("ping-pong")
	writeRaw, err := h.connWrite(sdkhost.ConnWriteRequest{ID: dial.ID, Data: payload})
	if err != nil {
		t.Fatalf("connWrite: %v", err)
	}
	var write sdkhost.ConnWriteResponse
	if err := json.Unmarshal(writeRaw, &write); err != nil {
		t.Fatalf("decode write: %v", err)
	}
	if write.Written != len(payload) {
		t.Fatalf("wrote %d, want %d", write.Written, len(payload))
	}

	readRaw, err := h.connRead(sdkhost.ConnReadRequest{ID: dial.ID, MaxBytes: 64, TimeoutMS: 2000})
	if err != nil {
		t.Fatalf("connRead: %v", err)
	}
	var read sdkhost.ConnReadResponse
	if err := json.Unmarshal(readRaw, &read); err != nil {
		t.Fatalf("decode read: %v", err)
	}
	if string(read.Data) != string(payload) {
		t.Fatalf("read %q, want %q", read.Data, payload)
	}

	if _, err := h.connClose(sdkhost.ConnCloseRequest{ID: dial.ID}); err != nil {
		t.Fatalf("connClose: %v", err)
	}
	if _, ok := h.conns.get(dial.ID); ok {
		t.Fatal("conn should be removed after close")
	}
}

// TestCliHostConnRejectsUnknownNetwork guards the allow-list.
func TestCliHostConnRejectsUnknownNetwork(t *testing.T) {
	h := cliHost{conns: newConnRegistry()}
	if _, err := h.connDial(sdkhost.ConnDialRequest{Network: "udp", Address: "127.0.0.1:1"}); err == nil {
		t.Fatal("expected udp to be rejected")
	}
}

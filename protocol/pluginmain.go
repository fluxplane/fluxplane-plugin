package protocol

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
)

type HostCaller interface {
	CallHost(command string, payload any) (json.RawMessage, error)
	EmitHostEvent(event string, payload any) error
}

type Handler func(Request, HostCaller) Response

type HostError struct {
	Code    string
	Message string
}

func (e HostError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

type frameHostClient struct {
	enc     *json.Encoder
	dec     *json.Decoder
	writeMu sync.Mutex
	nextID  atomic.Uint64
}

func (c *frameHostClient) CallHost(command string, payload any) (json.RawMessage, error) {
	if c == nil {
		return nil, HostError{Code: "host_unavailable", Message: "host client is unavailable"}
	}
	id := "host-" + strconv.FormatUint(c.nextID.Add(1), 10)
	frame, err := NewRequestFrame(id, TargetHost, command, payload)
	if err != nil {
		return nil, err
	}
	c.writeMu.Lock()
	err = c.enc.Encode(frame)
	c.writeMu.Unlock()
	if err != nil {
		return nil, err
	}
	for {
		var resp Frame
		if err := c.dec.Decode(&resp); err != nil {
			return nil, err
		}
		if resp.Protocol != Version {
			return nil, HostError{Code: "protocol_mismatch", Message: fmt.Sprintf("expected %s, got %s", Version, resp.Protocol)}
		}
		if resp.Type != FrameResponse || resp.ID != id {
			return nil, HostError{Code: "unexpected_frame", Message: "unexpected host frame"}
		}
		if !resp.OK {
			if resp.Error != nil {
				return nil, HostError{Code: resp.Error.Code, Message: resp.Error.Message}
			}
			return nil, HostError{Code: "host_error", Message: "host call failed"}
		}
		return resp.Result, nil
	}
}

func (c *frameHostClient) EmitHostEvent(event string, payload any) error {
	if c == nil {
		return HostError{Code: "host_unavailable", Message: "host client is unavailable"}
	}
	id := "event-" + strconv.FormatUint(c.nextID.Add(1), 10)
	frame, err := NewEventFrame(id, TargetHost, event, payload)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.enc.Encode(frame)
}

func Serve(handler Handler) {
	if handler == nil {
		writeFrame(NewResponseFrame("", Fail("plugin_error", "handler is nil")))
		os.Exit(1)
	}
	reader := bufio.NewReader(os.Stdin)
	dec := json.NewDecoder(reader)
	enc := json.NewEncoder(os.Stdout)
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		writeFrame(NewResponseFrame("", Fail("bad_request", err.Error())))
		os.Exit(1)
	}
	var frame Frame
	if err := json.Unmarshal(raw, &frame); err == nil && frame.Type != "" {
		if frame.Protocol != Version {
			writeFrame(NewResponseFrame(frame.ID, Fail("protocol_mismatch", fmt.Sprintf("expected %s, got %s", Version, frame.Protocol))))
			os.Exit(1)
		}
		if frame.Type != FrameRequest || frame.Target != TargetPlugin {
			writeFrame(NewResponseFrame(frame.ID, Fail("bad_request", "expected plugin request frame")))
			os.Exit(1)
		}
		var req Request
		if err := json.Unmarshal(frame.Payload, &req); err != nil {
			writeFrame(NewResponseFrame(frame.ID, Fail("bad_request", err.Error())))
			os.Exit(1)
		}
		if req.Protocol != Version {
			writeFrame(NewResponseFrame(frame.ID, Fail("protocol_mismatch", fmt.Sprintf("expected %s, got %s", Version, req.Protocol))))
			os.Exit(1)
		}
		resp := handler(req, &frameHostClient{enc: enc, dec: dec})
		if resp.Protocol == "" {
			resp.Protocol = Version
		}
		_ = enc.Encode(NewResponseFrame(frame.ID, resp))
		return
	}
	var req Request
	if err := json.Unmarshal(raw, &req); err == nil && req.Command != "" {
		if req.Protocol != Version && req.Protocol != VersionV1 {
			writeResponse(Fail("protocol_mismatch", fmt.Sprintf("expected %s, got %s", Version, req.Protocol)))
			os.Exit(1)
		}
		resp := handler(req, nil)
		if resp.Protocol == "" {
			resp.Protocol = req.Protocol
		}
		writeResponse(resp)
		return
	}
	if err := json.Unmarshal(raw, &frame); err != nil {
		writeFrame(NewResponseFrame("", Fail("bad_request", err.Error())))
		os.Exit(1)
	}
	writeFrame(NewResponseFrame(frame.ID, Fail("bad_request", "expected plugin request")))
}

func writeResponse(resp Response) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(resp)
}

func writeFrame(frame Frame) {
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(frame)
}

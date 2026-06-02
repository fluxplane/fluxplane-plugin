package pluginbinding

import (
	"fmt"
	"strings"

	"github.com/fluxplane/fluxplane-plugin/protocol"
)

const (
	EventStatus   = "status"
	EventProgress = "progress"
)

type EventSink interface {
	Emit(event string, payload any) error
}

type StatusEvent struct {
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type ProgressEvent struct {
	Message string `json:"message,omitempty"`
	Current int    `json:"current,omitempty"`
	Total   int    `json:"total,omitempty"`
	Data    any    `json:"data,omitempty"`
}

type hostEventSink struct {
	caller protocol.HostCaller
}

type unavailableEventSink struct{}

func newEventSink(caller protocol.HostCaller) EventSink {
	if caller == nil {
		return unavailableEventSink{}
	}
	return hostEventSink{caller: caller}
}

func (s hostEventSink) Emit(event string, payload any) error {
	event = strings.TrimSpace(event)
	if event == "" {
		return fmt.Errorf("event is required")
	}
	return s.caller.EmitHostEvent(event, payload)
}

func (unavailableEventSink) Emit(string, any) error {
	return nil
}

func (ctx Context) Emit(event string, payload any) error {
	if ctx.Events == nil {
		return nil
	}
	return ctx.Events.Emit(event, payload)
}

func (ctx Context) Status(message string, data any) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	return ctx.Emit(EventStatus, StatusEvent{Message: message, Data: data})
}

func (ctx Context) Progress(message string, current, total int, data any) error {
	return ctx.Emit(EventProgress, ProgressEvent{Message: strings.TrimSpace(message), Current: current, Total: total, Data: data})
}

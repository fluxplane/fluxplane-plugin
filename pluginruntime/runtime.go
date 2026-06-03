// Package pluginruntime hosts Fluxplane plugins without depending on Dex or Core.
package pluginruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	sdkhost "github.com/fluxplane/fluxplane-plugin/host"
	manifest "github.com/fluxplane/fluxplane-plugin/manifest"
	"github.com/fluxplane/fluxplane-plugin/pluginbinding"
	"github.com/fluxplane/fluxplane-plugin/protocol"
)

const DefaultOutputLimit = 4 * 1024 * 1024

type ProtocolMode string

const (
	ProtocolModeFramed ProtocolMode = "framed"
	ProtocolModeLegacy ProtocolMode = "legacy"
)

type Plugin interface {
	Name() string
	Invoke(context.Context, protocol.Request, protocol.HostCaller) (protocol.Response, error)
}

type Host struct {
	mu      sync.RWMutex
	plugins map[string]Plugin
}

type InvokeConfig struct {
	Instance string
	Config   map[string]any
	Grant    string
	Host     protocol.HostCaller
}

type InvokeOption func(*InvokeConfig)

func NewHost(plugins ...Plugin) (*Host, error) {
	host := &Host{plugins: map[string]Plugin{}}
	for _, plugin := range plugins {
		if err := host.Register(plugin); err != nil {
			return nil, err
		}
	}
	return host, nil
}

func (h *Host) Register(plugin Plugin) error {
	if h == nil {
		return fmt.Errorf("pluginruntime: host is nil")
	}
	if plugin == nil {
		return fmt.Errorf("pluginruntime: plugin is nil")
	}
	name := strings.TrimSpace(plugin.Name())
	if name == "" {
		return fmt.Errorf("pluginruntime: plugin name is required")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.plugins == nil {
		h.plugins = map[string]Plugin{}
	}
	if _, exists := h.plugins[name]; exists {
		return fmt.Errorf("pluginruntime: plugin %q already registered", name)
	}
	h.plugins[name] = plugin
	return nil
}

func (h *Host) Plugins() []string {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	names := make([]string, 0, len(h.plugins))
	for name := range h.plugins {
		names = append(names, name)
	}
	return names
}

func (h *Host) Invoke(ctx context.Context, pluginName, command string, payload any, options ...InvokeOption) (protocol.Response, error) {
	plugin, err := h.plugin(pluginName)
	if err != nil {
		return protocol.Response{}, err
	}
	req, err := protocol.NewRequest(command, plugin.Name(), payload)
	if err != nil {
		return protocol.Response{}, err
	}
	var cfg InvokeConfig
	for _, option := range options {
		if option != nil {
			option(&cfg)
		}
	}
	req.Instance = strings.TrimSpace(cfg.Instance)
	req.Config = cloneConfig(cfg.Config)
	req.Grant = strings.TrimSpace(cfg.Grant)
	return plugin.Invoke(ctx, req, cfg.Host)
}

func (h *Host) Manifest(ctx context.Context, pluginName string, options ...InvokeOption) (manifest.PluginManifest, error) {
	resp, err := h.Invoke(ctx, pluginName, protocol.CommandManifest, nil, options...)
	if err != nil {
		return manifest.PluginManifest{}, err
	}
	if !resp.OK {
		return manifest.PluginManifest{}, responseError(resp)
	}
	return protocol.DecodePayload[manifest.PluginManifest](resp.Result)
}

func (h *Host) CallOperation(ctx context.Context, pluginName string, call protocol.OperationCall, options ...InvokeOption) (protocol.Response, error) {
	return h.Invoke(ctx, pluginName, protocol.CommandOperationsCall, call, options...)
}

func (h *Host) plugin(name string) (Plugin, error) {
	if h == nil {
		return nil, fmt.Errorf("pluginruntime: host is nil")
	}
	name = strings.TrimSpace(name)
	h.mu.RLock()
	defer h.mu.RUnlock()
	plugin, ok := h.plugins[name]
	if !ok {
		return nil, fmt.Errorf("pluginruntime: unknown plugin %q", name)
	}
	return plugin, nil
}

func WithInstance(instance string) InvokeOption {
	return func(cfg *InvokeConfig) {
		cfg.Instance = instance
	}
}

func WithConfig(config map[string]any) InvokeOption {
	return func(cfg *InvokeConfig) {
		cfg.Config = cloneConfig(config)
	}
}

func WithGrant(grant string) InvokeOption {
	return func(cfg *InvokeConfig) {
		cfg.Grant = grant
	}
}

func WithHostCaller(host protocol.HostCaller) InvokeOption {
	return func(cfg *InvokeConfig) {
		cfg.Host = host
	}
}

type DirectPlugin struct {
	Plugin *pluginbinding.Plugin
}

func Direct(plugin *pluginbinding.Plugin) DirectPlugin {
	return DirectPlugin{Plugin: plugin}
}

func (p DirectPlugin) Name() string {
	if p.Plugin == nil {
		return ""
	}
	return p.Plugin.Manifest().Name
}

func (p DirectPlugin) Invoke(ctx context.Context, req protocol.Request, caller protocol.HostCaller) (protocol.Response, error) {
	if p.Plugin == nil {
		return protocol.Response{}, fmt.Errorf("pluginruntime: direct plugin is nil")
	}
	req = normalizeRequest(req, p.Name(), protocol.Version)
	return p.Plugin.HandleWithContextHostAndEvents(ctx, req, sdkhost.NewClient(caller), callerEventSink{caller: caller}), nil
}

type StdioPlugin struct {
	NameValue   string
	Command     string
	Args        []string
	Env         []string
	Dir         string
	Mode        ProtocolMode
	OutputLimit int
	CleanEnv    bool
}

func Stdio(name, command string, args ...string) StdioPlugin {
	return StdioPlugin{NameValue: strings.TrimSpace(name), Command: strings.TrimSpace(command), Args: append([]string(nil), args...), Mode: ProtocolModeFramed}
}

func (p StdioPlugin) Name() string {
	return strings.TrimSpace(p.NameValue)
}

func (p StdioPlugin) Invoke(ctx context.Context, req protocol.Request, caller protocol.HostCaller) (protocol.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(p.Command) == "" {
		return protocol.Response{}, fmt.Errorf("pluginruntime: stdio command is required")
	}
	req = normalizeRequest(req, p.Name(), protocol.Version)
	switch p.mode() {
	case ProtocolModeLegacy:
		return p.invokeLegacy(ctx, req)
	default:
		return p.invokeFramed(ctx, req, caller)
	}
}

func (p StdioPlugin) mode() ProtocolMode {
	if p.Mode == "" {
		return ProtocolModeFramed
	}
	return p.Mode
}

func (p StdioPlugin) outputLimit() int {
	if p.OutputLimit <= 0 {
		return DefaultOutputLimit
	}
	return p.OutputLimit
}

func (p StdioPlugin) command(ctx context.Context) *exec.Cmd {
	cmd := exec.CommandContext(ctx, p.Command, p.Args...)
	if p.CleanEnv {
		cmd.Env = append([]string(nil), p.Env...)
	} else if len(p.Env) > 0 {
		cmd.Env = append(os.Environ(), p.Env...)
	}
	if strings.TrimSpace(p.Dir) != "" {
		cmd.Dir = p.Dir
	}
	return cmd
}

func (p StdioPlugin) invokeLegacy(ctx context.Context, req protocol.Request) (protocol.Response, error) {
	cmd := p.command(ctx)
	req.Protocol = protocol.VersionV1
	data, err := json.Marshal(req)
	if err != nil {
		return protocol.Response{}, err
	}
	cmd.Stdin = bytes.NewReader(data)
	var stdout, stderr limitedBuffer
	stdout.limit = p.outputLimit()
	stderr.limit = p.outputLimit()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stdout.truncated || stderr.truncated {
			return protocol.Response{}, fmt.Errorf("run plugin %s: plugin output exceeded %d bytes", p.Name(), p.outputLimit())
		}
		if stdout.Len() > 0 {
			return decodeResponse(stdout.Bytes())
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return protocol.Response{}, fmt.Errorf("run plugin %s: %s", p.Name(), msg)
	}
	if stdout.truncated || stderr.truncated {
		return protocol.Response{}, fmt.Errorf("run plugin %s: plugin output exceeded %d bytes", p.Name(), p.outputLimit())
	}
	return decodeResponse(stdout.Bytes())
}

func (p StdioPlugin) invokeFramed(ctx context.Context, req protocol.Request, caller protocol.HostCaller) (protocol.Response, error) {
	cmd := p.command(ctx)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return protocol.Response{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return protocol.Response{}, err
	}
	var stderr limitedBuffer
	stderr.limit = p.outputLimit()
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return protocol.Response{}, err
	}
	enc := json.NewEncoder(stdin)
	stdoutLimit := &limitedReadCloser{ReadCloser: stdout, remaining: p.outputLimit()}
	dec := json.NewDecoder(stdoutLimit)
	frame, err := protocol.NewRequestFrame("root", protocol.TargetPlugin, req.Command, req)
	if err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		return protocol.Response{}, err
	}
	if err := enc.Encode(frame); err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		return protocol.Response{}, err
	}
	for {
		var frame protocol.Frame
		if err := dec.Decode(&frame); err != nil {
			_ = stdin.Close()
			if stdoutLimit.exceeded || stderr.truncated {
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				_ = cmd.Wait()
				if stdoutLimit.exceeded {
					return protocol.Response{}, fmt.Errorf("run plugin %s: plugin stdout exceeded %d bytes", p.Name(), p.outputLimit())
				}
				return protocol.Response{}, fmt.Errorf("run plugin %s: plugin stderr exceeded %d bytes", p.Name(), p.outputLimit())
			}
			waitErr := cmd.Wait()
			msg := strings.TrimSpace(stderr.String())
			if msg == "" && waitErr != nil {
				msg = waitErr.Error()
			}
			if msg == "" {
				msg = err.Error()
			}
			return protocol.Response{}, fmt.Errorf("run plugin %s: %s", p.Name(), msg)
		}
		if frame.Protocol != protocol.Version {
			_ = stdin.Close()
			_ = cmd.Wait()
			return protocol.Response{}, fmt.Errorf("plugin protocol mismatch: %s", frame.Protocol)
		}
		switch frame.Type {
		case protocol.FrameResponse:
			if frame.ID != "root" {
				_ = stdin.Close()
				_ = cmd.Wait()
				return protocol.Response{}, fmt.Errorf("unexpected plugin response frame %q", frame.ID)
			}
			_ = stdin.Close()
			waitErr := cmd.Wait()
			if stderr.truncated {
				return protocol.Response{}, fmt.Errorf("run plugin %s: plugin stderr exceeded %d bytes", p.Name(), p.outputLimit())
			}
			resp := protocol.Response{Protocol: protocol.Version, OK: frame.OK, Result: frame.Result, Error: frame.Error}
			if waitErr != nil && resp.OK {
				msg := strings.TrimSpace(stderr.String())
				if msg == "" {
					msg = waitErr.Error()
				}
				return protocol.Response{}, fmt.Errorf("run plugin %s: %s", p.Name(), msg)
			}
			if !resp.OK && resp.Error != nil {
				return resp, fmt.Errorf("%s", resp.Error.Message)
			}
			return resp, nil
		case protocol.FrameRequest:
			resp := callHost(caller, frame)
			if err := enc.Encode(protocol.NewResponseFrame(frame.ID, resp)); err != nil {
				_ = stdin.Close()
				_ = cmd.Wait()
				return protocol.Response{}, err
			}
		case protocol.FrameEvent:
			if caller != nil {
				_ = caller.EmitHostEvent(strings.TrimSpace(frame.Command), frame.Payload)
			}
		default:
			_ = stdin.Close()
			_ = cmd.Wait()
			if frame.Type == "" {
				return protocol.Response{}, fmt.Errorf("plugin %s returned an unframed response on v2 framed protocol", p.Name())
			}
			return protocol.Response{}, fmt.Errorf("unexpected plugin frame type %q", frame.Type)
		}
	}
}

type callerEventSink struct {
	caller protocol.HostCaller
}

func (s callerEventSink) Emit(event string, payload any) error {
	if s.caller == nil {
		return nil
	}
	return s.caller.EmitHostEvent(event, payload)
}

func callHost(caller protocol.HostCaller, frame protocol.Frame) protocol.Response {
	if frame.Target != protocol.TargetHost {
		return protocol.Fail("bad_request", "plugin may only request host target")
	}
	if caller == nil {
		return protocol.Fail("host_unavailable", "host caller is unavailable")
	}
	raw, err := caller.CallHost(frame.Command, frame.Payload)
	if err != nil {
		return protocol.Fail("host_error", err.Error())
	}
	return protocol.Response{Protocol: protocol.Version, OK: true, Result: raw}
}

func normalizeRequest(req protocol.Request, plugin, version string) protocol.Request {
	if req.Protocol == "" {
		req.Protocol = version
	}
	if strings.TrimSpace(req.Plugin) == "" {
		req.Plugin = strings.TrimSpace(plugin)
	}
	return req
}

func cloneConfig(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func responseError(resp protocol.Response) error {
	if resp.Error == nil {
		return fmt.Errorf("plugin response failed")
	}
	if resp.Error.Code == "" {
		return fmt.Errorf("%s", resp.Error.Message)
	}
	return fmt.Errorf("%s: %s", resp.Error.Code, resp.Error.Message)
}

type limitedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return b.Buffer.Write(p)
	}
	remaining := b.limit - b.Buffer.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.Buffer.Write(p[:remaining])
		b.truncated = true
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

type limitedReadCloser struct {
	io.ReadCloser
	remaining int
	exceeded  bool
}

func (r *limitedReadCloser) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		r.exceeded = true
		return 0, io.EOF
	}
	if len(p) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.ReadCloser.Read(p)
	r.remaining -= n
	return n, err
}

func decodeResponse(data []byte) (protocol.Response, error) {
	var resp protocol.Response
	if err := json.Unmarshal(data, &resp); err != nil {
		return resp, fmt.Errorf("decode plugin response: %w", err)
	}
	if resp.Protocol == "" {
		resp.Protocol = protocol.VersionV1
	}
	if resp.Protocol != protocol.Version && resp.Protocol != protocol.VersionV1 {
		return resp, fmt.Errorf("plugin protocol mismatch: %s", resp.Protocol)
	}
	if !resp.OK && resp.Error != nil {
		return resp, fmt.Errorf("%s", resp.Error.Message)
	}
	return resp, nil
}

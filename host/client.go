package host

import (
	"encoding/json"
	"fmt"
	"strings"

	fpendpoint "github.com/fluxplane/fluxplane-endpoint"
	fpdatasource "github.com/fluxplane/fluxplane-plugin/datasource"
	"github.com/fluxplane/fluxplane-plugin/protocol"
)

const (
	ManifestProtocolKey = "fluxplane.plugin.protocol"
	SecretGetCommand    = "host.secret.get"
	IndexLookupCommand  = "host.index.lookup"
	IndexSearchCommand  = "host.index.search"
	IndexGetCommand     = "host.index.get"
	EndpointResolve     = "host.endpoint.resolve"
)

// Client is the SDK surface exposed by a Fluxplane host to a plugin.
type Client interface {
	Secret(purpose string) (SecretMaterial, error)
	Lookup(input fpdatasource.LookupInput) (fpdatasource.LookupResult[fpdatasource.LookupMatch[any]], error)
	Search(input fpdatasource.SearchInput) (fpdatasource.SearchResult[any], error)
	Get(input fpdatasource.GetInput) (fpdatasource.GetResult[any], error)
	ResolveEndpoint(ref string) (fpendpoint.EndpointRef, error)
	HTTP(input HTTPRequest) (HTTPResponse, error)
	BlobRead(input BlobReadRequest) (BlobReadResponse, error)
	BlobWrite(input BlobWriteRequest) (BlobRef, error)
	BlobInfo(input BlobInfoRequest) (BlobRef, error)
	EnvLookup(key string) (EnvLookupResponse, error)
	ProcessRun(input ProcessRunRequest) (ProcessRunResponse, error)
	ProcessStart(input ProcessStartRequest) (ProcessStartResponse, error)
	ProcessStop(input ProcessStopRequest) (ProcessStopResponse, error)
	CapabilityCall(input ProviderCallRequest) (ProviderCallResponse, error)
}

// ConnDialer is an optional host capability for raw byte-stream connections
// (tcp/unix, optional host-terminated TLS). It is kept separate from Client so
// hosts and test doubles that do not need outbound sockets are unaffected;
// callers type-assert a Client to ConnDialer (see pluginbinding.HostDialer).
// The SDK-provided Client always satisfies it.
type ConnDialer interface {
	ConnDial(input ConnDialRequest) (ConnDialResponse, error)
	ConnRead(input ConnReadRequest) (ConnReadResponse, error)
	ConnWrite(input ConnWriteRequest) (ConnWriteResponse, error)
	ConnClose(input ConnCloseRequest) (ConnCloseResponse, error)
}

// ProcessLister is an optional host capability for listing host-managed
// background processes started via ProcessStart. Kept separate from Client
// (like ConnDialer) so existing hosts and test doubles are unaffected;
// callers type-assert a Client to ProcessLister. The SDK-provided Client
// always satisfies it.
type ProcessLister interface {
	ProcessList(input ProcessListRequest) (ProcessListResponse, error)
}

type client struct {
	caller protocol.HostCaller
}

type unavailableClient struct{}

// NewClient adapts a protocol HostCaller to the higher-level host Client.
func NewClient(caller protocol.HostCaller) Client {
	if caller == nil {
		return unavailableClient{}
	}
	return client{caller: caller}
}

func (h client) Secret(purpose string) (SecretMaterial, error) {
	var out SecretMaterial
	err := h.call(SecretGetCommand, map[string]any{"purpose": strings.TrimSpace(purpose)}, &out)
	if out.Purpose == "" {
		out.Purpose = strings.TrimSpace(purpose)
	}
	return out, err
}

func (h client) Lookup(input fpdatasource.LookupInput) (fpdatasource.LookupResult[fpdatasource.LookupMatch[any]], error) {
	var out fpdatasource.LookupResult[fpdatasource.LookupMatch[any]]
	err := h.call(IndexLookupCommand, input, &out)
	return out, err
}

func (h client) Search(input fpdatasource.SearchInput) (fpdatasource.SearchResult[any], error) {
	var out fpdatasource.SearchResult[any]
	err := h.call(IndexSearchCommand, input, &out)
	return out, err
}

func (h client) Get(input fpdatasource.GetInput) (fpdatasource.GetResult[any], error) {
	var out fpdatasource.GetResult[any]
	err := h.call(IndexGetCommand, input, &out)
	return out, err
}

func (h client) ResolveEndpoint(ref string) (fpendpoint.EndpointRef, error) {
	var out fpendpoint.EndpointRef
	err := h.call(EndpointResolve, map[string]any{"endpoint_ref": strings.TrimSpace(ref)}, &out)
	return out, err
}

func (h client) HTTP(input HTTPRequest) (HTTPResponse, error) {
	var out HTTPResponse
	err := h.call(protocol.HostCapabilityHTTPDo, input, &out)
	return out, err
}

func (h client) BlobRead(input BlobReadRequest) (BlobReadResponse, error) {
	var out BlobReadResponse
	err := h.call(protocol.HostCapabilityBlobRead, input, &out)
	return out, err
}

func (h client) BlobWrite(input BlobWriteRequest) (BlobRef, error) {
	var out BlobRef
	err := h.call(protocol.HostCapabilityBlobWrite, input, &out)
	return out, err
}

func (h client) BlobInfo(input BlobInfoRequest) (BlobRef, error) {
	var out BlobRef
	err := h.call(protocol.HostCapabilityBlobInfo, input, &out)
	return out, err
}

func (h client) EnvLookup(key string) (EnvLookupResponse, error) {
	var out EnvLookupResponse
	err := h.call(protocol.HostCapabilityEnvLookup, EnvLookupRequest{Key: strings.TrimSpace(key)}, &out)
	return out, err
}

func (h client) ProcessRun(input ProcessRunRequest) (ProcessRunResponse, error) {
	var out ProcessRunResponse
	err := h.call(protocol.HostCapabilityProcessRun, input, &out)
	return out, err
}

func (h client) ProcessStart(input ProcessStartRequest) (ProcessStartResponse, error) {
	var out ProcessStartResponse
	err := h.call(protocol.HostCapabilityProcessStart, input, &out)
	return out, err
}

func (h client) ProcessList(input ProcessListRequest) (ProcessListResponse, error) {
	var out ProcessListResponse
	err := h.call(protocol.HostCapabilityProcessList, input, &out)
	return out, err
}

func (h client) ProcessStop(input ProcessStopRequest) (ProcessStopResponse, error) {
	var out ProcessStopResponse
	err := h.call(protocol.HostCapabilityProcessStop, input, &out)
	return out, err
}

func (h client) CapabilityCall(input ProviderCallRequest) (ProviderCallResponse, error) {
	var out ProviderCallResponse
	err := h.call(protocol.HostCapabilityProviderCall, input, &out)
	return out, err
}

func (h client) ConnDial(input ConnDialRequest) (ConnDialResponse, error) {
	var out ConnDialResponse
	err := h.call(protocol.HostCapabilityConnDial, input, &out)
	return out, err
}

func (h client) ConnRead(input ConnReadRequest) (ConnReadResponse, error) {
	var out ConnReadResponse
	err := h.call(protocol.HostCapabilityConnRead, input, &out)
	return out, err
}

func (h client) ConnWrite(input ConnWriteRequest) (ConnWriteResponse, error) {
	var out ConnWriteResponse
	err := h.call(protocol.HostCapabilityConnWrite, input, &out)
	return out, err
}

func (h client) ConnClose(input ConnCloseRequest) (ConnCloseResponse, error) {
	var out ConnCloseResponse
	err := h.call(protocol.HostCapabilityConnClose, input, &out)
	return out, err
}

func (h client) call(command string, input any, out any) error {
	raw, err := h.caller.CallHost(command, input)
	if err != nil {
		return err
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (unavailableClient) Secret(string) (SecretMaterial, error) {
	return SecretMaterial{}, fmt.Errorf("host client is unavailable")
}

func (unavailableClient) Lookup(fpdatasource.LookupInput) (fpdatasource.LookupResult[fpdatasource.LookupMatch[any]], error) {
	return fpdatasource.LookupResult[fpdatasource.LookupMatch[any]]{}, fmt.Errorf("host client is unavailable")
}

func (unavailableClient) Search(fpdatasource.SearchInput) (fpdatasource.SearchResult[any], error) {
	return fpdatasource.SearchResult[any]{}, fmt.Errorf("host client is unavailable")
}

func (unavailableClient) Get(fpdatasource.GetInput) (fpdatasource.GetResult[any], error) {
	return fpdatasource.GetResult[any]{}, fmt.Errorf("host client is unavailable")
}

func (unavailableClient) ResolveEndpoint(string) (fpendpoint.EndpointRef, error) {
	return fpendpoint.EndpointRef{}, fmt.Errorf("host client is unavailable")
}

func (unavailableClient) HTTP(HTTPRequest) (HTTPResponse, error) {
	return HTTPResponse{}, fmt.Errorf("host client is unavailable")
}

func (unavailableClient) BlobRead(BlobReadRequest) (BlobReadResponse, error) {
	return BlobReadResponse{}, fmt.Errorf("host client is unavailable")
}

func (unavailableClient) BlobWrite(BlobWriteRequest) (BlobRef, error) {
	return BlobRef{}, fmt.Errorf("host client is unavailable")
}

func (unavailableClient) BlobInfo(BlobInfoRequest) (BlobRef, error) {
	return BlobRef{}, fmt.Errorf("host client is unavailable")
}

func (unavailableClient) EnvLookup(string) (EnvLookupResponse, error) {
	return EnvLookupResponse{}, fmt.Errorf("host client is unavailable")
}

func (unavailableClient) ProcessRun(ProcessRunRequest) (ProcessRunResponse, error) {
	return ProcessRunResponse{}, fmt.Errorf("host client is unavailable")
}

func (unavailableClient) ProcessStart(ProcessStartRequest) (ProcessStartResponse, error) {
	return ProcessStartResponse{}, fmt.Errorf("host client is unavailable")
}

func (unavailableClient) ProcessStop(ProcessStopRequest) (ProcessStopResponse, error) {
	return ProcessStopResponse{}, fmt.Errorf("host client is unavailable")
}

func (unavailableClient) CapabilityCall(ProviderCallRequest) (ProviderCallResponse, error) {
	return ProviderCallResponse{}, fmt.Errorf("host client is unavailable")
}

func (unavailableClient) ConnDial(ConnDialRequest) (ConnDialResponse, error) {
	return ConnDialResponse{}, fmt.Errorf("host client is unavailable")
}

func (unavailableClient) ConnRead(ConnReadRequest) (ConnReadResponse, error) {
	return ConnReadResponse{}, fmt.Errorf("host client is unavailable")
}

func (unavailableClient) ConnWrite(ConnWriteRequest) (ConnWriteResponse, error) {
	return ConnWriteResponse{}, fmt.Errorf("host client is unavailable")
}

func (unavailableClient) ConnClose(ConnCloseRequest) (ConnCloseResponse, error) {
	return ConnCloseResponse{}, fmt.Errorf("host client is unavailable")
}

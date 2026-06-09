package pluginbinding

import sdkhost "github.com/fluxplane/fluxplane-plugin/host"

const (
	CapabilityHTTP      = sdkhost.CapabilityHTTP
	CapabilityBlobRead  = sdkhost.CapabilityBlobRead
	CapabilityBlobWrite = sdkhost.CapabilityBlobWrite
	CapabilityEnvLookup = sdkhost.CapabilityEnvLookup
	CapabilityProcess   = sdkhost.CapabilityProcess
	CapabilityProvider  = sdkhost.CapabilityProvider
	CapabilityConn      = sdkhost.CapabilityConn
)

type HTTPRequest = sdkhost.HTTPRequest
type HTTPAuthRequest = sdkhost.HTTPAuthRequest
type HTTPResponse = sdkhost.HTTPResponse
type BlobRef = sdkhost.BlobRef
type BlobReadRequest = sdkhost.BlobReadRequest
type BlobReadResponse = sdkhost.BlobReadResponse
type BlobWriteRequest = sdkhost.BlobWriteRequest
type BlobInfoRequest = sdkhost.BlobInfoRequest
type EnvLookupRequest = sdkhost.EnvLookupRequest
type EnvLookupResponse = sdkhost.EnvLookupResponse
type ProcessRunRequest = sdkhost.ProcessRunRequest
type ProcessRunResponse = sdkhost.ProcessRunResponse
type ProcessStartRequest = sdkhost.ProcessStartRequest
type ProcessStartResponse = sdkhost.ProcessStartResponse
type ProcessStopRequest = sdkhost.ProcessStopRequest
type ProcessStopResponse = sdkhost.ProcessStopResponse
type ProviderCallRequest = sdkhost.ProviderCallRequest
type ProviderCallResponse = sdkhost.ProviderCallResponse
type ConnDialer = sdkhost.ConnDialer
type ConnTLS = sdkhost.ConnTLS
type ConnDialRequest = sdkhost.ConnDialRequest
type ConnDialResponse = sdkhost.ConnDialResponse
type ConnReadRequest = sdkhost.ConnReadRequest
type ConnReadResponse = sdkhost.ConnReadResponse
type ConnWriteRequest = sdkhost.ConnWriteRequest
type ConnWriteResponse = sdkhost.ConnWriteResponse
type ConnCloseRequest = sdkhost.ConnCloseRequest
type ConnCloseResponse = sdkhost.ConnCloseResponse

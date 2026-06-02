package pluginbinding

import sdkhost "github.com/fluxplane/fluxplane-plugin/host"

const (
	CapabilityHTTP      = sdkhost.CapabilityHTTP
	CapabilityBlobRead  = sdkhost.CapabilityBlobRead
	CapabilityBlobWrite = sdkhost.CapabilityBlobWrite
	CapabilityEnvLookup = sdkhost.CapabilityEnvLookup
	CapabilityProvider  = sdkhost.CapabilityProvider
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
type ProviderCallRequest = sdkhost.ProviderCallRequest
type ProviderCallResponse = sdkhost.ProviderCallResponse

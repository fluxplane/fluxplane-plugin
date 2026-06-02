package pluginbinding

import (
	sdkhost "github.com/fluxplane/fluxplane-plugin/host"
	"github.com/fluxplane/fluxplane-plugin/protocol"
)

const (
	ManifestProtocolKey = sdkhost.ManifestProtocolKey
	HostSecretGet       = sdkhost.SecretGetCommand
	HostIndexLookup     = sdkhost.IndexLookupCommand
	HostIndexSearch     = sdkhost.IndexSearchCommand
	HostIndexGet        = sdkhost.IndexGetCommand
	HostEndpointResolve = sdkhost.EndpointResolve
)

type HostClient = sdkhost.Client

func newHostClient(caller protocol.HostCaller) HostClient {
	return sdkhost.NewClient(caller)
}

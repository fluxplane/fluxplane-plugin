package host

import fpsecret "github.com/fluxplane/fluxplane-secret"

// SecretMaterial is the plugin wire shape for host-provided secret material.
type SecretMaterial struct {
	Kind    fpsecret.Kind `json:"kind,omitempty"`
	Value   string        `json:"value"`
	Source  string        `json:"source,omitempty"`
	Purpose string        `json:"purpose,omitempty"`
}

// Material converts the plugin wire shape to the shared secret material contract.
func (m SecretMaterial) Material() fpsecret.Material {
	return fpsecret.Material{Kind: m.Kind, Value: []byte(m.Value)}
}

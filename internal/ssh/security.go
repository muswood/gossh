// owner: muswood | Email: mumu920@outlook.com
package ssh

import xssh "golang.org/x/crypto/ssh"

type SecurityProfile struct {
	KeyExchanges       []string `json:"keyExchanges"`
	Ciphers            []string `json:"ciphers"`
	MACs               []string `json:"macs"`
	HostKeyAlgorithms  []string `json:"hostKeyAlgorithms"`
	PublicKeyAuths     []string `json:"publicKeyAuths"`
	InsecureAlgorithms int      `json:"insecureAlgorithms"`
	PostQuantumKEX     bool     `json:"postQuantumKex"`
}

func ModernSecurityProfile() SecurityProfile {
	supported := xssh.SupportedAlgorithms()
	insecure := xssh.InsecureAlgorithms()
	return SecurityProfile{
		KeyExchanges:       supported.KeyExchanges,
		Ciphers:            supported.Ciphers,
		MACs:               supported.MACs,
		HostKeyAlgorithms:  supported.HostKeys,
		PublicKeyAuths:     supported.PublicKeyAuths,
		InsecureAlgorithms: len(insecure.KeyExchanges) + len(insecure.Ciphers) + len(insecure.MACs) + len(insecure.HostKeys) + len(insecure.PublicKeyAuths),
		PostQuantumKEX:     contains(supported.KeyExchanges, xssh.KeyExchangeMLKEM768X25519),
	}
}

func modernAlgorithmsConfig() xssh.Config {
	supported := xssh.SupportedAlgorithms()
	return xssh.Config{
		KeyExchanges: supported.KeyExchanges,
		Ciphers:      supported.Ciphers,
		MACs:         supported.MACs,
	}
}

func modernHostKeyAlgorithms() []string {
	return xssh.SupportedAlgorithms().HostKeys
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

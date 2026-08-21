// owner: muswood | Email: mumu920@outlook.com
package ssh

import "testing"

func TestModernSecurityProfileIncludesAlgorithms(t *testing.T) {
	profile := ModernSecurityProfile()
	if len(profile.KeyExchanges) == 0 || len(profile.Ciphers) == 0 || len(profile.MACs) == 0 || len(profile.HostKeyAlgorithms) == 0 {
		t.Fatalf("incomplete security profile: %+v", profile)
	}
	if !profile.PostQuantumKEX {
		t.Fatalf("expected post-quantum KEX support in profile: %+v", profile.KeyExchanges)
	}
}

// owner: muswood | Email: mumu920@outlook.com
package openssh

import "testing"

func TestParseConfigImportsConcreteHosts(t *testing.T) {
	profiles, err := ParseConfig(`
Host *
  ServerAliveInterval 60
Host production staging
  HostName 10.0.0.12
  User deploy
  Port 2222
  IdentityFile ~/.ssh/id_deploy
  CertificateFile ~/.ssh/id_deploy-cert.pub
  ProxyJump jump.example.com
Host *.internal
  User ignored
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 {
		t.Fatalf("got %d profiles, want 2", len(profiles))
	}
	for _, profile := range profiles {
		if profile.HostName != "10.0.0.12" || profile.User != "deploy" || profile.Port != 2222 {
			t.Fatalf("unexpected profile: %+v", profile)
		}
		if profile.IdentityFile != "~/.ssh/id_deploy" || profile.CertificateFile != "~/.ssh/id_deploy-cert.pub" {
			t.Fatalf("key paths not parsed: %+v", profile)
		}
	}
}

func TestParseConfigRejectsInvalidPort(t *testing.T) {
	_, err := ParseConfig("Host server\n  Port 70000\n")
	if err == nil {
		t.Fatal("expected invalid port error")
	}
}

func TestParseConfigProxyCommand(t *testing.T) {
	profiles, err := ParseConfig("Host target\n  ProxyCommand nc -x proxy.example:1080 %h %p\n  ProxyJump first,second\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].ProxyCommand != "nc -x proxy.example:1080 %h %p" || profiles[0].ProxyJump != "first,second" {
		t.Fatalf("unexpected profile: %+v", profiles)
	}
}

func TestParseConfigForwardsAndRemoteCommand(t *testing.T) {
	profiles, err := ParseConfig(`
Host target
  HostName target.example
  RemoteCommand tmux new -A -s ops
  RequestTTY force
  ForwardAgent yes
  LocalForward 127.0.0.1:15432 db.internal:5432
  RemoteForward 0.0.0.0:18080 127.0.0.1:8080
  DynamicForward 127.0.0.1:1080
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 {
		t.Fatalf("got %d profiles, want 1", len(profiles))
	}
	p := profiles[0]
	if p.RemoteCommand != "tmux new -A -s ops" || p.RequestTTY != "force" || !p.ForwardAgent {
		t.Fatalf("session options not parsed: %+v", p)
	}
	if len(p.Forwards) != 3 {
		t.Fatalf("got %d forwards, want 3: %+v", len(p.Forwards), p.Forwards)
	}
	if f := p.Forwards[0]; f.Type != "local" || f.LocalHost != "127.0.0.1" || f.LocalPort != 15432 || f.RemoteHost != "db.internal" || f.RemotePort != 5432 {
		t.Fatalf("unexpected local forward: %+v", f)
	}
	if f := p.Forwards[1]; f.Type != "remote" || f.RemoteHost != "0.0.0.0" || f.RemotePort != 18080 || f.LocalHost != "127.0.0.1" || f.LocalPort != 8080 {
		t.Fatalf("unexpected remote forward: %+v", f)
	}
	if f := p.Forwards[2]; f.Type != "dynamic" || f.LocalHost != "127.0.0.1" || f.LocalPort != 1080 {
		t.Fatalf("unexpected dynamic forward: %+v", f)
	}
}

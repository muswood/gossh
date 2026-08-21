// owner: muswood | Email: mumu920@outlook.com
package agent

import "testing"

func TestAssessCommandAllowsReadOnlyChecks(t *testing.T) {
	allowed := []string{
		"df -hT",
		"lscpu",
		"nproc",
		"systemctl status nginx --no-pager -l",
		"systemctl --failed --no-pager",
		"kubectl get pods -A",
		"grep -R \"delete\" /var/log/app.log",
		"tail -n 200 /var/log/syslog",
	}
	for _, command := range allowed {
		if decision := AssessCommand(command); !decision.Allowed {
			t.Fatalf("expected command to be allowed: %q (%s)", command, decision.Reason)
		}
	}
}

func TestAssessCommandBlocksDangerousOperations(t *testing.T) {
	blocked := []string{
		"rm -rf /tmp/demo",
		"systemctl restart nginx",
		"kubectl delete pod web-0",
		"helm uninstall prod",
		"echo test > /etc/profile",
		"UPDATE users SET role='admin'",
		"apt-get install nginx",
		"chmod 777 /etc/shadow",
	}
	for _, command := range blocked {
		if decision := AssessCommand(command); decision.Allowed {
			t.Fatalf("expected command to be blocked: %q", command)
		}
	}
}

func TestAssessCommandRejectsShellEscapeAndUnsafeReadOnlyWrappers(t *testing.T) {
	blocked := []string{
		"df -h; rm -rf /",
		"cat /tmp/a | sh",
		"cat $(whoami)",
		"find /tmp -exec rm -f {} \\;",
		"sed -i 's/a/b/' /etc/config",
	}
	for _, command := range blocked {
		decision := AssessCommand(command)
		if decision.Allowed {
			t.Fatalf("expected command to be rejected: %q", command)
		}
	}
}

func TestAssessCommandAllowsReadOnlyPipeline(t *testing.T) {
	decision := AssessCommand("ps aux | grep ssh")
	if !decision.Allowed || !decision.ReadOnly || decision.Mutating {
		t.Fatalf("unexpected read-only pipeline decision: %#v", decision)
	}
}

func TestAssessCommandAllowsReadOnlyCommandSequence(t *testing.T) {
	allowed := []string{
		"uptime; echo '---'; free -h; nproc",
		"df -h -x tmpfs -x devtmpfs; echo '---INODES---'; df -i -x tmpfs -x devtmpfs",
		"ps aux --sort=-%cpu | head -n 8; echo '---MEM---'; ps aux --sort=-%mem | head -n 8",
	}
	for _, command := range allowed {
		decision := AssessCommand(command)
		if !decision.Allowed || !decision.ReadOnly || decision.Mutating {
			t.Fatalf("expected safe command sequence to be allowed as read-only: %q (%#v)", command, decision)
		}
	}
}

func TestAssessCommandRejectsMutationInsideReadOnlySequence(t *testing.T) {
	blocked := []string{
		"uptime; touch /tmp/marker",
		"echo safe; rm -rf /tmp/demo",
		"uptime; echo $(whoami)",
		"uptime; echo safe > /tmp/marker",
	}
	for _, command := range blocked {
		if decision := AssessCommand(command); decision.Allowed {
			t.Fatalf("expected unsafe command sequence to be rejected: %q", command)
		}
	}
}

func TestAssessCommandModeRequiresExplicitMutationAuthorization(t *testing.T) {
	if decision := AssessCommandMode("touch /tmp/marker", false); decision.Allowed {
		t.Fatal("mutation was allowed without task authorization")
	}
	decision := AssessCommandMode("touch /tmp/marker", true)
	if !decision.Allowed || !decision.Mutating || decision.ReadOnly {
		t.Fatalf("unexpected authorized mutation decision: %#v", decision)
	}
	if decision := AssessCommandMode("rm -rf /", true); decision.Allowed {
		t.Fatal("destructive mutation was allowed")
	}
}

func TestAssessCommandModeRequiresDeletionSwitch(t *testing.T) {
	previous := GetSecurityConfig()
	defer SetSecurityConfig(previous)

	config := DefaultSecurityConfig()
	config.WhitelistEnabled = false
	config.BlacklistEnabled = false
	config.MutationsEnabled = true
	config.DeletionsEnabled = false
	SetSecurityConfig(config)

	if decision := AssessCommandMode("rm -rf /tmp/demo", true); decision.Allowed {
		t.Fatal("deletion must remain blocked when the deletion switch is disabled")
	}

	config.DeletionsEnabled = true
	SetSecurityConfig(config)
	decision := AssessCommandMode("rm -rf /tmp/demo", true)
	if !decision.Allowed || !decision.Mutating || decision.ReadOnly || !decision.Deleting {
		t.Fatalf("expected authorized recursive deletion to be allowed: %#v", decision)
	}
	for _, command := range []string{"rm -rf /", "rm -rf /etc", "rm -rf /tmp/*", "find / -delete"} {
		if decision := AssessCommandMode(command, true); decision.Allowed {
			t.Fatalf("unsafe deletion target was allowed: %q (%#v)", command, decision)
		}
	}
}

func TestAssessCommandUsesConfigurableSecurityLists(t *testing.T) {
	previous := GetSecurityConfig()
	defer SetSecurityConfig(previous)

	config := DefaultSecurityConfig()
	config.CommandWhitelist = []string{"lscpu"}
	if decision := func() PolicyDecision {
		SetSecurityConfig(config)
		return AssessCommand("uname -a")
	}(); decision.Allowed {
		t.Fatal("custom whitelist allowed a command that was not configured")
	}

	config.WhitelistEnabled = false
	config.CommandBlacklist = []string{"nproc"}
	SetSecurityConfig(config)
	if decision := AssessCommand("nproc"); decision.Allowed || decision.Risk != "blacklisted" {
		t.Fatalf("expected blacklist to reject nproc: %#v", decision)
	}
	if decision := AssessCommand("echo health"); !decision.Allowed {
		t.Fatalf("disabling whitelist should allow a non-mutating command: %s", decision.Reason)
	}
	if decision := AssessCommand("curl https://example.test"); decision.Allowed {
		t.Fatalf("unknown command was treated as read-only: %#v", decision)
	}

	config.MutationsEnabled = false
	SetSecurityConfig(config)
	if decision := AssessCommandMode("touch /tmp/marker", true); decision.Allowed {
		t.Fatal("global mutation switch allowed a write command")
	}
}

func TestAssessCommandAllowsReadOnlySystemctlModeWhenListsAreDisabled(t *testing.T) {
	previous := GetSecurityConfig()
	defer SetSecurityConfig(previous)

	config := DefaultSecurityConfig()
	config.WhitelistEnabled = false
	config.BlacklistEnabled = false
	config.MutationsEnabled = true
	SetSecurityConfig(config)

	decision := AssessCommandMode("systemctl --failed --no-pager", true)
	if !decision.Allowed || !decision.ReadOnly || decision.Mutating {
		t.Fatalf("expected systemctl --failed to remain read-only and allowed: %#v", decision)
	}
	decision = AssessCommandMode("systemctl restart nginx", true)
	if !decision.Allowed || !decision.Mutating || decision.ReadOnly {
		t.Fatalf("expected authorized systemctl restart to be allowed as a mutation: %#v", decision)
	}
	if decision := AssessCommandMode("systemctl restart nginx", false); decision.Allowed {
		t.Fatal("systemctl restart must require task mutation authorization")
	}
}

func TestAssessCommandBaselineLeavesSemanticClassificationToAI(t *testing.T) {
	decision := AssessCommandBaseline("apt list --upgradable; pro status; swapon --show")
	if !decision.Allowed || !decision.ReadOnly || decision.Mutating || decision.Deleting {
		t.Fatalf("unexpected baseline decision for read-only unknown commands: %#v", decision)
	}
}

func TestAssessCommandBaselineKeepsDeletionAsProgramEnforced(t *testing.T) {
	decision := AssessCommandBaseline("rm -f /tmp/demo")
	if !decision.Allowed || !decision.Mutating || !decision.Deleting || decision.ReadOnly {
		t.Fatalf("deletion was not classified by program baseline: %#v", decision)
	}
	for _, command := range []string{"rm -rf /", "rm -rf /tmp/*"} {
		if decision := AssessCommandBaseline(command); decision.Allowed {
			t.Fatalf("unsafe deletion target was allowed by baseline: %q", command)
		}
	}
}

func TestAssessCommandBlocksOperationalMutationWhenMutationsAreDisabled(t *testing.T) {
	previous := GetSecurityConfig()
	defer SetSecurityConfig(previous)

	config := DefaultSecurityConfig()
	config.MutationsEnabled = false
	SetSecurityConfig(config)

	if decision := AssessCommandMode("systemctl restart nginx", true); decision.Allowed {
		t.Fatal("global mutation switch must block systemctl restart")
	}
}

func TestAdministratorModeBypassesCommandPolicyButMarksDeletion(t *testing.T) {
	previous := GetSecurityConfig()
	defer SetSecurityConfig(previous)

	config := DefaultSecurityConfig()
	config.AdministratorEnabled = true
	config.MutationsEnabled = false
	config.DeletionsEnabled = false
	SetSecurityConfig(config)

	for _, command := range []string{
		"systemctl restart nginx",
		"printf test > /tmp/gossh-admin-test",
		"python3 -c 'print(1)'",
		"rm -rf /",
	} {
		decision := AssessCommandMode(command, false)
		if !decision.Allowed || !decision.Administrator || !decision.Mutating {
			t.Fatalf("administrator mode blocked command %q: %#v", command, decision)
		}
	}
	if decision := AssessCommand("rm -rf /"); !decision.Deleting {
		t.Fatalf("administrator mode must keep deletion classification: %#v", decision)
	}
	for _, command := range []string{"sh -c 'rm -rf /tmp/demo'", "python3 -c 'os.remove(\"/tmp/demo\")'"} {
		if decision := AssessCommand(command); !decision.Deleting {
			t.Fatalf("administrator mode missed deletion classification for %q: %#v", command, decision)
		}
	}
}

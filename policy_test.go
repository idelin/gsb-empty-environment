package netsec

import (
	"os"
	"path/filepath"
	"testing"
)

func baseRules() Rules {
	return Rules{
		AllowedMethods: []string{
			MethodCreateContainer,
			MethodExecProcess,
			MethodCopyFile,
		},
	}
}

func TestCompliantCreateAndExecutionAllowed(t *testing.T) {
	root := t.TempDir()
	policy := NewPolicy(baseRules())

	create := Payload{
		ContainerID:  "container-1",
		Root:         root,
		Args:         []string{"/bin/sh", "-c", "sleep 60"},
		Env:          []string{"PATH=/usr/bin", "LANG=C"},
		Mounts:       []Mount{{Source: "/safe/data", Destination: "/data", Type: "bind"}},
		Capabilities: []string{"CAP_CHOWN"},
		Devices:      []Device{{Path: "/dev/null", Type: "c"}},
	}
	if decision := policy.Check(MethodCreateContainer, create); !decision.Allowed {
		t.Fatalf("compliant CreateContainer was denied: %s", decision)
	}

	exec := Payload{
		ContainerID:  "container-1",
		Args:         []string{"/bin/sh", "-c", "sleep 60"},
		Capabilities: []string{"cap_chown"},
	}
	if decision := policy.Check(MethodExecProcess, exec); !decision.Allowed {
		t.Fatalf("registered ExecProcess was denied: %s", decision)
	}

	copyFile := Payload{
		ContainerID: "container-1",
		Path:        "data/file.txt",
	}
	if decision := policy.Check(MethodCopyFile, copyFile); !decision.Allowed {
		t.Fatalf("compliant CopyFile was denied: %s", decision)
	}
}

func TestUnapprovedMethodDenied(t *testing.T) {
	policy := NewPolicy(baseRules())
	decision := policy.Check("UpdateRoutes", map[string]any{"routes": []any{}})
	if decision.Allowed || decision.Code != "method_denied" {
		t.Fatalf("unapproved method decision = %+v, want method_denied", decision)
	}
}

func TestPrivilegedDenied(t *testing.T) {
	policy := NewPolicy(baseRules())
	decision := policy.Check(MethodCreateContainer, Payload{
		ContainerID: "container-1",
		Args:        []string{"/bin/sh"},
		Privileged:  true,
	})
	if decision.Allowed || decision.Code != "privileged_denied" {
		t.Fatalf("privileged decision = %+v, want privileged_denied", decision)
	}
}

func TestUnregisteredExecutionDenied(t *testing.T) {
	policy := NewPolicy(baseRules())
	if decision := policy.Check(MethodCreateContainer, Payload{
		ContainerID: "container-1",
		Args:        []string{"/bin/sh", "-c", "sleep 60"},
	}); !decision.Allowed {
		t.Fatalf("setup CreateContainer denied: %s", decision)
	}

	decision := policy.Check(MethodExecProcess, Payload{
		ContainerID: "container-1",
		Args:        []string{"/bin/sh", "-c", "cat /etc/shadow"},
	})
	if decision.Allowed || decision.Code != "exec_args_denied" {
		t.Fatalf("unregistered execution decision = %+v, want exec_args_denied", decision)
	}
}

func TestExecutionWithoutCreationDenied(t *testing.T) {
	policy := NewPolicy(baseRules())
	decision := policy.Check(MethodExecProcess, Payload{
		ContainerID: "container-1",
		Args:        []string{"/bin/sh"},
	})
	if decision.Allowed || decision.Code != "container_unknown" {
		t.Fatalf("execution without creation decision = %+v, want container_unknown", decision)
	}
}

func TestCapabilityEscalationDenied(t *testing.T) {
	policy := NewPolicy(baseRules())
	if decision := policy.Check(MethodCreateContainer, Payload{
		ContainerID:  "container-1",
		Args:         []string{"/bin/sh"},
		Capabilities: []string{"CAP_CHOWN", "CAP_NET_BIND_SERVICE"},
	}); !decision.Allowed {
		t.Fatalf("setup CreateContainer denied: %s", decision)
	}

	decision := policy.Check(MethodExecProcess, Payload{
		ContainerID:  "container-1",
		Args:         []string{"/bin/sh"},
		Capabilities: []string{"CAP_SYS_ADMIN"},
	})
	if decision.Allowed || decision.Code != "capability_denied" {
		t.Fatalf("capability escalation decision = %+v, want capability_denied", decision)
	}

	allowed := policy.Check(MethodExecProcess, Payload{
		ContainerID:  "container-1",
		Args:         []string{"/bin/sh"},
		Capabilities: []string{"CAP_NET_BIND_SERVICE"},
	})
	if !allowed.Allowed {
		t.Fatalf("registered capability subset was denied: %s", allowed)
	}
}

func TestCopyFileTraversalDenied(t *testing.T) {
	root := t.TempDir()
	policy := NewPolicy(baseRules())

	for _, path := range []string{
		"../../etc/passwd",
		"/../../etc/passwd",
		`..\..\etc\passwd`,
		"..%2f..%2fetc%2fpasswd",
	} {
		decision := policy.Check(MethodCopyFile, Payload{Root: root, Path: path})
		if decision.Allowed || decision.Code != "copy_path_denied" {
			t.Fatalf("CopyFile path %q decision = %+v, want copy_path_denied", path, decision)
		}
	}
}

func TestCopyFileSymlinkEscapeDenied(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "rootfs")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}

	policy := NewPolicy(baseRules())
	decision := policy.Check(MethodCopyFile, Payload{
		Root: root,
		Path: "escape/secret",
	})
	if decision.Allowed || decision.Code != "copy_path_denied" {
		t.Fatalf("symlink escape decision = %+v, want copy_path_denied", decision)
	}

	if err := ValidateCopyPath(root, "inside/file"); err != nil {
		t.Fatalf("ordinary path rejected: %v", err)
	}
}

func TestCopyFileDanglingSymlinkEscapeDenied(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "rootfs")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "missing", "outside"), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}

	if err := ValidateCopyPath(root, "escape/file"); err == nil {
		t.Fatal("dangling symlink outside the root was accepted")
	}
}

func TestCopyFileSymlinkInsideRootAllowed(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "real")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCopyPath(root, "alias/file"); err != nil {
		t.Fatalf("symlink inside root rejected: %v", err)
	}
}

func TestFieldRulesDenyForbiddenEnvironmentKey(t *testing.T) {
	rules := Rules{
		AllowedMethods: []string{MethodCreateContainer},
		Methods: map[string]MethodRules{
			MethodCreateContainer: {
				Fields: map[string]FieldRule{
					"env": {
						Prefix:        []string{"PATH="},
						ForbiddenKeys: []string{"LD_PRELOAD"},
					},
				},
			},
		},
	}
	policy := NewPolicy(rules)

	decision := policy.Check(MethodCreateContainer, Payload{
		Args: []string{"/bin/sh"},
		Env:  []string{"PATH=/usr/bin", "LD_PRELOAD=/tmp/evil.so"},
	})
	if decision.Allowed || decision.Code != "forbidden_key" {
		t.Fatalf("forbidden env decision = %+v, want forbidden_key", decision)
	}

	decision = policy.Check(MethodCreateContainer, Payload{
		Args: []string{"/bin/sh"},
		Env:  []string{"PATH=/usr/bin", "HOME=/root"},
	})
	if decision.Allowed {
		t.Fatalf("environment outside prefix allowlist was allowed: %s", decision)
	}
}

func TestJSONPayloadAndRegexRule(t *testing.T) {
	rules := Rules{
		AllowedMethods: []string{MethodCreateContainer},
		Methods: map[string]MethodRules{
			MethodCreateContainer: {
				Fields: map[string]FieldRule{
					"args":               {Regex: []string{`^/bin/(sh|bash)$`}},
					"mounts.destination": {Prefix: []string{"/data"}},
				},
			},
		},
	}
	policy := NewPolicy(rules)
	payload := []byte(`{
		"container_id": "c1",
		"process": {"args": ["/bin/bash"]},
		"mounts": [
			{"source": "/safe", "destination": "/data/cache", "type": "bind"}
		]
	}`)
	if decision := policy.Check(MethodCreateContainer, payload); !decision.Allowed {
		t.Fatalf("valid JSON payload denied: %s", decision)
	}
}

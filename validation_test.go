package netsec

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestAcceptsCompliantUntrustedRequest(t *testing.T) {
	allowedRoot := t.TempDir()
	hostPath := filepath.Join(allowedRoot, "cache")
	if err := os.Mkdir(hostPath, 0o755); err != nil {
		t.Fatal(err)
	}

	policy := Policy{
		AllowedCapabilities: []string{"CAP_NET_BIND_SERVICE"},
		HostMountRoots:      []string{allowedRoot},
		AllowedDevices:      []string{"/dev/null"},
	}
	req := BuildRequest{
		Isolation: IsolationOCI,
		Namespaces: NamespaceOptions{
			Network: "none",
			PID:     "private",
			IPC:     "private",
			UTS:     "private",
			User:    "private",
		},
		AddedCapabilities: []string{"CAP_NET_BIND_SERVICE"},
		Mounts: []Mount{{
			Source:      hostPath,
			Destination: "/cache",
			Options:     []string{"bind", "ro", "nosuid", "nodev"},
		}},
		Devices: []string{"/dev/null"},
		UserMappings: []UserMapping{{
			ContainerID: 0,
			HostID:      100000,
			Size:        1,
		}},
	}

	if err := Validate(req, policy); err != nil {
		t.Fatalf("compliant request was rejected: %v", err)
	}
}

func TestAcceptsCompliantRootlessRequest(t *testing.T) {
	req := BuildRequest{
		Isolation: IsolationRootless,
		Namespaces: NamespaceOptions{
			Network: "none",
			PID:     "private",
			IPC:     "private",
		},
		UserMappings: []UserMapping{{
			ContainerID: 0,
			HostID:      int64(os.Getuid()),
			Size:        1,
		}},
	}

	if err := Validate(req, Policy{}); err != nil {
		t.Fatalf("compliant rootless request was rejected: %v", err)
	}
}

func TestUntrustedIsolation(t *testing.T) {
	tests := []struct {
		name      string
		isolation Isolation
		trusted   bool
		wantCode  string
	}{
		{name: "chroot", isolation: IsolationChroot, wantCode: CodeIsolationNotAllowed},
		{name: "unknown", isolation: Isolation("gvisor"), wantCode: CodeIsolationUnknown},
		{name: "empty", isolation: Isolation(""), wantCode: CodeIsolationUnknown},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := BuildRequest{Isolation: test.isolation, Trusted: test.trusted}
			assertViolationCode(t, Validate(req, Policy{}), test.wantCode)
		})
	}
}

func TestTrustedChrootStillRequiresSafeRuntimeOptions(t *testing.T) {
	trustedChroot := BuildRequest{Isolation: IsolationChroot, Trusted: true}
	if err := Validate(trustedChroot, Policy{}); err != nil {
		t.Fatalf("trusted chroot request was rejected: %v", err)
	}

	req := BuildRequest{
		Isolation:  IsolationChroot,
		Trusted:    true,
		Namespaces: NamespaceOptions{Network: "host"},
	}
	assertViolationCode(t, Validate(req, Policy{}), CodeNamespaceHost)
}

func TestRejectsHostNamespaces(t *testing.T) {
	tests := []struct {
		name      string
		namespace NamespaceOptions
	}{
		{name: "network", namespace: NamespaceOptions{Network: "host"}},
		{name: "pid", namespace: NamespaceOptions{PID: "HOST"}},
		{name: "ipc", namespace: NamespaceOptions{IPC: " host "}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := BuildRequest{
				Isolation:  IsolationOCI,
				Namespaces: test.namespace,
			}
			assertViolationCode(t, Validate(req, Policy{}), CodeNamespaceHost)
		})
	}
}

func TestRejectsUnverifiableExternalNamespaces(t *testing.T) {
	tests := []struct {
		name      string
		namespace NamespaceOptions
	}{
		{name: "network_path", namespace: NamespaceOptions{Network: "ns:/proc/1/ns/net"}},
		{name: "pid_container", namespace: NamespaceOptions{PID: "container:abc"}},
		{name: "ipc_path", namespace: NamespaceOptions{IPC: "path:/run/namespaces/ipc"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := BuildRequest{
				Isolation:  IsolationOCI,
				Namespaces: test.namespace,
			}
			assertViolationCode(t, Validate(req, Policy{}), CodeNamespaceInvalid)
		})
	}
}

func TestRejectsPrivilegedCapabilitiesEvenWhenRegistered(t *testing.T) {
	tests := []string{"SYS_ADMIN", "CAP_SYS_ADMIN", "cap_sys_ptrace", "SYS_MODULE", "ALL"}
	for _, capability := range tests {
		t.Run(capability, func(t *testing.T) {
			policy := Policy{AllowedCapabilities: []string{capability}}
			req := BuildRequest{
				Isolation:         IsolationOCI,
				AddedCapabilities: []string{capability},
			}
			assertViolationCode(t, Validate(req, policy), CodeCapabilityPrivileged)
		})
	}
}

func TestCapabilitiesMustBeRegistered(t *testing.T) {
	policy := Policy{AllowedCapabilities: []string{"NET_BIND_SERVICE"}}
	req := BuildRequest{
		Isolation:         IsolationOCI,
		AddedCapabilities: []string{"CHOWN"},
	}
	assertViolationCode(t, Validate(req, policy), CodeCapabilityNotAllowed)

	req.AddedCapabilities = []string{""}
	assertViolationCode(t, Validate(req, policy), CodeCapabilityInvalid)

	req.AddedCapabilities = []string{"CAP_NET_BIND_SERVICE"}
	if err := Validate(req, policy); err != nil {
		t.Fatalf("registered capability was rejected: %v", err)
	}
}

func TestRejectsMountEscape(t *testing.T) {
	base := t.TempDir()
	allowedRoot := filepath.Join(base, "allowed")
	outsideRoot := filepath.Join(base, "outside")
	prefixLookalike := allowedRoot + "-evil"
	for _, dir := range []string{allowedRoot, outsideRoot, prefixLookalike} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	allowedFile := filepath.Join(allowedRoot, "allowed.txt")
	outsideFile := filepath.Join(outsideRoot, "secret.txt")
	if err := os.WriteFile(allowedFile, []byte("allowed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	symlinkEscape := filepath.Join(allowedRoot, "escape")
	if err := os.Symlink(outsideFile, symlinkEscape); err != nil {
		t.Fatal(err)
	}

	symlinkIntoAllowed := filepath.Join(outsideRoot, "inward")
	if err := os.Symlink(allowedFile, symlinkIntoAllowed); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		source string
	}{
		{name: "direct_outside", source: outsideFile},
		{name: "symlink_to_outside", source: symlinkEscape},
		{name: "prefix_lookalike", source: filepath.Join(prefixLookalike, "file")},
		{name: "outside_symlink_to_inside", source: symlinkIntoAllowed},
	}

	policy := Policy{HostMountRoots: []string{allowedRoot}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := BuildRequest{
				Isolation: IsolationOCI,
				Mounts: []Mount{{
					Source:      test.source,
					Destination: "/data",
					Options:     []string{"bind", "ro", "nosuid", "nodev"},
				}},
			}
			assertViolationCode(t, Validate(req, policy), CodeMountSourceOutside)
		})
	}

	absolutePath, err := filepath.Abs(allowedFile)
	if err != nil {
		t.Fatal(err)
	}
	req := BuildRequest{
		Isolation: IsolationOCI,
		Mounts: []Mount{{
			Source:      absolutePath,
			Destination: "/data",
			Options:     []string{"bind", "ro", "nosuid", "nodev"},
		}},
	}
	if err := Validate(req, policy); err != nil {
		t.Fatalf("allowlisted mount source was rejected: %v", err)
	}
}

func TestRejectsUnsafeOrIncompleteMountOptions(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.WriteFile(source, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	policy := Policy{HostMountRoots: []string{root}}

	tests := []struct {
		name    string
		options []string
		code    string
	}{
		{name: "missing_nodev", options: []string{"bind", "ro", "nosuid"}, code: CodeMountRestrictions},
		{name: "read_write", options: []string{"bind", "ro", "rw", "nosuid", "nodev"}, code: CodeMountOptionConflict},
		{name: "suid", options: []string{"bind", "ro", "suid", "nodev"}, code: CodeMountOptionConflict},
		{name: "dev", options: []string{"bind", "ro", "nosuid", "dev"}, code: CodeMountOptionConflict},
		{name: "shared", options: []string{"bind", "ro", "nosuid", "nodev", "rshared"}, code: CodeMountPropagationUnsafe},
		{name: "empty_option", options: []string{"bind", "", "ro", "nosuid", "nodev"}, code: CodeMountOptionsInvalid},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := BuildRequest{
				Isolation: IsolationOCI,
				Mounts: []Mount{{
					Source:      source,
					Destination: "/data",
					Options:     test.options,
				}},
			}
			assertViolationCode(t, Validate(req, policy), test.code)
		})
	}
}

func TestRejectsUnsafeMountTargets(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.WriteFile(source, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	policy := Policy{HostMountRoots: []string{root}}

	tests := []struct {
		name        string
		destination string
		target      string
		code        string
	}{
		{name: "relative", destination: "data", code: CodeMountTargetInvalid},
		{name: "traversal", destination: "/../../data", code: CodeMountTargetInvalid},
		{name: "missing", destination: "", code: CodeMountTargetInvalid},
		{name: "conflict", destination: "/one", target: "/two", code: CodeMountTargetConflict},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := BuildRequest{
				Isolation: IsolationOCI,
				Mounts: []Mount{{
					Source:      source,
					Destination: test.destination,
					Target:      test.target,
					Options:     []string{"bind", "ro", "nosuid", "nodev"},
				}},
			}
			assertViolationCode(t, Validate(req, policy), test.code)
		})
	}
}

func TestRejectsUnregisteredDevices(t *testing.T) {
	policy := Policy{AllowedDevices: []string{"/dev/null"}}
	tests := []struct {
		name   string
		device string
		code   string
	}{
		{name: "not_registered", device: "/dev/zero", code: CodeDeviceNotAllowed},
		{name: "relative", device: "dev/null", code: CodeDeviceInvalid},
		{name: "empty", device: "", code: CodeDeviceInvalid},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := BuildRequest{
				Isolation: IsolationOCI,
				Devices:   []string{test.device},
			}
			assertViolationCode(t, Validate(req, policy), test.code)
		})
	}

	req := BuildRequest{Isolation: IsolationOCI, Devices: []string{"/dev/null"}}
	if err := Validate(req, policy); err != nil {
		t.Fatalf("registered device was rejected: %v", err)
	}
}

func TestRejectsDeviceSymlinkNotExplicitlyRegistered(t *testing.T) {
	link := filepath.Join(t.TempDir(), "null-link")
	if err := os.Symlink("/dev/null", link); err != nil {
		t.Fatal(err)
	}

	req := BuildRequest{
		Isolation: IsolationOCI,
		Devices:   []string{link},
	}
	assertViolationCode(t, Validate(req, Policy{AllowedDevices: []string{"/dev/null"}}), CodeDeviceNotAllowed)
}

func TestRejectsHostRootUserMappings(t *testing.T) {
	tests := []struct {
		name    string
		mapping UserMapping
		code    string
	}{
		{
			name:    "host_root",
			mapping: UserMapping{ContainerID: 0, HostID: 0, Size: 1},
			code:    CodeUserMappingMapsHostRoot,
		},
		{
			name:    "host_root_range",
			mapping: UserMapping{ContainerID: 10, HostID: 0, Size: 100},
			code:    CodeUserMappingMapsHostRoot,
		},
		{
			name:    "zero_size",
			mapping: UserMapping{ContainerID: 0, HostID: 1000, Size: 0},
			code:    CodeUserMappingInvalid,
		},
		{
			name:    "negative_host",
			mapping: UserMapping{ContainerID: 0, HostID: -1, Size: 1},
			code:    CodeUserMappingInvalid,
		},
		{
			name:    "overflow",
			mapping: UserMapping{ContainerID: 0, HostID: math.MaxInt64, Size: 2},
			code:    CodeUserMappingInvalid,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := BuildRequest{
				Isolation:    IsolationOCI,
				UserMappings: []UserMapping{test.mapping},
			}
			assertViolationCode(t, Validate(req, Policy{}), test.code)
		})
	}
}

func TestRejectsInvalidMountPolicy(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(source, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	req := BuildRequest{
		Isolation: IsolationOCI,
		Mounts: []Mount{{
			Source:      source,
			Destination: "/data",
			Options:     []string{"bind", "ro", "nosuid", "nodev"},
		}},
	}
	assertViolationCode(t, Validate(req, Policy{HostMountRoots: []string{"relative/root"}}), CodePolicyInvalid)
	assertViolationCode(t, Validate(req, Policy{HostMountRoots: []string{source}}), CodePolicyInvalid)
}

func TestCollectsMultipleViolations(t *testing.T) {
	req := BuildRequest{
		Isolation:         IsolationChroot,
		Namespaces:        NamespaceOptions{Network: "host", PID: "host", IPC: "host"},
		AddedCapabilities: []string{"SYS_ADMIN"},
		Devices:           []string{"/dev/zero"},
		UserMappings:      []UserMapping{{ContainerID: 0, HostID: 0, Size: 1}},
	}

	err := Validate(req, Policy{})
	validationErr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if len(validationErr.Violations) < 6 {
		t.Fatalf("expected multiple violations, got %d: %v", len(validationErr.Violations), err)
	}
}

func assertViolationCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected violation %q, got nil", want)
	}
	validationErr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if !validationErr.HasCode(want) {
		t.Fatalf("expected violation %q, got: %v", want, err)
	}
}

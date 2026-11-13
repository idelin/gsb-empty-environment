package hostpath_test

import (
	"errors"
	"fmt"
	"testing"

	"example.org/netsec/q0688/hostpath"
)

func testValidator(t *testing.T) *hostpath.Validator {
	t.Helper()
	return hostpath.New(
		[]hostpath.Prefix{
			{Path: "/var/log", Writable: true},
			{Path: "/var/data", Writable: false},
			{Path: "/run/shared", Writable: true},
		},
		nil,
		hostpath.SymlinkTable{
			// 允许目录下的链接指向宿主根，用于验证符号链接逃逸。
			"/var/log/rootlink":   "/",
			"/var/log/containerd": "/run/containerd",
			// 允许目录内的相对链接，仍解析到允许目录之内。
			"/var/log/app/current": "../archive",
			// 解析链上的环。
			"/var/log/loop-a": "/var/log/loop-b",
			"/var/log/loop-b": "/var/log/loop-a",
			// subPath 中的链接越出卷目录。
			"/var/data/reports/escape": "/etc/passwd",
		},
	)
}

func TestCheckPath(t *testing.T) {
	v := testValidator(t)

	tests := []struct {
		name string
		path string
		want error
	}{
		{
			name: "clean allowed path",
			path: "/var/log/pods/app.log",
			want: nil,
		},
		{
			name: "dot and duplicate slashes normalized",
			path: "/var/log/./pods//app.log",
			want: nil,
		},
		{
			name: "dotdot fold lands outside whitelist",
			path: "/var/log/../run/containerd",
			want: hostpath.ErrWhitelist,
		},
		{
			name: "dotdot fold stays inside whitelist",
			path: "/var/log/pods/../archive/app.log",
			want: nil,
		},
		{
			name: "pre-fold path already outside whitelist",
			path: "/var/log/../etc/ssh/sshd_config",
			want: hostpath.ErrWhitelist,
		},
		{
			name: "segment boundary rejects /var/log2",
			path: "/var/log2/kubelet.log",
			want: hostpath.ErrWhitelist,
		},
		{
			name: "segment boundary exact prefix allowed",
			path: "/var/log",
			want: nil,
		},
		{
			name: "symlink to host root rejected",
			path: "/var/log/rootlink",
			want: hostpath.ErrWhitelist,
		},
		{
			name: "symlink chain lands outside whitelist",
			path: "/var/log/containerd/runc",
			want: hostpath.ErrWhitelist,
		},
		{
			name: "relative symlink stays inside whitelist",
			path: "/var/log/app/current/old.log",
			want: nil,
		},
		{
			name: "socket under allowed prefix denied",
			path: "/run/shared/agent.sock",
			want: hostpath.ErrDenied,
		},
		{
			name: "deep socket under allowed prefix denied",
			path: "/var/log/cri/runtime.sock",
			want: hostpath.ErrDenied,
		},
		{
			name: "relative path rejected",
			path: "var/log/app.log",
			want: hostpath.ErrNotAbsolute,
		},
		{
			name: "empty path rejected",
			path: "",
			want: hostpath.ErrNotAbsolute,
		},
		{
			name: "symlink loop reported",
			path: "/var/log/loop-a",
			want: hostpath.ErrSymlinkLoop,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.CheckPath(tt.path)
			if !errors.Is(err, tt.want) {
				t.Fatalf("CheckPath(%q) = %v, want %v", tt.path, err, tt.want)
			}
		})
	}
}

func TestDenyOverridesWhitelist(t *testing.T) {
	// 即便白名单放行了根目录与 /var/lib，默认禁止清单仍须优先生效。
	v := hostpath.New(
		[]hostpath.Prefix{
			{Path: "/", Writable: false},
			{Path: "/var/lib", Writable: true},
		},
		nil,
		nil,
	)

	tests := []struct {
		path string
		want error
	}{
		{"/", hostpath.ErrDenied},
		{"/proc/sys/kernel", hostpath.ErrDenied},
		{"/sys/fs/cgroup", hostpath.ErrDenied},
		{"/etc/hostname", hostpath.ErrDenied},
		{"/var/lib/kubelet/pods/uid", hostpath.ErrDenied},
		{"/var/lib/app/data", nil},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if err := v.CheckPath(tt.path); !errors.Is(err, tt.want) {
				t.Fatalf("CheckPath(%q) = %v, want %v", tt.path, err, tt.want)
			}
		})
	}
}

func TestHostPathType(t *testing.T) {
	v := testValidator(t)

	if err := v.ValidateHostPath(hostpath.HostPathVolume{Path: "/var/log/app.log", Type: ""}); err != nil {
		t.Fatalf("empty type should skip existence checks: %v", err)
	}
	if err := v.ValidateHostPath(hostpath.HostPathVolume{Path: "/var/log/app.log", Type: "DirectoryOrCreate"}); err != nil {
		t.Fatalf("known type rejected: %v", err)
	}
	err := v.ValidateHostPath(hostpath.HostPathVolume{Path: "/var/log/app.log", Type: "Pipe"})
	if !errors.Is(err, hostpath.ErrUnknownType) {
		t.Fatalf("unknown type error = %v, want ErrUnknownType", err)
	}
}

func TestValidateMount(t *testing.T) {
	v := testValidator(t)

	tests := []struct {
		name  string
		vol   hostpath.HostPathVolume
		mount hostpath.VolumeMount
		env   map[string]string
		want  error
	}{
		{
			name:  "read-write mount on writable prefix",
			vol:   hostpath.HostPathVolume{Path: "/var/log/pods"},
			mount: hostpath.VolumeMount{ReadOnly: false},
			want:  nil,
		},
		{
			name:  "read-only mount on read-only prefix",
			vol:   hostpath.HostPathVolume{Path: "/var/data/reports"},
			mount: hostpath.VolumeMount{ReadOnly: true},
			want:  nil,
		},
		{
			name:  "read-write mount on read-only prefix rejected",
			vol:   hostpath.HostPathVolume{Path: "/var/data/reports"},
			mount: hostpath.VolumeMount{ReadOnly: false},
			want:  hostpath.ErrWritable,
		},
		{
			name:  "subpath with dotdot rejected",
			vol:   hostpath.HostPathVolume{Path: "/var/log/pods"},
			mount: hostpath.VolumeMount{SubPath: "../run", ReadOnly: true},
			want:  hostpath.ErrSubPathParent,
		},
		{
			name:  "absolute subpath rejected",
			vol:   hostpath.HostPathVolume{Path: "/var/log/pods"},
			mount: hostpath.VolumeMount{SubPath: "/etc/passwd", ReadOnly: true},
			want:  hostpath.ErrSubPathAbsolute,
		},
		{
			name:  "lexical subpath inside volume allowed",
			vol:   hostpath.HostPathVolume{Path: "/var/log"},
			mount: hostpath.VolumeMount{SubPath: "pods/app/./log", ReadOnly: true},
			want:  nil,
		},
		{
			name:  "subpath symlink escape rejected",
			vol:   hostpath.HostPathVolume{Path: "/var/data"},
			mount: hostpath.VolumeMount{SubPath: "reports/escape", ReadOnly: true},
			want:  hostpath.ErrSubPathEscape,
		},
		{
			name:  "subpath to socket denied",
			vol:   hostpath.HostPathVolume{Path: "/run/shared"},
			mount: hostpath.VolumeMount{SubPath: "agent.sock", ReadOnly: true},
			want:  hostpath.ErrDenied,
		},
		{
			name:  "subpathexpr expanded inside volume",
			vol:   hostpath.HostPathVolume{Path: "/var/log"},
			mount: hostpath.VolumeMount{SubPathExpr: "$(POD_NAMESPACE)/$(POD_NAME)", ReadOnly: true},
			env:   map[string]string{"POD_NAMESPACE": "ns-a", "POD_NAME": "pod-1"},
			want:  nil,
		},
		{
			name:  "subpathexpr with dotdot rejected",
			vol:   hostpath.HostPathVolume{Path: "/var/log"},
			mount: hostpath.VolumeMount{SubPathExpr: "$(POD_NAME)/..", ReadOnly: true},
			env:   map[string]string{"POD_NAME": "pod-1"},
			want:  hostpath.ErrSubPathParent,
		},
		{
			name:  "subpathexpr undefined variable rejected",
			vol:   hostpath.HostPathVolume{Path: "/var/log"},
			mount: hostpath.VolumeMount{SubPathExpr: "$(MISSING)/log", ReadOnly: true},
			want:  hostpath.ErrSubPathVar,
		},
		{
			name:  "subpath and subpathexpr conflict",
			vol:   hostpath.HostPathVolume{Path: "/var/log"},
			mount: hostpath.VolumeMount{SubPath: "a", SubPathExpr: "$(POD_NAME)", ReadOnly: true},
			want:  hostpath.ErrSubPathConflict,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.ValidateMount(tt.vol, tt.mount, tt.env)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ValidateMount() = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestCustomDenyList(t *testing.T) {
	v := hostpath.New(
		[]hostpath.Prefix{{Path: "/var/log", Writable: true}},
		&hostpath.DenyList{
			Paths:       []string{"/var/log/forbidden"},
			DenySockets: false,
		},
		nil,
	)

	if err := v.CheckPath("/var/log/runtime.sock"); err != nil {
		t.Fatalf("sockets should be allowed when DenySockets is false: %v", err)
	}
	if err := v.CheckPath("/var/log/forbidden/file"); !errors.Is(err, hostpath.ErrDenied) {
		t.Fatalf("custom deny error = %v, want ErrDenied", err)
	}
}

func ExampleValidator() {
	v := hostpath.New(
		[]hostpath.Prefix{{Path: "/var/log", Writable: false}},
		nil,
		hostpath.SymlinkTable{"/var/log/host": "/"},
	)

	err := v.ValidateMount(
		hostpath.HostPathVolume{Path: "/var/log/../run/containerd"},
		hostpath.VolumeMount{ReadOnly: true},
		nil,
	)
	if errors.Is(err, hostpath.ErrWhitelist) {
		fmt.Println("rejected: folded path escapes whitelist")
	}
	// Output: rejected: folded path escapes whitelist
}

package netsec

import (
	"reflect"
	"strings"
	"testing"
)

func newTestChecker(t *testing.T, opts ...Option) *Checker {
	t.Helper()

	mountPoints, err := ParseMountPoints(strings.NewReader(sampleMountInfo))
	if err != nil {
		t.Fatalf("ParseMountPoints returned error: %v", err)
	}

	return NewChecker(mountPoints, []string{
		"sha256:credential-token",
		"sha256:team-password",
	}, opts...)
}

func TestCompliantLayerProducesNoFindings(t *testing.T) {
	checker := newTestChecker(t)

	layers := []LayerChanges{{
		Layer: "layer-0",
		Changes: []FileChange{
			{Path: "/app/bin/server", Digest: "sha256:binary"},
			{Path: "/etc/app/config.yaml", Digest: "sha256:config"},
			{Path: "/tmp/build-cache/object.o", Digest: "sha256:object"},
		},
	}}

	if findings := checker.Check(layers); findings != nil {
		t.Fatalf("compliant layer produced findings: %+v", findings)
	}
}

func TestMountPointWithSpaceIsProtected(t *testing.T) {
	checker := newTestChecker(t)

	layers := []LayerChanges{{
		Layer: "layer-1",
		Changes: []FileChange{
			{Path: "/mnt/team creds/id_rsa", Digest: "sha256:key"},
			{Path: "/mnt/team creds/sub/dir/notes.txt", Digest: "sha256:notes"},
		},
	}}

	findings := checker.Check(layers)
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2: %+v", len(findings), findings)
	}

	for _, finding := range findings {
		if finding.MountPoint != "/mnt/team creds" {
			t.Fatalf("mount point = %q, want %q", finding.MountPoint, "/mnt/team creds")
		}
		if !reflect.DeepEqual(finding.Kinds, []LeakKind{LeakMountPath}) {
			t.Fatalf("kinds = %v, want only %v", finding.Kinds, LeakMountPath)
		}
	}
}

func TestNestedMountPrefersMostSpecificMountPoint(t *testing.T) {
	checker := newTestChecker(t)

	layers := []LayerChanges{{
		Layer: "layer-2",
		Changes: []FileChange{
			{Path: "/mnt/data/file.bin", Digest: "sha256:file"},
			{Path: "/mnt/data/nested/inner.bin", Digest: "sha256:inner"},
			{Path: "/mnt/data/nested", Digest: "sha256:mountpoint-itself"},
		},
	}}

	findings := checker.Check(layers)
	if len(findings) != 3 {
		t.Fatalf("got %d findings, want 3: %+v", len(findings), findings)
	}

	wantMounts := map[string]string{
		"/mnt/data/file.bin":         "/mnt/data",
		"/mnt/data/nested/inner.bin": "/mnt/data/nested",
		"/mnt/data/nested":           "/mnt/data/nested",
	}
	for _, finding := range findings {
		want, ok := wantMounts[finding.Path]
		if !ok {
			t.Fatalf("unexpected finding path %q", finding.Path)
		}
		if finding.MountPoint != want {
			t.Fatalf("path %q matched %q, want %q", finding.Path, finding.MountPoint, want)
		}
	}
}

func TestCredentialCopiedOutOfMountLeaksByDigest(t *testing.T) {
	checker := newTestChecker(t)

	layers := []LayerChanges{{
		Layer: "layer-3",
		Changes: []FileChange{
			{Path: "/app/built/token", Digest: "sha256:credential-token"},
			{Path: "/var/lib/app/password.txt", Digest: "sha256:team-password"},
			{Path: "/app/built/normal.txt", Digest: "sha256:normal-content"},
		},
	}}

	findings := checker.Check(layers)
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2: %+v", len(findings), findings)
	}

	for _, finding := range findings {
		if len(finding.Kinds) != 1 || finding.Kinds[0] != LeakSensitiveDigest {
			t.Fatalf("path %q kinds = %v, want only %v", finding.Path, finding.Kinds, LeakSensitiveDigest)
		}
		if finding.MountPoint != "" {
			t.Fatalf("path %q should not report a mount point, got %q", finding.Path, finding.MountPoint)
		}
	}
}

func TestSameChangeCanViolateBothRules(t *testing.T) {
	checker := newTestChecker(t)

	layers := []LayerChanges{{
		Layer: "layer-4",
		Changes: []FileChange{
			{Path: "/var/run/secrets/kubernetes.io/serviceaccount/token", Digest: "sha256:credential-token"},
		},
	}}

	findings := checker.Check(layers)
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}

	wantKinds := []LeakKind{LeakMountPath, LeakSensitiveDigest}
	if !reflect.DeepEqual(findings[0].Kinds, wantKinds) {
		t.Fatalf("kinds = %v, want %v", findings[0].Kinds, wantKinds)
	}
	if findings[0].MountPoint != "/var/run/secrets/kubernetes.io/serviceaccount" {
		t.Fatalf("mount point = %q", findings[0].MountPoint)
	}
}

func TestBuiltinIgnorePathsAreNotEvaluated(t *testing.T) {
	checker := newTestChecker(t)

	layers := []LayerChanges{{
		Layer: "layer-5",
		Changes: []FileChange{
			{Path: "/kaniko/ssl/certs/ca-bundle.crt", Digest: "sha256:credential-token"},
			{Path: "/proc/self/mountinfo", Digest: "sha256:proc"},
			{Path: "/proc", Digest: "sha256:proc-root"},
			{Path: "/dev/null", Digest: "sha256:dev-null"},
		},
	}}

	if findings := checker.Check(layers); findings != nil {
		t.Fatalf("ignored paths produced findings: %+v", findings)
	}

	wantIgnores := []string{"/dev", "/kaniko", "/proc", "/sys"}
	if got := checker.IgnorePaths(); !reflect.DeepEqual(got, wantIgnores) {
		t.Fatalf("ignore paths = %v, want %v", got, wantIgnores)
	}
}

func TestExtraIgnorePaths(t *testing.T) {
	checker := newTestChecker(t, WithExtraIgnorePaths("/build/tmp"))

	layers := []LayerChanges{{
		Layer: "layer-6",
		Changes: []FileChange{
			{Path: "/build/tmp/secret", Digest: "sha256:credential-token"},
			{Path: "/build/out/app", Digest: "sha256:app"},
		},
	}}

	if findings := checker.Check(layers); findings != nil {
		t.Fatalf("extra-ignored path produced findings: %+v", findings)
	}
}

func TestRootMountCoversEveryPath(t *testing.T) {
	checker := NewChecker([]string{"/"}, []string{"sha256:secret"})

	layers := []LayerChanges{{
		Layer: "layer-7",
		Changes: []FileChange{
			{Path: "/app/server", Digest: "sha256:binary"},
		},
	}}

	findings := checker.Check(layers)
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	if findings[0].MountPoint != "/" {
		t.Fatalf("mount point = %q, want /", findings[0].MountPoint)
	}
}

func TestMultipleLayersAndCheckLayer(t *testing.T) {
	checker := newTestChecker(t)

	all := []LayerChanges{
		{
			Layer: "clean",
			Changes: []FileChange{
				{Path: "/app/bin/server", Digest: "sha256:binary"},
			},
		},
		{
			Layer: "dirty",
			Changes: []FileChange{
				{Path: "/app/token", Digest: "sha256:credential-token"},
			},
		},
	}

	if findings := checker.CheckLayer(all[0]); findings != nil {
		t.Fatalf("clean layer produced findings: %+v", findings)
	}

	findings := checker.Check(all)
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	if findings[0].Layer != "dirty" || findings[0].Path != "/app/token" {
		t.Fatalf("unexpected finding: %+v", findings[0])
	}
}

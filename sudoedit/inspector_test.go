package sudoedit

import (
	"os"
	"path/filepath"
	"testing"
)

// The default inspector must detect a writable target on the real filesystem.
func TestOSInspector_WritableTarget(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses ordinary write permission checks")
	}

	dir := t.TempDir()
	writable := filepath.Join(dir, "writable.conf")
	if err := os.WriteFile(writable, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	inspector := OSInspector{}
	if !inspector.IsWritable(writable) {
		t.Fatalf("expected %q to be writable by the caller", writable)
	}

	readOnly := filepath.Join(dir, "readonly.conf")
	if err := os.WriteFile(readOnly, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(readOnly, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o600) })

	if inspector.IsWritable(readOnly) {
		t.Fatalf("did not expect %q to be writable", readOnly)
	}
}

// A target reached through a symlinked parent directory must be rejected.
func TestAuthorize_RealFSSymlinkParent(t *testing.T) {
	root := t.TempDir()

	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(realDir, "policy.conf")
	if err := os.WriteFile(target, []byte("policy"), 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(target, 0o600) })

	link := filepath.Join(root, "linked")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}

	viaLink := filepath.Join(link, "policy.conf")
	req := Request{
		Env:             map[string]string{"EDITOR": "/usr/bin/vim"},
		Targets:         []string{viaLink},
		Authorized:      []string{root + "/"},
		EditorAllowlist: map[string]bool{"/usr/bin/vim": true},
	}

	decision := Authorize(req)
	if decision.Allowed {
		t.Fatalf("expected denial through symlinked parent %q", link)
	}
	if decision.Reason != ReasonUnsafePath || decision.DeniedPath != link {
		t.Fatalf("expected unsafe symlink %q, got reason=%q path=%q",
			link, decision.Reason, decision.DeniedPath)
	}
}

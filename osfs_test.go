package netsec

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOSFSIntegration(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(os.TempDir(), filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}

	osfs := &OSFS{
		Root: root,
		ResolveHandleFunc: func(handle string) (string, error) {
			if handle == "h" {
				return filepath.Join(root, "f.txt"), nil
			}
			return "", statusError(SSH_FX_BAD_MESSAGE, "unknown handle")
		},
	}
	a, err := New(Config{Root: root, FS: osfs, ReadOnly: false})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := a.CheckRead("/f.txt"); err != nil {
		t.Fatalf("read inside root: %v", err)
	}
	if code := mustStatusCode(t, a.CheckRead("/out/whatever")); code != SSH_FX_PERMISSION_DENIED {
		t.Fatalf("symlink escape status = %d, want permission denied", code)
	}
	if code := mustStatusCode(t, a.CheckRead("")); code != SSH_FX_BAD_MESSAGE {
		t.Fatalf("empty path status = %d, want bad message", code)
	}
	if err := a.CheckExtended(ExtendedRequest{Name: ExtFSetStat, Handle: "h"}); err != nil {
		t.Fatalf("fsetstat with valid handle: %v", err)
	}
	if code := mustStatusCode(t, a.CheckExtended(ExtendedRequest{
		Name: ExtFSetStat, Handle: "nope",
	})); code != SSH_FX_BAD_MESSAGE {
		t.Fatalf("bad handle status = %d, want bad message", code)
	}

	ro, err := New(Config{Root: root, FS: osfs, ReadOnly: true})
	if err != nil {
		t.Fatalf("New read-only: %v", err)
	}
	if code := mustStatusCode(t, ro.CheckWrite("/f.txt")); code != SSH_FX_PERMISSION_DENIED {
		t.Fatalf("read-only status = %d, want permission denied", code)
	}

	bare := &OSFS{Root: root}
	_, resolveErr := bare.ResolveHandle("h")
	if code := mustStatusCode(t, resolveErr); code != SSH_FX_BAD_MESSAGE {
		t.Fatalf("unconfigured resolver status = %d, want bad message", code)
	}
}

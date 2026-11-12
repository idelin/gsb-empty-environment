//go:build darwin || linux

package netsec_test

import (
	"os"
	"path/filepath"
	"testing"

	netsec "example.org/netsec/q0263"
)

func tempBase(t *testing.T) string {
	t.Helper()
	base, err := os.MkdirTemp(".", "rootdir-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	return base
}

func TestRootDirReclaim(t *testing.T) {
	base := tempBase(t)

	outside := filepath.Join(base, "outside")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "root")
	if err := os.MkdirAll(filepath.Join(root, "sub", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		filepath.Join(root, "escape"):       outside,
		filepath.Join(root, "loop"):         root,
		filepath.Join(root, "sub", "self"):  ".",
		filepath.Join(root, "sub", "cycle"): "cycle",
	} {
		if err := os.Symlink(target, name); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{
		filepath.Join(root, "top"),
		filepath.Join(root, "sub", "deep", "leaf"),
	} {
		if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	d, err := netsec.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := netsec.Reclaim(d, nil)
	if cerr := d.Close(); cerr != nil {
		t.Fatal(cerr)
	}
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if rep.FilesRemoved != 6 || rep.DirsRemoved != 2 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("root not empty: %v", entries)
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "secret" {
		t.Fatalf("outside object was touched: %v %q", err, data)
	}
}

func TestRootDirReclaimChild(t *testing.T) {
	base := tempBase(t)
	if err := os.MkdirAll(filepath.Join(base, "victim", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "victim", "sub", "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	parent, err := netsec.Open(base)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := netsec.ReclaimChild(parent, "victim", nil)
	if cerr := parent.Close(); cerr != nil {
		t.Fatal(cerr)
	}
	if err != nil {
		t.Fatalf("reclaim child: %v", err)
	}
	if rep.FilesRemoved != 1 || rep.DirsRemoved != 2 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if _, err := os.Lstat(filepath.Join(base, "victim")); !os.IsNotExist(err) {
		t.Fatalf("child still exists: %v", err)
	}
}

func TestRootDirOpenRejectsSymlink(t *testing.T) {
	base := tempBase(t)
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	d, err := netsec.Open(link)
	if err == nil {
		d.Close()
		t.Fatal("Open followed a symlink")
	}
}

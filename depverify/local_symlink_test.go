package depverify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalReplacementSymlinkEscapeRejected(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.go"), []byte("package secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module "+modA+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.go"), filepath.Join(dir, "link.go")); err != nil {
		t.Fatal(err)
	}
	gomod := "module app\n\nrequire " + modA + " " + verA + "\n\n" +
		"replace " + modA + " " + verA + " => " + dir + "\n"

	v := New(Options{LocalReplacements: []LocalReplacement{
		{Path: modA, Version: verA, Dir: dir},
	}})
	if _, err := v.Verify(context.Background(), gomod, "", newFakeFetcher()); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("expected ErrInvalidSource for escaping symlink, got %v", err)
	}
}

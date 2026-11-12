package netsec

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func newTestAuthorizer(t *testing.T, root string, readOnly bool) *Authorizer {
	t.Helper()
	authorizer, err := New(Config{
		Root:     root,
		ReadOnly: readOnly,
		FS:       OSFS{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return authorizer
}

func writeTestFile(t *testing.T, name string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(name, []byte("data"), mode); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func requireStatus(t *testing.T, err error, want Status) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected status %d, got nil", want)
	}
	if got := ErrorStatus(err); got != want {
		t.Fatalf("status = %d (%v), want %d", got, err, want)
	}
}

func TestReadOnlyDeniesEverySupportedExtension(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "source"), 0o644)
	writeTestFile(t, filepath.Join(root, "target"), 0o644)

	authorizer := newTestAuthorizer(t, root, true)

	requests := []ExtensionRequest{
		{Name: ExtPosixRename, Source: "source", Target: "target"},
		{Name: ExtHardlink, Source: "source", Target: "target"},
		{Name: ExtCopyData, Source: "source", Target: "target"},
		{Name: ExtFsetstat, Path: "target"},
	}
	for _, request := range requests {
		err := authorizer.AuthorizeExtension(request)
		requireStatus(t, err, SSH_FX_PERMISSION_DENIED)
	}
}

func TestReadOnlyDeniesPositionalExtensionDispatch(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "source"), 0o644)

	authorizer := newTestAuthorizer(t, root, true)
	requireStatus(t, authorizer.AuthorizeExtensionPaths(ExtPosixRename, "source", "target"), SSH_FX_PERMISSION_DENIED)
	requireStatus(t, authorizer.AuthorizeExtensionPaths(ExtHardlink, "source", "target"), SSH_FX_PERMISSION_DENIED)
	requireStatus(t, authorizer.AuthorizeExtensionPaths(ExtCopyData, "source", "target"), SSH_FX_PERMISSION_DENIED)
	requireStatus(t, authorizer.AuthorizeExtensionPaths(ExtFsetstat, "source"), SSH_FX_PERMISSION_DENIED)
}

func TestRenameCannotEscapeRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "dir", "file"), 0o644)

	authorizer := newTestAuthorizer(t, root, false)

	for _, target := range []string{
		"../outside",
		"../../outside",
		"/../outside",
		root + string(filepath.Separator) + ".." + string(filepath.Separator) + "outside",
	} {
		err := authorizer.AuthorizeRename("dir/file", target)
		requireStatus(t, err, SSH_FX_PERMISSION_DENIED)
		if !errors.Is(err, ErrRootEscape) {
			t.Fatalf("rename to %q: expected ErrRootEscape, got %v", target, err)
		}
	}
}

func TestRenameRequiresWriteOnBothDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "source-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "source-dir", "file"), 0o644)
	if err := os.Mkdir(filepath.Join(root, "target-dir"), 0o755); err != nil {
		t.Fatal(err)
	}

	authorizer := newTestAuthorizer(t, root, false)
	if err := authorizer.AuthorizeRename("source-dir/file", "target-dir/file"); err != nil {
		t.Fatalf("writable rename: %v", err)
	}
	if err := os.Chmod(filepath.Join(root, "source-dir", "file"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := authorizer.AuthorizeRename("source-dir/file", "target-dir/file"); err != nil {
		t.Fatalf("rename does not require source-file write permission: %v", err)
	}

	if err := os.Chmod(filepath.Join(root, "target-dir"), 0o555); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, authorizer.AuthorizeRename("source-dir/file", "target-dir/file"), SSH_FX_PERMISSION_DENIED)

	if err := os.Chmod(filepath.Join(root, "target-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "source-dir"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Join(root, "source-dir"), 0o755)
	})
	requireStatus(t, authorizer.AuthorizeRename("source-dir/file", "target-dir/file"), SSH_FX_PERMISSION_DENIED)
}

func TestHardlinkToUnreadableFileIsDenied(t *testing.T) {
	root := t.TempDir()
	secret := filepath.Join(root, "secret")
	writeTestFile(t, secret, 0o200)
	if err := os.Chmod(secret, 0o200); err != nil {
		t.Fatal(err)
	}

	authorizer := newTestAuthorizer(t, root, false)
	err := authorizer.AuthorizeExtension(ExtensionRequest{
		Name:   ExtHardlink,
		Source: "secret",
		Target: "link",
	})
	requireStatus(t, err, SSH_FX_PERMISSION_DENIED)
}

func TestHardlinkRequiresTargetDirectoryWrite(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "source"), 0o644)
	if err := os.Mkdir(filepath.Join(root, "target-dir"), 0o755); err != nil {
		t.Fatal(err)
	}

	authorizer := newTestAuthorizer(t, root, false)
	if err := authorizer.AuthorizeHardlink("source", "target-dir/link"); err != nil {
		t.Fatalf("writable hardlink: %v", err)
	}

	if err := os.Chmod(filepath.Join(root, "target-dir"), 0o555); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, authorizer.AuthorizeHardlink("source", "target-dir/link"), SSH_FX_PERMISSION_DENIED)
}

func TestCopyDataRequiresSourceReadAndTargetWrite(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "source"), 0o644)
	writeTestFile(t, filepath.Join(root, "target"), 0o644)

	authorizer := newTestAuthorizer(t, root, false)
	if err := authorizer.AuthorizeCopyData("source", "target"); err != nil {
		t.Fatalf("readable source and writable target: %v", err)
	}

	if err := os.Chmod(filepath.Join(root, "source"), 0o200); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, authorizer.AuthorizeCopyData("source", "target"), SSH_FX_PERMISSION_DENIED)

	if err := os.Chmod(filepath.Join(root, "source"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "target"), 0o444); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, authorizer.AuthorizeCopyData("source", "target"), SSH_FX_PERMISSION_DENIED)
}

func TestFsetstatRequiresTargetWrite(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "target"), 0o644)

	authorizer := newTestAuthorizer(t, root, false)
	if err := authorizer.AuthorizeFsetstat("target"); err != nil {
		t.Fatalf("writable fsetstat: %v", err)
	}

	if err := os.Chmod(filepath.Join(root, "target"), 0o444); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, authorizer.AuthorizeFsetstat("target"), SSH_FX_PERMISSION_DENIED)
}

func TestUnknownExtensionsAreUnsupported(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "source"), 0o644)
	authorizer := newTestAuthorizer(t, root, false)

	for _, name := range []string{
		"posix-rename@evil.example",
		"hardlink@openssh.com.evil",
		"copy-data@openssh.com",
		"fsetstat@openssh.com.evil",
		"POSIX-RENAME@openssh.com",
		"arbitrary@domain",
	} {
		err := authorizer.AuthorizeExtension(ExtensionRequest{
			Name:   name,
			Source: "source",
			Target: "target",
		})
		requireStatus(t, err, SSH_FX_OP_UNSUPPORTED)
	}

	requireStatus(t, authorizer.AuthorizeExtensionPaths("extension@domain", "source", "target"), SSH_FX_OP_UNSUPPORTED)

	readOnly := newTestAuthorizer(t, root, true)
	requireStatus(t, readOnly.AuthorizeExtension(ExtensionRequest{Name: "unknown@domain"}), SSH_FX_OP_UNSUPPORTED)
}

func TestInjectedPolicyControlsHardlinkSourceRead(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "secret"), 0o644)
	writeTestFile(t, filepath.Join(root, "public"), 0o644)

	policy := AccessPolicyFunc(func(path string, permission Permission, info fs.FileInfo) error {
		if permission&Read != 0 && info.Name() == "secret" {
			return ErrPermissionDenied
		}
		return nil
	})
	authorizer, err := New(Config{Root: root, FS: OSFS{}, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}

	requireStatus(t, authorizer.AuthorizeHardlink("secret", "secret-link"), SSH_FX_PERMISSION_DENIED)
	if err := authorizer.AuthorizeHardlink("public", "public-link"); err != nil {
		t.Fatalf("allowed source should pass: %v", err)
	}
}

func TestSymlinkEscapeIsRejected(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "inside"), 0o644)
	writeTestFile(t, filepath.Join(outside, "secret"), 0o644)

	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "absolute-escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside/secret", filepath.Join(root, "relative-escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("inside", filepath.Join(root, "inside-link")); err != nil {
		t.Fatal(err)
	}

	authorizer := newTestAuthorizer(t, root, false)

	requireStatus(t, authorizer.AuthorizeRead("absolute-escape"), SSH_FX_PERMISSION_DENIED)
	requireStatus(t, authorizer.AuthorizeRead("relative-escape"), SSH_FX_PERMISSION_DENIED)
	requireStatus(t, authorizer.AuthorizeFsetstat("absolute-escape"), SSH_FX_PERMISSION_DENIED)
	if err := authorizer.AuthorizeRead("inside-link"); err != nil {
		t.Fatalf("in-root symlink should be allowed: %v", err)
	}

	// A symlinked parent must also be resolved before directory permissions
	// are checked for rename.
	if err := os.Symlink(outside, filepath.Join(root, "escape-dir")); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, authorizer.AuthorizeRename("inside", "escape-dir/moved"), SSH_FX_PERMISSION_DENIED)
}

func TestNormalizedPathsStayInsideRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "dir", "file"), 0o644)

	authorizer := newTestAuthorizer(t, root, false)
	if err := authorizer.AuthorizeRead("dir/../dir/./file"); err != nil {
		t.Fatalf("normalized in-root path: %v", err)
	}
	requireStatus(t, authorizer.AuthorizeRead("../file"), SSH_FX_PERMISSION_DENIED)
	requireStatus(t, authorizer.AuthorizeRead("dir/../../file"), SSH_FX_PERMISSION_DENIED)
}

func TestCreationRequiresParentDirectoryWrite(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	authorizer := newTestAuthorizer(t, root, false)

	if err := authorizer.AuthorizeWrite("dir/new-file"); err != nil {
		t.Fatalf("writable parent: %v", err)
	}
	if err := os.Chmod(filepath.Join(root, "dir"), 0o555); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, authorizer.AuthorizeWrite("dir/new-file"), SSH_FX_PERMISSION_DENIED)
}

func TestConfigValidation(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("New should require FS and Root")
	}
	if _, err := New(Config{Root: t.TempDir()}); err == nil {
		t.Fatal("New should require FS")
	}
	if _, err := New(Config{FS: OSFS{}}); err == nil {
		t.Fatal("New should require Root")
	}
}

package netsec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestMkdirAllAtWritesUploadInsideTenantRoot(t *testing.T) {
	base := t.TempDir()
	tenant := makeDir(t, base, "tenant")
	outside := makeDir(t, base, "outside")
	root := openDir(t, tenant)

	dir, err := MkdirAllAt(int(root.Fd()), "accounts/2026/11/12")
	if err != nil {
		t.Fatalf("MkdirAllAt: %v", err)
	}
	t.Cleanup(func() { _ = dir.Close() })

	upload, err := CreateFileAt(int(dir.Fd()), "payload.bin", 0o600)
	if err != nil {
		t.Fatalf("CreateFileAt: %v", err)
	}
	if _, err := upload.WriteString("uploaded payload"); err != nil {
		t.Fatalf("write upload: %v", err)
	}
	if err := upload.Close(); err != nil {
		t.Fatalf("close upload: %v", err)
	}
	if err := dir.Close(); err != nil {
		t.Fatalf("close directory: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(tenant, "accounts", "2026", "11", "12", "payload.bin"))
	if err != nil {
		t.Fatalf("read upload: %v", err)
	}
	if string(got) != "uploaded payload" {
		t.Fatalf("upload contents = %q", got)
	}
	requireEmptyDir(t, outside)
}

func TestMkdirAllAtRejectsUnsafePathsBeforeFilesystemAccess(t *testing.T) {
	tests := []string{
		"",
		"/absolute",
		"..",
		"../outside",
		"tenant/../outside",
		".",
		"tenant/.",
		"tenant//nested",
		"tenant/",
		"tenant\x00nested",
	}
	for _, relPath := range tests {
		t.Run(fmt.Sprintf("%q", relPath), func(t *testing.T) {
			fs := &unexpectedFS{}
			_, err := MkdirAllAtFS(fs, 123, relPath)
			if !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("error = %v, want ErrUnsafePath", err)
			}
		})
	}
}

func TestMkdirAllAtRejectsExistingLinksAndFiles(t *testing.T) {
	base := t.TempDir()
	tenant := makeDir(t, base, "tenant")
	outside := makeDir(t, base, "outside")
	root := openDir(t, tenant)

	if err := os.Symlink(outside, filepath.Join(tenant, "linked")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	if _, err := MkdirAllAt(int(root.Fd()), "linked/nested"); err == nil {
		t.Fatal("MkdirAllAt followed an existing symlink")
	}

	if err := os.WriteFile(filepath.Join(tenant, "plain-file"), []byte("keep"), 0o600); err != nil {
		t.Fatalf("create regular file: %v", err)
	}
	if _, err := MkdirAllAt(int(root.Fd()), "plain-file/nested"); err == nil {
		t.Fatal("MkdirAllAt traversed an existing regular file")
	}

	got, err := os.ReadFile(filepath.Join(tenant, "plain-file"))
	if err != nil {
		t.Fatalf("read regular file: %v", err)
	}
	if string(got) != "keep" {
		t.Fatalf("regular file changed to %q", got)
	}
	requireEmptyDir(t, outside)
}

func TestMkdirAllAtRejectsNonDirectoryRoot(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "not-a-directory")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write root file: %v", err)
	}
	root, err := os.Open(path)
	if err != nil {
		t.Fatalf("open root file: %v", err)
	}
	defer root.Close()

	_, err = MkdirAllAt(int(root.Fd()), "nested")
	if !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("error = %v, want ErrNotDirectory", err)
	}
}

func TestInjectedSwapBetweenSegmentsRejectsSymlink(t *testing.T) {
	base := t.TempDir()
	tenant := makeDir(t, base, "tenant")
	outside := makeDir(t, base, "outside")
	root := openDir(t, tenant)

	fs := &hookFS{
		FileSystem: OSFileSystem{},
		hook: func(_ int, name string) {
			if name != "a" {
				return
			}
			if err := os.RemoveAll(filepath.Join(tenant, "a")); err != nil {
				t.Fatalf("remove directory during traversal: %v", err)
			}
			if err := os.Symlink(outside, filepath.Join(tenant, "a")); err != nil {
				t.Fatalf("replace directory with symlink: %v", err)
			}
		},
	}

	_, err := MkdirAllAtFS(fs, int(root.Fd()), "a/b")
	if err == nil {
		t.Fatal("MkdirAllAt accepted a directory replaced by a symlink")
	}
	requireEmptyDir(t, outside)
}

func TestInjectedSymlinkAtNextSegmentIsRejected(t *testing.T) {
	base := t.TempDir()
	tenant := makeDir(t, base, "tenant")
	outside := makeDir(t, base, "outside")
	root := openDir(t, tenant)

	fs := &hookFS{
		FileSystem: OSFileSystem{},
		hook: func(_ int, name string) {
			if name != "b" {
				return
			}
			if err := os.Remove(filepath.Join(tenant, "a", "b")); err != nil {
				t.Fatalf("remove next segment: %v", err)
			}
			if err := os.Symlink(outside, filepath.Join(tenant, "a", "b")); err != nil {
				t.Fatalf("create next-segment symlink: %v", err)
			}
		},
	}

	_, err := MkdirAllAtFS(fs, int(root.Fd()), "a/b/c")
	if err == nil {
		t.Fatal("MkdirAllAt accepted a symlink at the next segment")
	}
	requireEmptyDir(t, outside)
}

func TestInjectedDeleteRecreateSymlinkIsRejected(t *testing.T) {
	base := t.TempDir()
	tenant := makeDir(t, base, "tenant")
	outside := makeDir(t, base, "outside")
	root := openDir(t, tenant)

	fs := &hookFS{
		FileSystem: OSFileSystem{},
		hook: func(_ int, name string) {
			if name != "a" {
				return
			}
			if err := os.Remove(filepath.Join(tenant, "a")); err != nil {
				t.Fatalf("remove directory: %v", err)
			}
			if err := os.Mkdir(filepath.Join(tenant, "a"), 0o700); err != nil {
				t.Fatalf("recreate directory: %v", err)
			}
			if err := os.Symlink(outside, filepath.Join(tenant, "a", "b")); err != nil {
				t.Fatalf("create link in recreated directory: %v", err)
			}
		},
	}

	_, err := MkdirAllAtFS(fs, int(root.Fd()), "a/b")
	if err == nil {
		t.Fatal("MkdirAllAt accepted a deleted and recreated symlink")
	}
	requireEmptyDir(t, outside)
}

func TestInjectedDeleteRecreateRealDirectoryStaysInsideRoot(t *testing.T) {
	base := t.TempDir()
	tenant := makeDir(t, base, "tenant")
	outside := makeDir(t, base, "outside")
	root := openDir(t, tenant)

	replaced := false
	fs := &hookFS{
		FileSystem: OSFileSystem{},
		hook: func(_ int, name string) {
			if name != "a" || replaced {
				return
			}
			replaced = true
			if err := os.Remove(filepath.Join(tenant, "a")); err != nil {
				t.Fatalf("remove directory: %v", err)
			}
			if err := os.Mkdir(filepath.Join(tenant, "a"), 0o700); err != nil {
				t.Fatalf("recreate directory: %v", err)
			}
		},
	}

	dir, err := MkdirAllAtFS(fs, int(root.Fd()), "a/b")
	if err != nil {
		t.Fatalf("MkdirAllAtFS: %v", err)
	}
	defer dir.Close()
	upload, err := CreateFileAt(int(dir.Fd()), "upload", 0o600)
	if err != nil {
		t.Fatalf("CreateFileAt: %v", err)
	}
	if _, err := upload.WriteString("inside"); err != nil {
		t.Fatalf("write upload: %v", err)
	}
	if err := upload.Close(); err != nil {
		t.Fatalf("close upload: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(tenant, "a", "b", "upload"))
	if err != nil {
		t.Fatalf("read recreated path: %v", err)
	}
	if string(got) != "inside" {
		t.Fatalf("upload contents = %q", got)
	}
	requireEmptyDir(t, outside)
}

func TestAnchoredDirectoryFDPreventsWriteAfterAncestorSwap(t *testing.T) {
	base := t.TempDir()
	tenant := makeDir(t, base, "tenant")
	outside := makeDir(t, base, "outside")
	if err := os.Mkdir(filepath.Join(outside, "reports"), 0o700); err != nil {
		t.Fatalf("prepare outside path: %v", err)
	}
	root := openDir(t, tenant)

	dir, err := MkdirAllAt(int(root.Fd()), "team/reports")
	if err != nil {
		t.Fatalf("MkdirAllAt: %v", err)
	}
	defer dir.Close()

	if err := os.Rename(filepath.Join(tenant, "team"), filepath.Join(tenant, "team-original")); err != nil {
		t.Fatalf("rename tenant subtree: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(tenant, "team")); err != nil {
		t.Fatalf("replace ancestor path with symlink: %v", err)
	}

	upload, err := CreateFileAt(int(dir.Fd()), "upload", 0o600)
	if err != nil {
		t.Fatalf("CreateFileAt: %v", err)
	}
	if _, err := upload.WriteString("anchored"); err != nil {
		t.Fatalf("write upload: %v", err)
	}
	if err := upload.Close(); err != nil {
		t.Fatalf("close upload: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(tenant, "team-original", "reports", "upload"))
	if err != nil {
		t.Fatalf("read anchored upload: %v", err)
	}
	if string(got) != "anchored" {
		t.Fatalf("upload contents = %q", got)
	}
	if _, err := os.Lstat(filepath.Join(outside, "reports", "upload")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside write exists or stat failed unexpectedly: %v", err)
	}
}

func TestEveryDirectoryOpenUsesNoFollowAndDirectoryFlags(t *testing.T) {
	base := t.TempDir()
	tenant := makeDir(t, base, "tenant")
	root := openDir(t, tenant)

	fs := &recordingFS{FileSystem: OSFileSystem{}}
	dir, err := MkdirAllAtFS(fs, int(root.Fd()), "a/b/c")
	if err != nil {
		t.Fatalf("MkdirAllAtFS: %v", err)
	}
	defer dir.Close()

	if len(fs.openFlags) != 3 {
		t.Fatalf("openat calls = %d, want 3", len(fs.openFlags))
	}
	for i, flags := range fs.openFlags {
		if flags&DirOpenFlags != DirOpenFlags {
			t.Fatalf("openat call %d flags = %#x, want at least %#x", i, flags, DirOpenFlags)
		}
		if flags&syscall.O_NOFOLLOW == 0 || flags&syscall.O_DIRECTORY == 0 {
			t.Fatalf("openat call %d lacks no-follow or directory flag: %#x", i, flags)
		}
	}
}

func TestFstatTypeCheckRejectsInjectedRegularFile(t *testing.T) {
	fs := &typeFS{openedFD: 99}
	_, err := MkdirAllAtFS(fs, 42, "a")
	if !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("error = %v, want ErrNotDirectory", err)
	}
	if fs.closedFD != 99 {
		t.Fatalf("closed fd = %d, want 99", fs.closedFD)
	}
}

func TestCreateFileAtRejectsExistingSymlink(t *testing.T) {
	base := t.TempDir()
	tenant := makeDir(t, base, "tenant")
	outside := makeDir(t, base, "outside")
	target := filepath.Join(outside, "target")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatalf("write outside target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(tenant, "upload")); err != nil {
		t.Fatalf("create upload symlink: %v", err)
	}
	root := openDir(t, tenant)

	if _, err := CreateFileAt(int(root.Fd()), "upload", 0o600); err == nil {
		t.Fatal("CreateFileAt followed an existing upload symlink")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read outside target: %v", err)
	}
	if string(got) != "outside" {
		t.Fatalf("outside target changed to %q", got)
	}
}

type unexpectedFS struct{}

func (*unexpectedFS) Mkdirat(int, string, uint32) error {
	panic("Mkdirat called for an unsafe path")
}

func (*unexpectedFS) Openat(int, string, int, uint32) (int, error) {
	panic("Openat called for an unsafe path")
}

func (*unexpectedFS) Fstat(int) (FileStat, error) {
	panic("Fstat called for an unsafe path")
}

func (*unexpectedFS) Close(int) error {
	panic("Close called for an unsafe path")
}

type hookFS struct {
	FileSystem
	hook func(dirFD int, name string)
}

func (f *hookFS) Mkdirat(dirFD int, name string, mode uint32) error {
	err := f.FileSystem.Mkdirat(dirFD, name, mode)
	if err == nil || errors.Is(err, syscall.EEXIST) {
		f.hook(dirFD, name)
	}
	return err
}

type recordingFS struct {
	FileSystem
	openFlags []int
}

func (f *recordingFS) Openat(dirFD int, name string, flags int, mode uint32) (int, error) {
	f.openFlags = append(f.openFlags, flags)
	return f.FileSystem.Openat(dirFD, name, flags, mode)
}

type typeFS struct {
	openedFD int
	closedFD int
}

func (*typeFS) Mkdirat(int, string, uint32) error {
	return nil
}

func (f *typeFS) Openat(int, string, int, uint32) (int, error) {
	return f.openedFD, nil
}

func (*typeFS) Fstat(fd int) (FileStat, error) {
	if fd == 42 {
		return FileStat{Mode: syscall.S_IFDIR}, nil
	}
	return FileStat{Mode: syscall.S_IFREG}, nil
}

func (f *typeFS) Close(fd int) error {
	f.closedFD = fd
	return nil
}

func makeDir(t *testing.T, parent, name string) string {
	t.Helper()
	path := filepath.Join(parent, name)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	return path
}

func openDir(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func requireEmptyDir(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("read directory %s: %v", path, err)
	}
	if len(entries) != 0 {
		t.Fatalf("directory %s is not empty: %v", path, entries)
	}
}

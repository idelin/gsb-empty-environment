package netsec

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type dropFixture struct {
	t       *testing.T
	root    string
	outside string
	target  *Target
}

func newDropFixture(t *testing.T, maxSize int64) *dropFixture {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "drop-root")
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	target, err := NewTarget(Policy{Root: root, MaxFileSize: maxSize})
	if err != nil {
		t.Fatalf("NewTarget: %v", err)
	}
	return &dropFixture{t: t, root: target.Root(), outside: outside, target: target}
}

func (f *dropFixture) receive(relPath, content string) (string, error) {
	return f.target.ReceiveFile(context.Background(), relPath, strings.NewReader(content))
}

func (f *dropFixture) read(relPath string) string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.root, relPath))
	if err != nil {
		f.t.Fatalf("read %s: %v", relPath, err)
	}
	return string(data)
}

func (f *dropFixture) assertExists(relPath string) {
	f.t.Helper()
	if _, err := os.Lstat(filepath.Join(f.root, relPath)); err != nil {
		f.t.Fatalf("expected %s to exist: %v", relPath, err)
	}
}

func (f *dropFixture) assertNotExists(relPath string) {
	f.t.Helper()
	if _, err := os.Lstat(filepath.Join(f.root, relPath)); !errors.Is(err, fs.ErrNotExist) {
		f.t.Fatalf("expected %s to be absent, got err=%v", relPath, err)
	}
}

// snapshotDir 对目录做 路径->内容 快照，用于证明攻击没有在目标目录之外落盘。
func snapshotDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			snap[rel+"/"] = ""
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			target, _ := os.Readlink(path)
			snap[rel] = "symlink->" + target
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snap[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", dir, err)
	}
	return snap
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestReceiveLegalDrop(t *testing.T) {
	f := newDropFixture(t, 1024)

	rel, err := f.receive("docs/report/notes.txt", "hello drop")
	if err != nil {
		t.Fatalf("ReceiveFile: %v", err)
	}
	if rel != filepath.Join("docs", "report", "notes.txt") {
		t.Fatalf("unexpected relative path %q", rel)
	}
	if got := f.read(filepath.Join("docs", "report", "notes.txt")); got != "hello drop" {
		t.Fatalf("content = %q", got)
	}
	info, err := os.Stat(filepath.Join(f.root, "docs", "report", "notes.txt"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode()&0o111 != 0 {
		t.Fatalf("dropped file must not be executable, mode=%o", info.Mode())
	}

	// 点号片段与多余分隔符应被规范化为根目录内路径。
	if _, err := f.receive("./a//b/./c.txt", "x"); err != nil {
		t.Fatalf("normalized legal path rejected: %v", err)
	}
	f.assertExists(filepath.Join("a", "b", "c.txt"))
}

func TestRejectAbsoluteAndVolumePaths(t *testing.T) {
	f := newDropFixture(t, 1024)
	malicious := []string{
		"/etc/passwd",
		"/tmp/absolute.bin",
		`C:\Windows\System32\evil.dll`,
		"c:/windows/system32/evil.dll",
		`\\server\share\evil.exe`,
	}
	before := snapshotDir(t, f.outside)
	for _, path := range malicious {
		if _, err := f.receive(path, "x"); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("path %q: want ErrInvalidPath, got %v", path, err)
		}
	}
	if got := snapshotDir(t, f.outside); !mapsEqual(got, before) {
		t.Fatalf("outside directory changed: before=%v after=%v", before, got)
	}
	// 根目录下除临时目录外不应有任何攻击产物。
	f.assertNotExists("etc")
	f.assertNotExists("tmp")
	f.assertNotExists("C:")
}

func TestRejectDirectoryTraversal(t *testing.T) {
	f := newDropFixture(t, 1024)
	malicious := []string{
		"../escape.txt",
		"../../escape.txt",
		"a/../../escape.txt",
		"a/b/../../../escape.txt",
		"docs/../../escape.txt",
		"",
		".",
	}
	before := snapshotDir(t, f.outside)
	for _, path := range malicious {
		if _, err := f.receive(path, "x"); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("path %q: want ErrInvalidPath, got %v", path, err)
		}
	}
	if got := snapshotDir(t, f.outside); !mapsEqual(got, before) {
		t.Fatalf("outside directory changed by traversal: %v", got)
	}

	// 非逃逸的 ".." 只抵消同级目录，规范化后必须落在根目录内。
	if _, err := f.receive("docs/../inside/escape.txt", "safe"); err != nil {
		t.Fatalf("non-escaping traversal should be contained: %v", err)
	}
	f.assertExists(filepath.Join("inside", "escape.txt"))
	if got := snapshotDir(t, f.outside); !mapsEqual(got, before) {
		t.Fatalf("outside directory changed by contained drop: %v", got)
	}
}

func TestRejectSymlinkEscape(t *testing.T) {
	f := newDropFixture(t, 1024)

	// 目录符号链接指向根目录之外。
	evilDir := filepath.Join(f.root, "evil-dir")
	if err := os.Symlink(f.outside, evilDir); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	// 文件符号链接指向根目录之外的既有文件。
	outsideFile := filepath.Join(f.outside, "target.txt")
	if err := os.WriteFile(outsideFile, []byte("original"), 0o644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}
	evilFile := filepath.Join(f.root, "evil-link.txt")
	if err := os.Symlink(outsideFile, evilFile); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	before := snapshotDir(t, f.outside)
	if _, err := f.receive("evil-dir/pwn.txt", "pwn"); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("dir symlink: want ErrOutsideRoot, got %v", err)
	}
	if _, err := f.receive("evil-link.txt", "pwn"); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("file symlink: want ErrOutsideRoot, got %v", err)
	}
	// 多级穿越 + 逃逸链接组合。
	if _, err := f.receive("evil-dir/../evil-dir/pwn2.txt", "pwn"); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("nested symlink traversal: want ErrOutsideRoot, got %v", err)
	}
	if got := snapshotDir(t, f.outside); !mapsEqual(got, before) {
		t.Fatalf("outside directory changed via symlink: before=%v after=%v", before, got)
	}
	if data, err := os.ReadFile(outsideFile); err != nil || string(data) != "original" {
		t.Fatalf("outside target file tampered: data=%q err=%v", data, err)
	}
}

func TestAllowInternalSymlink(t *testing.T) {
	f := newDropFixture(t, 1024)

	// 链接目标位于根目录内部：解析后仍在根内，属于合法拖放。
	innerDir := filepath.Join(f.root, "real-dir")
	if err := os.Mkdir(innerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(f.root, "inner-link")
	if err := os.Symlink(innerDir, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := f.receive("inner-link/inside.txt", "ok"); err != nil {
		t.Fatalf("internal symlink should be allowed: %v", err)
	}
	if got := f.read(filepath.Join("real-dir", "inside.txt")); got != "ok" {
		t.Fatalf("content = %q", got)
	}
}

func TestRejectExecutableTypes(t *testing.T) {
	f := newDropFixture(t, 1024)
	denied := []string{
		"payload.exe", "PAYLOAD.EXE", "tool.sh", "macro.ps1",
		"libnative.dll", "run.App", "bundle.JAR",
	}
	for _, name := range denied {
		if _, err := f.receive(name, "binary"); !errors.Is(err, ErrExecutableDenied) {
			t.Fatalf("%s: want ErrExecutableDenied, got %v", name, err)
		}
		f.assertNotExists(name)
	}

	// 自定义策略：仅拦截 .dat，默认清单不再生效。
	custom, err := NewTarget(Policy{
		Root:             f.root,
		MaxFileSize:      1024,
		DeniedExtensions: []string{"dat"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := custom.ReceiveFile(context.Background(), "okay.exe", strings.NewReader("x")); err != nil {
		t.Fatalf("custom policy should allow .exe: %v", err)
	}
	if _, err := custom.ReceiveFile(context.Background(), "blocked.dat", strings.NewReader("x")); !errors.Is(err, ErrExecutableDenied) {
		t.Fatalf("custom .dat: want ErrExecutableDenied, got %v", err)
	}

	// 显式空清单：扩展名检查全部放行。
	open, err := NewTarget(Policy{Root: f.root, MaxFileSize: 1024, DeniedExtensions: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := open.ReceiveFile(context.Background(), "anything.exe", strings.NewReader("x")); err != nil {
		t.Fatalf("empty deny list should allow all extensions: %v", err)
	}
}

func TestEnforceSizeLimit(t *testing.T) {
	f := newDropFixture(t, 4)
	if _, err := f.receive("small.txt", "abcd"); err != nil {
		t.Fatalf("within-limit file rejected: %v", err)
	}
	if _, err := f.receive("big.txt", "abcde"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized file: want ErrTooLarge, got %v", err)
	}
	f.assertNotExists("big.txt")

	// 超限或空内容后，临时目录不得残留文件。
	entries, err := os.ReadDir(filepath.Join(f.root, ".drop-staging"))
	if err != nil {
		t.Fatalf("staging dir: %v", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			t.Fatalf("staging dir leaked temporary file: %s", entry.Name())
		}
	}

	if _, err := f.receive("empty.txt", ""); !errors.Is(err, ErrEmptyContent) {
		t.Fatalf("empty content: want ErrEmptyContent, got %v", err)
	}
	f.assertNotExists("empty.txt")
}

func TestAtomicOverwrite(t *testing.T) {
	f := newDropFixture(t, 1024)
	dest := "data.txt"
	if _, err := f.receive(dest, "version-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.receive(dest, "version-2-longer"); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if got := f.read(dest); got != "version-2-longer" {
		t.Fatalf("overwritten content = %q", got)
	}

	// 覆盖一个根内符号链接本身（而非顺着链接写入其目标）。
	link := filepath.Join(f.root, "link.txt")
	target := filepath.Join(f.root, "data.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := f.receive("link.txt", "replaced"); err != nil {
		t.Fatalf("replace symlink: %v", err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat replaced link: %v", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		t.Fatal("rename must replace the symlink itself, not follow it")
	}
	if got := f.read("link.txt"); got != "replaced" {
		t.Fatalf("replaced content = %q", got)
	}
	if got := f.read("data.txt"); got != "version-2-longer" {
		t.Fatalf("symlink target must be untouched, got %q", got)
	}
}

func TestRenameWithinRoot(t *testing.T) {
	f := newDropFixture(t, 1024)
	if _, err := f.receive("old.txt", "move me"); err != nil {
		t.Fatal(err)
	}
	if err := f.target.Rename("old.txt", filepath.Join("archive", "new.txt")); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	f.assertNotExists("old.txt")
	if got := f.read(filepath.Join("archive", "new.txt")); got != "move me" {
		t.Fatalf("renamed content = %q", got)
	}

	// 重命名到根外、经逃逸链接、或改成可执行扩展名都必须被拒绝。
	evilDir := filepath.Join(f.root, "evil-dir")
	if err := os.Symlink(f.outside, evilDir); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	cases := []struct {
		name string
		old  string
		new  string
		want error
	}{
		{"traversal", "archive/new.txt", "../gone.txt", ErrInvalidPath},
		{"absolute", "archive/new.txt", "/tmp/gone.txt", ErrInvalidPath},
		{"symlink escape", "archive/new.txt", "evil-dir/gone.txt", ErrOutsideRoot},
		{"executable ext", "archive/new.txt", "archive/new.exe", ErrExecutableDenied},
		{"missing source", "missing.txt", "whatever.txt", fs.ErrNotExist},
	}
	for _, tc := range cases {
		if err := f.target.Rename(tc.old, tc.new); !errors.Is(err, tc.want) {
			t.Fatalf("%s: want %v, got %v", tc.name, tc.want, err)
		}
	}
	// 全部失败后原文件仍在、根外目录无变化。
	f.assertExists(filepath.Join("archive", "new.txt"))
	if got := snapshotDir(t, f.outside); len(got) != 1 {
		t.Fatalf("outside dir changed after failed renames: %v", got)
	}
}

func TestNewTargetValidation(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []Policy{
		{Root: "", MaxFileSize: 10},
		{Root: "relative/dir", MaxFileSize: 10},
		{Root: root, MaxFileSize: 0},
		{Root: root, MaxFileSize: -1},
		{Root: filepath.Join(base, "missing"), MaxFileSize: 10},
	}
	for _, policy := range cases {
		if _, err := NewTarget(policy); err == nil {
			t.Fatalf("policy %+v should be rejected", policy)
		}
	}

	// 根目录本身是逃逸符号链接时，构造即失败。
	link := filepath.Join(base, "root-link")
	if err := os.Symlink(filepath.Join(base, "elsewhere"), link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := NewTarget(Policy{Root: link, MaxFileSize: 10}); err == nil {
		t.Fatal("symlinked root pointing elsewhere must be rejected")
	}
}

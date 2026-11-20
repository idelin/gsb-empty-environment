package netsec

import (
	"errors"
	"reflect"
	"syscall"
	"testing"
)

func newMemSandbox(promises ...string) (*Sandbox, *MemFS) {
	m := NewMemFS()
	return New(m, promises...), m
}

// 提权重调：Pledge 只能收窄，试图恢复已丢弃的 promise 失败且原集合不变。
func TestPledgeNarrowsOnly(t *testing.T) {
	s, _ := newMemSandbox("stdio", "rpath", "wpath")

	if err := s.Pledge("stdio", "rpath"); err != nil {
		t.Fatalf("narrow pledge: %v", err)
	}
	if got := s.Promises(); !reflect.DeepEqual(got, []string{"rpath", "stdio"}) {
		t.Fatalf("promises after narrow = %v", got)
	}

	// Attempt to regain a dropped promise must fail.
	if err := s.Pledge("stdio", "wpath"); !errors.Is(err, ErrWiden) {
		t.Fatalf("widening pledge err = %v, want ErrWiden", err)
	}
	if got := s.Promises(); !reflect.DeepEqual(got, []string{"rpath", "stdio"}) {
		t.Fatalf("promises after failed widen = %v, want unchanged", got)
	}

	// Narrowing further still works.
	if err := s.Pledge("stdio"); err != nil {
		t.Fatalf("narrow to stdio: %v", err)
	}
	if got := s.Promises(); !reflect.DeepEqual(got, []string{"stdio"}) {
		t.Fatalf("promises = %v", got)
	}
}

// 锁定后收窄：Lock 之后 Pledge/Unveil 一律失败且状态不变。
func TestLockedSandboxRejectsPledgeAndUnveil(t *testing.T) {
	s, _ := newMemSandbox("stdio", "rpath")
	if err := s.Unveil("/data", "rw"); err != nil {
		t.Fatal(err)
	}
	s.Lock()

	if err := s.Pledge("stdio"); !errors.Is(err, ErrLocked) {
		t.Fatalf("pledge after lock err = %v, want ErrLocked", err)
	}
	if got := s.Promises(); !reflect.DeepEqual(got, []string{"rpath", "stdio"}) {
		t.Fatalf("promises changed after locked pledge: %v", got)
	}

	if err := s.Unveil("/other", "r"); !errors.Is(err, ErrLocked) {
		t.Fatalf("unveil after lock err = %v, want ErrLocked", err)
	}
	if err := s.Unveil("/data", "r"); !errors.Is(err, ErrLocked) {
		t.Fatalf("narrowing unveil after lock err = %v, want ErrLocked", err)
	}
	if p, _ := s.UnveilPerms("/data"); p != PermRead|PermWrite {
		t.Fatalf("unveil perms changed after lock: %v", p)
	}
}

// 权限扩大：同一路径重复登记只保留交集，绝不扩大。
func TestUnveilIntersectionOnly(t *testing.T) {
	s, _ := newMemSandbox()

	mustUnveil(t, s, "/data", "rwc")
	mustUnveil(t, s, "/data", "rw")
	if p, _ := s.UnveilPerms("/data"); p != PermRead|PermWrite {
		t.Fatalf("perms = %v, want rw", p)
	}

	// Attempt to widen with x and c must be ignored.
	mustUnveil(t, s, "/data", "rwxc")
	if p, _ := s.UnveilPerms("/data"); p != PermRead|PermWrite {
		t.Fatalf("perms widened to %v, want rw", p)
	}

	// Narrowing to a subset works.
	mustUnveil(t, s, "/data", "w")
	if p, _ := s.UnveilPerms("/data"); p != PermWrite {
		t.Fatalf("perms = %v, want w", p)
	}
}

// ENOENT 与 EACCES 区分：未暴露路径是 ENOENT，权限不足才是 EACCES。
func TestCheckDistinguishesNotExistAndPermDenied(t *testing.T) {
	s, m := newMemSandbox()
	m.AddFile("/data/file.txt")
	m.AddFile("/secret/passwd")
	mustUnveil(t, s, "/data", "r")

	if err := s.Check("/data/file.txt", PermRead); err != nil {
		t.Fatalf("read unveiled file: %v", err)
	}

	err := s.Check("/data/file.txt", PermWrite)
	if !errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.ENOENT) {
		t.Fatalf("write on r-only entry err = %v, want EACCES", err)
	}

	err = s.Check("/secret/passwd", PermRead)
	if !errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.EACCES) {
		t.Fatalf("unveiled path err = %v, want ENOENT", err)
	}
}

// ".." 不得越出已登记路径。
func TestDotDotCannotEscape(t *testing.T) {
	s, m := newMemSandbox()
	m.AddFile("/data/file.txt")
	m.AddFile("/secret/passwd")
	mustUnveil(t, s, "/data", "r")

	if err := s.Check("/data/./file.txt", PermRead); err != nil {
		t.Fatalf("dot segment: %v", err)
	}
	err := s.Check("/data/../secret/passwd", PermRead)
	if !errors.Is(err, syscall.ENOENT) {
		t.Fatalf("dot-dot escape err = %v, want ENOENT", err)
	}
	// ".." that stays inside the entry is fine.
	if err := s.Check("/data/sub/../file.txt", PermRead); err != nil {
		t.Fatalf("dot-dot inside entry: %v", err)
	}
}

// 跨登记符号链接：符号链接可指向另一已登记路径。
func TestSymlinkAcrossRegisteredEntries(t *testing.T) {
	s, m := newMemSandbox()
	m.AddFile("/b/file.txt")
	m.Symlink("/b/file.txt", "/a/link")
	mustUnveil(t, s, "/a", "r")
	mustUnveil(t, s, "/b", "r")

	if err := s.Check("/a/link", PermRead); err != nil {
		t.Fatalf("cross-entry symlink: %v", err)
	}
	// Permissions are taken from the resolved entry (/b is r-only).
	if err := s.Check("/a/link", PermWrite); !errors.Is(err, syscall.EACCES) {
		t.Fatalf("write via symlink err = %v, want EACCES", err)
	}
}

// 符号链接越出已登记集合即 ENOENT，包括相对目标与 ".."。
func TestSymlinkEscapeIsENOENT(t *testing.T) {
	s, m := newMemSandbox()
	m.AddFile("/etc/passwd")
	m.AddFile("/a/secret")
	m.Symlink("/etc/passwd", "/a/abs-link")
	m.Symlink("../../etc/passwd", "/a/rel-link")
	m.Symlink("secret", "/a/ok-link")
	mustUnveil(t, s, "/a", "r")

	if err := s.Check("/a/ok-link", PermRead); err != nil {
		t.Fatalf("relative symlink inside entry: %v", err)
	}
	for _, p := range []string{"/a/abs-link", "/a/rel-link"} {
		err := s.Check(p, PermRead)
		if !errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.EACCES) {
			t.Fatalf("symlink escape %s err = %v, want ENOENT", p, err)
		}
	}
}

// 嵌套符号链接：每一跳都必须在已登记集合内。
func TestNestedSymlinkChain(t *testing.T) {
	s, m := newMemSandbox()
	m.AddFile("/c/real")
	m.Symlink("/c/real", "/b/link2")
	m.Symlink("/b/link2", "/a/link1")
	mustUnveil(t, s, "/a", "r")
	mustUnveil(t, s, "/b", "r")
	mustUnveil(t, s, "/c", "r")

	if err := s.Check("/a/link1", PermRead); err != nil {
		t.Fatalf("nested symlink chain: %v", err)
	}

	// Remove /c from the set in a fresh sandbox: the chain breaks.
	s2, m2 := newMemSandbox()
	m2.AddFile("/c/real")
	m2.Symlink("/c/real", "/b/link2")
	m2.Symlink("/b/link2", "/a/link1")
	mustUnveil(t, s2, "/a", "r")
	mustUnveil(t, s2, "/b", "r")
	if err := s2.Check("/a/link1", PermRead); !errors.Is(err, syscall.ENOENT) {
		t.Fatalf("chain escaping to unregistered /c err = %v, want ENOENT", err)
	}
}

// 尾斜杠与大小写按平台规则归一。
func TestPathNormalization(t *testing.T) {
	s, m := newMemSandbox()
	m.AddFile("/data/dir/file.txt")
	mustUnveil(t, s, "/data/dir/", "r") // trailing slash

	if p, ok := s.UnveilPerms("/data/dir"); !ok || p != PermRead {
		t.Fatalf("trailing slash not normalized: %v %v", p, ok)
	}
	if err := s.Check("/data/dir/", PermRead); err != nil {
		t.Fatalf("trailing slash check: %v", err)
	}

	if caseInsensitive {
		if err := s.Check("/DATA/DIR/file.txt", PermRead); err != nil {
			t.Fatalf("case-insensitive check: %v", err)
		}
		if p, ok := s.UnveilPerms("/DATA/DIR"); !ok || p != PermRead {
			t.Fatalf("case-insensitive lookup: %v %v", p, ok)
		}
	} else {
		if err := s.Check("/DATA/DIR/file.txt", PermRead); !errors.Is(err, syscall.ENOENT) {
			t.Fatalf("case-sensitive check err = %v, want ENOENT", err)
		}
	}
}

// 锁定后访问判定仍然有效；'c' 权限覆盖尚未创建的路径。
func TestCheckAfterLockAndCreatePerm(t *testing.T) {
	s, m := newMemSandbox()
	m.AddFile("/data/existing.txt")
	mustUnveil(t, s, "/data", "rc")
	s.Lock()

	if err := s.Check("/data/existing.txt", PermRead); err != nil {
		t.Fatalf("read after lock: %v", err)
	}
	if err := s.Check("/data/newfile.txt", PermCreate); err != nil {
		t.Fatalf("create missing file under c entry: %v", err)
	}
	if err := s.Check("/data/newfile.txt", PermWrite); !errors.Is(err, syscall.EACCES) {
		t.Fatalf("write without w err = %v, want EACCES", err)
	}
}

// 更具体的登记项优先于祖先登记项。
func TestLongestMatchWins(t *testing.T) {
	s, m := newMemSandbox()
	m.AddFile("/data/pub/file.txt")
	mustUnveil(t, s, "/data", "r")
	mustUnveil(t, s, "/data/pub", "rw")

	if err := s.Check("/data/pub/file.txt", PermWrite); err != nil {
		t.Fatalf("longest match should grant w: %v", err)
	}
	if err := s.Check("/data/other", PermWrite); !errors.Is(err, syscall.EACCES) {
		t.Fatalf("ancestor entry w err = %v, want EACCES", err)
	}
}

func mustUnveil(t *testing.T, s *Sandbox, path, perms string) {
	t.Helper()
	if err := s.Unveil(path, perms); err != nil {
		t.Fatalf("unveil %s %s: %v", path, perms, err)
	}
}

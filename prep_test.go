package netsec

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeMounter 记录挂载调用，不真正执行挂载。
type fakeMounter struct {
	binds      [][2]string
	tmpfs      []string
	remounts   []string
	remountErr map[string]error
	major      uint32
	minor      uint32
	devErr     error
}

func (f *fakeMounter) ops() Ops {
	return Ops{
		Lstat: os.Lstat,
		BindMount: func(source, target string) error {
			f.binds = append(f.binds, [2]string{source, target})
			return nil
		},
		MountReadonlyTmpfs: func(target string) error {
			f.tmpfs = append(f.tmpfs, target)
			return nil
		},
		RemountReadonly: func(target string) error {
			f.remounts = append(f.remounts, target)
			if err, ok := f.remountErr[target]; ok {
				return err
			}
			return nil
		},
		DevNull: func() (uint32, uint32, error) {
			return f.major, f.minor, f.devErr
		},
	}
}

func newFake() *fakeMounter {
	return &fakeMounter{major: devNullMajor, minor: devNullMinor}
}

// 构造容器根文件系统树：
//
//	root/proc/kcore  普通文件
//	root/proc/sys    目录
//	root/etc/secret  普通文件（符号链接指向的“别处”）
func buildRootfs(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "proc", "sys"), 0o755))
	must(os.MkdirAll(filepath.Join(root, "etc"), 0o755))
	must(os.WriteFile(filepath.Join(root, "proc", "kcore"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(root, "etc", "secret"), []byte("s"), 0o644))
	return root
}

func TestMaskRejectsSymlinkTarget(t *testing.T) {
	root := buildRootfs(t)
	// 攻击者把遮蔽目标换成符号链接，指向树外敏感文件。
	target := filepath.Join(root, "proc", "kcore")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "etc", "secret"), target); err != nil {
		t.Fatal(err)
	}

	f := newFake()
	err := NewWithOps(f.ops()).MaskPaths([]string{target})
	if err == nil || !strings.Contains(err.Error(), "符号链接") {
		t.Fatalf("期望符号链接拒绝错误，得到 %v", err)
	}
	if len(f.binds) != 0 || len(f.tmpfs) != 0 {
		t.Fatalf("拒绝后不应发生任何挂载: binds=%v tmpfs=%v", f.binds, f.tmpfs)
	}
}

func TestMaskRejectsSymlinkParent(t *testing.T) {
	root := buildRootfs(t)
	// 攻击者把父级目录换成符号链接。
	proc := filepath.Join(root, "proc")
	if err := os.RemoveAll(proc); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "etc"), proc); err != nil {
		t.Fatal(err)
	}

	f := newFake()
	err := NewWithOps(f.ops()).MaskPaths([]string{filepath.Join(proc, "secret")})
	if err == nil || !strings.Contains(err.Error(), "符号链接") {
		t.Fatalf("期望父级符号链接拒绝错误，得到 %v", err)
	}
	if len(f.binds) != 0 || len(f.tmpfs) != 0 {
		t.Fatalf("拒绝后不应发生任何挂载: binds=%v tmpfs=%v", f.binds, f.tmpfs)
	}
}

func TestMaskSkipsMissingTarget(t *testing.T) {
	root := buildRootfs(t)
	f := newFake()
	missing := filepath.Join(root, "proc", "nonexistent")
	if err := NewWithOps(f.ops()).MaskPaths([]string{missing}); err != nil {
		t.Fatalf("不存在的目标应跳过，得到错误 %v", err)
	}
	if len(f.binds) != 0 || len(f.tmpfs) != 0 {
		t.Fatalf("跳过时不应发生挂载: binds=%v tmpfs=%v", f.binds, f.tmpfs)
	}
}

func TestMaskMissingTargetUnderSymlinkParentRejected(t *testing.T) {
	root := buildRootfs(t)
	// 目标不存在，但父级是符号链接：仍须拒绝。
	proc := filepath.Join(root, "proc")
	if err := os.RemoveAll(proc); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "etc"), proc); err != nil {
		t.Fatal(err)
	}

	f := newFake()
	err := NewWithOps(f.ops()).MaskPaths([]string{filepath.Join(proc, "nonexistent")})
	if err == nil || !strings.Contains(err.Error(), "符号链接") {
		t.Fatalf("期望父级符号链接拒绝错误，得到 %v", err)
	}
}

func TestMaskRejectsWrongDevNullDevice(t *testing.T) {
	root := buildRootfs(t)
	f := newFake()
	f.major, f.minor = 1, 5 // /dev/zero 的设备号，不是 /dev/null
	target := filepath.Join(root, "proc", "kcore")
	err := NewWithOps(f.ops()).MaskPaths([]string{target})
	if err == nil || !strings.Contains(err.Error(), "设备号") {
		t.Fatalf("期望设备号不符错误，得到 %v", err)
	}
	if len(f.binds) != 0 {
		t.Fatalf("设备号不符时不应绑定挂载: %v", f.binds)
	}
}

func TestMaskNormalPaths(t *testing.T) {
	root := buildRootfs(t)
	f := newFake()
	file := filepath.Join(root, "proc", "kcore")
	dir := filepath.Join(root, "proc", "sys")
	if err := NewWithOps(f.ops()).MaskPaths([]string{file, dir}); err != nil {
		t.Fatalf("正常路径遮蔽失败: %v", err)
	}
	if len(f.binds) != 1 || f.binds[0] != [2]string{"/dev/null", file} {
		t.Fatalf("文件目标应绑定 /dev/null: %v", f.binds)
	}
	if len(f.tmpfs) != 1 || f.tmpfs[0] != dir {
		t.Fatalf("目录目标应挂只读 tmpfs: %v", f.tmpfs)
	}
}

func TestReadonlyNormalAndMissing(t *testing.T) {
	root := buildRootfs(t)
	f := newFake()
	existing := filepath.Join(root, "proc", "kcore")
	missing := filepath.Join(root, "proc", "nonexistent")
	if err := NewWithOps(f.ops()).MakeReadonly([]string{existing, missing}); err != nil {
		t.Fatalf("重挂只读失败: %v", err)
	}
	if len(f.remounts) != 1 || f.remounts[0] != existing {
		t.Fatalf("只应重挂存在的路径: %v", f.remounts)
	}
}

func TestReadonlyRemountFailureIsFatal(t *testing.T) {
	root := buildRootfs(t)
	f := newFake()
	existing := filepath.Join(root, "proc", "kcore")
	f.remountErr = map[string]error{existing: errors.New("operation not permitted")}
	err := NewWithOps(f.ops()).MakeReadonly([]string{existing})
	if err == nil || !strings.Contains(err.Error(), "启动失败") {
		t.Fatalf("存在却不能重挂应视为启动失败，得到 %v", err)
	}
}

func TestPrepareRunsMaskedThenReadonly(t *testing.T) {
	root := buildRootfs(t)
	f := newFake()
	masked := []string{filepath.Join(root, "proc", "kcore")}
	readonly := []string{filepath.Join(root, "proc", "sys")}
	if err := NewWithOps(f.ops()).Prepare(masked, readonly); err != nil {
		t.Fatalf("Prepare 失败: %v", err)
	}
	if len(f.binds) != 1 {
		t.Fatalf("maskedPaths 未执行: %v", f.binds)
	}
	if len(f.remounts) != 1 {
		t.Fatalf("readonlyPaths 未执行: %v", f.remounts)
	}
}

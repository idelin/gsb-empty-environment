package netsec

import (
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readAll(t *testing.T, f *os.File) string {
	t.Helper()
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read %s: %v", f.Name(), err)
	}
	return string(b)
}

func digestOf(content string) [sha256.Size]byte {
	return sha256.Sum256([]byte(content))
}

// 嵌套脚本：脚本 -> 中间解释器（也是脚本） -> 最终二进制。
func TestResolveNestedChain(t *testing.T) {
	dir := t.TempDir()
	leaf := filepath.Join(dir, "leaf.bin")
	mid := filepath.Join(dir, "mid")
	script := filepath.Join(dir, "script")

	leafBody := "binary-interpreter"
	midBody := "#!" + leaf + "\nmid body\n"
	scriptBody := "#!" + mid + " -v\nscript body\n"
	writeFile(t, leaf, leafBody)
	writeFile(t, mid, midBody)
	writeFile(t, script, scriptBody)

	plan, err := Resolve(script)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	defer plan.Close()

	chain := plan.Chain()
	if len(chain) != 2 {
		t.Fatalf("chain length = %d, want 2", len(chain))
	}
	if chain[0].Path != mid || chain[0].Arg != "-v" || !chain[0].HasArg {
		t.Errorf("chain[0] = %+v, want path %s arg -v", chain[0], mid)
	}
	if chain[1].Path != leaf || chain[1].HasArg {
		t.Errorf("chain[1] = %+v, want path %s no arg", chain[1], leaf)
	}
	if chain[0].Digest != digestOf(midBody) {
		t.Errorf("chain[0] digest mismatch")
	}
	if chain[1].Digest != digestOf(leafBody) {
		t.Errorf("chain[1] digest mismatch")
	}
	if plan.ScriptDigest != digestOf(scriptBody) {
		t.Errorf("script digest mismatch")
	}
	if got := readAll(t, plan.Script()); got != scriptBody {
		t.Errorf("script content = %q, want %q", got, scriptBody)
	}
	if got := readAll(t, chain[0].File()); got != midBody {
		t.Errorf("mid content = %q, want %q", got, midBody)
	}
}

// 空参数：脚本头只有解释器路径。
func TestResolveEmptyArgument(t *testing.T) {
	dir := t.TempDir()
	leaf := filepath.Join(dir, "leaf.bin")
	script := filepath.Join(dir, "script")
	writeFile(t, leaf, "bin")
	writeFile(t, script, "#!"+leaf+"\nbody\n")

	plan, err := Resolve(script)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	defer plan.Close()

	chain := plan.Chain()
	if len(chain) != 1 {
		t.Fatalf("chain length = %d, want 1", len(chain))
	}
	if chain[0].HasArg || chain[0].Arg != "" {
		t.Errorf("HasArg = %v Arg = %q, want no argument", chain[0].HasArg, chain[0].Arg)
	}
}

// 换路径：解析后把路径下的文件掉包，计划仍须读取原对象。
func TestPathSwapKeepsPinnedObjects(t *testing.T) {
	dir := t.TempDir()
	leaf := filepath.Join(dir, "leaf.bin")
	script := filepath.Join(dir, "script")

	leafV1 := "leaf-v1"
	scriptV1 := "#!" + leaf + "\nscript-v1\n"
	writeFile(t, leaf, leafV1)
	writeFile(t, script, scriptV1)

	plan, err := Resolve(script)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	defer plan.Close()

	// 用 rename 原子掉包两个路径（新 inode）。
	leafV2 := "leaf-v2-evil"
	scriptV2 := "#!" + leaf + "\nscript-v2-evil\n"
	tmpLeaf := filepath.Join(dir, "leaf.tmp")
	tmpScript := filepath.Join(dir, "script.tmp")
	writeFile(t, tmpLeaf, leafV2)
	writeFile(t, tmpScript, scriptV2)
	if err := os.Rename(tmpLeaf, leaf); err != nil {
		t.Fatalf("swap leaf: %v", err)
	}
	if err := os.Rename(tmpScript, script); err != nil {
		t.Fatalf("swap script: %v", err)
	}

	//  sanity：按名读取已是新内容。
	if b, _ := os.ReadFile(script); string(b) != scriptV2 {
		t.Fatalf("path swap did not take effect")
	}

	// 计划保留的是原对象：内容与摘要都必须是 v1。
	if got := readAll(t, plan.Script()); got != scriptV1 {
		t.Errorf("pinned script = %q, want original %q", got, scriptV1)
	}
	if plan.ScriptDigest != digestOf(scriptV1) {
		t.Errorf("script digest changed after path swap")
	}
	chain := plan.Chain()
	if got := readAll(t, chain[0].File()); got != leafV1 {
		t.Errorf("pinned interpreter = %q, want original %q", got, leafV1)
	}
	if chain[0].Digest != digestOf(leafV1) {
		t.Errorf("interpreter digest changed after path swap")
	}
}

// 环：任意已固定对象再次出现即拒绝。
func TestCycleRejected(t *testing.T) {
	dir := t.TempDir()

	// 自引用。
	self := filepath.Join(dir, "self")
	writeFile(t, self, "#!"+self+"\n")
	if _, err := Resolve(self); !errors.Is(err, ErrCycle) {
		t.Errorf("self cycle: err = %v, want ErrCycle", err)
	}

	// 互相引用：a -> b -> a。
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	writeFile(t, a, "#!"+b+"\n")
	writeFile(t, b, "#!"+a+"\n")
	if _, err := Resolve(a); !errors.Is(err, ErrCycle) {
		t.Errorf("mutual cycle: err = %v, want ErrCycle", err)
	}

	// 解释器指回原脚本：s -> i -> s。
	s := filepath.Join(dir, "s")
	i := filepath.Join(dir, "i")
	writeFile(t, s, "#!"+i+"\n")
	writeFile(t, i, "#!"+s+"\n")
	if _, err := Resolve(s); !errors.Is(err, ErrCycle) {
		t.Errorf("script cycle: err = %v, want ErrCycle", err)
	}
}

// 最大深度可配置。
func TestMaxDepth(t *testing.T) {
	dir := t.TempDir()
	leaf := filepath.Join(dir, "leaf.bin")
	i1 := filepath.Join(dir, "i1")
	i2 := filepath.Join(dir, "i2")
	script := filepath.Join(dir, "script")
	writeFile(t, leaf, "bin")
	writeFile(t, i2, "#!"+leaf+"\n")
	writeFile(t, i1, "#!"+i2+"\n")
	writeFile(t, script, "#!"+i1+"\n")

	if _, err := Resolve(script, WithMaxDepth(2)); !errors.Is(err, ErrDepthLimit) {
		t.Errorf("depth 2: err = %v, want ErrDepthLimit", err)
	}
	plan, err := Resolve(script, WithMaxDepth(3))
	if err != nil {
		t.Fatalf("depth 3: %v", err)
	}
	defer plan.Close()
	if len(plan.Chain()) != 3 {
		t.Errorf("chain length = %d, want 3", len(plan.Chain()))
	}
	if _, err := Resolve(script, WithMaxDepth(0)); !errors.Is(err, ErrDepthLimit) {
		t.Errorf("depth 0: err = %v, want ErrDepthLimit", err)
	}
}

// 许可策略必须覆盖整条链：每一环都被回调，任一拒绝即失败。
func TestPolicyCoversWholeChain(t *testing.T) {
	dir := t.TempDir()
	leaf := filepath.Join(dir, "leaf.bin")
	mid := filepath.Join(dir, "mid")
	script := filepath.Join(dir, "script")
	writeFile(t, leaf, "bin")
	writeFile(t, mid, "#!"+leaf+" -x\n")
	writeFile(t, script, "#!"+mid+"\n")

	var visited []string
	allow := func(l Link) error {
		visited = append(visited, l.Path)
		return nil
	}
	plan, err := Resolve(script, WithPolicy(allow))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	plan.Close()
	if want := []string{mid, leaf}; !reflect.DeepEqual(visited, want) {
		t.Errorf("visited = %v, want %v", visited, want)
	}

	// 拒绝第二环：第一环已访问，整体返回 ErrDenied。
	visited = nil
	deny := func(l Link) error {
		visited = append(visited, l.Path)
		if l.Path == leaf {
			return errors.New("not allowed")
		}
		return nil
	}
	if _, err := Resolve(script, WithPolicy(deny)); !errors.Is(err, ErrDenied) {
		t.Fatalf("deny: err = %v, want ErrDenied", err)
	}
	if want := []string{mid, leaf}; !reflect.DeepEqual(visited, want) {
		t.Errorf("visited = %v, want %v", visited, want)
	}
}

// 脚本头限定：相对路径、多个参数、缺失解释器、超长指令均拒绝。
func TestBadHeaders(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		header  string
		wantErr error
	}{
		{"relative path", "#!usr/bin/env\n", ErrNotAbsolute},
		{"two arguments", "#!/bin/sh -x -e\n", ErrBadShebang},
		{"missing interpreter", "#!\n", ErrBadShebang},
		{"too long", "#!/" + strings.Repeat("a", MaxShebangBytes) + "\n", ErrBadShebang},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := filepath.Join(dir, "s")
			writeFile(t, script, tc.header)
			if _, err := Resolve(script); !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// 无 "#!" 的脚本：空链，直接执行。
func TestPlainScriptNoChain(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "plain")
	writeFile(t, script, "echo hi\n")
	plan, err := Resolve(script)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	defer plan.Close()
	if len(plan.Chain()) != 0 {
		t.Errorf("chain length = %d, want 0", len(plan.Chain()))
	}
}

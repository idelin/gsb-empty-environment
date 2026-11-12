package nameindex

import (
	"errors"
	"strings"
	"testing"
)

// 测试用预组合与分解形式的 Unicode 字符：
const (
	preE  = "é"  // é（预组合）
	preEg = "è"  // è（预组合）
	deE   = "é" // e + 组合尖音符
	deEg  = "è" // e + 组合沉音符
)

// testConfig 模拟一个大小写不敏感、含一对多折叠、忽略尾部
// '.' 与 ' ' 的目标文件系统（近似 Windows + 德语/法语折叠）。
func testConfig() Config {
	fold := FoldTable{
		'ß':      "ss", // 一对二折叠
		'ẞ':      "ss", // 大写 ß 同样折叠
		'é':      deE,  // 预组合 → 组合字符序列
		'É':      "É",
		'è':      deEg,
		'\u212a': "k", // 开尔文符号 U+212A
	}
	for r := 'A'; r <= 'Z'; r++ {
		fold[r] = string(r - 'A' + 'a')
	}
	return Config{
		Fold:           fold,
		IgnoreTrailing: map[rune]bool{'.': true, ' ': true},
	}
}

func mustAdd(t *testing.T, ix *Index, path string, isFile bool) {
	t.Helper()
	if err := ix.Add(path, isFile); err != nil {
		t.Fatalf("Add(%q, %v) unexpected error: %v", path, isFile, err)
	}
}

func mustConflict(t *testing.T, ix *Index, path string, isFile bool, kind ConflictKind) *ConflictError {
	t.Helper()
	err := ix.Add(path, isFile)
	if err == nil {
		t.Fatalf("Add(%q, %v) succeeded, want %v", path, isFile, kind)
	}
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("Add(%q, %v) error = %T %v, want *ConflictError", path, isFile, err, err)
	}
	if ce.Kind != kind {
		t.Fatalf("Add(%q, %v) kind = %v, want %v", path, isFile, ce.Kind, kind)
	}
	return ce
}

// 非一对一折叠：'ß' 折叠为 "ss"，"straße" 与 "strasse" 身份相同。
func TestNonOneToOneFold(t *testing.T) {
	ix := New(testConfig())
	mustAdd(t, ix, "straße", true)

	ce := mustConflict(t, ix, "strasse", true, ConflictDuplicate)
	if ce.ExistingPath != "straße" || ce.ExistingSegment != "straße" {
		t.Fatalf("evidence lost: %+v", ce)
	}
	if ce.Path != "strasse" || ce.Segment != "strasse" {
		t.Fatalf("rejected path not preserved: %+v", ce)
	}

	// 开尔文符号 K (U+212A) 折叠为 "k"。
	mustAdd(t, ix, "1\u212a", true)
	mustConflict(t, ix, "1k", true, ConflictDuplicate)

	// 原登记仍可解析，且解析结果保留首次登记的原名。
	orig, isFile, ok := ix.Lookup("STRASSE")
	if !ok || !isFile || orig != "straße" {
		t.Fatalf("Lookup(STRASSE) = %q, %v, %v", orig, isFile, ok)
	}
}

// 组合字符：预组合 'é' 折叠为 "e" + 组合尖音符，NFC 与 NFD
// 形式的路径身份相同，不得静默覆盖。
func TestCombiningCharacters(t *testing.T) {
	ix := New(testConfig())
	mustAdd(t, ix, "caf"+preE, true)

	ce := mustConflict(t, ix, "caf"+deE, true, ConflictDuplicate)
	if ce.ExistingPath != "caf"+preE {
		t.Fatalf("evidence lost: %+v", ce)
	}

	// 目录层级同样适用。
	mustAdd(t, ix, preE+"l"+preEg+"ve/notes.txt", true)
	mustConflict(t, ix, deE+"l"+deEg+"ve/notes.txt", true, ConflictDuplicate)
}

// 尾部忽略字符：'.' 与 ' ' 在段尾被目标文件系统忽略。
func TestTrailingIgnoredChars(t *testing.T) {
	ix := New(testConfig())
	mustAdd(t, ix, "readme.txt", true)
	mustConflict(t, ix, "readme.txt.", true, ConflictDuplicate)
	mustConflict(t, ix, "readme.txt ", true, ConflictDuplicate)
	mustConflict(t, ix, "README.TXT ..", true, ConflictDuplicate)

	// 整段身份被剥空的路径非法。
	if err := ix.Add("...", true); err == nil {
		t.Fatal("Add(\"...\") succeeded, want empty-identity error")
	}
}

// 文件/目录冲突：两个方向都必须拒绝。
func TestFileDirConflict(t *testing.T) {
	ix := New(testConfig())
	mustAdd(t, ix, "asset.bin", true)
	mustAdd(t, ix, "docs/guide.md", true)

	// 已存在的文件被当作目录使用。
	ce := mustConflict(t, ix, "asset.bin/nested", true, ConflictFileDir)
	if ce.ExistingPath != "asset.bin" || ce.Segment != "asset.bin" {
		t.Fatalf("evidence lost: %+v", ce)
	}

	// 已存在的目录被登记为文件。
	ce = mustConflict(t, ix, "DOCS", true, ConflictFileDir)
	if ce.ExistingSegment != "docs" || ce.Segment != "DOCS" {
		t.Fatalf("evidence lost: %+v", ce)
	}

	// 同一身份先文件后目录同样冲突。
	mustAdd(t, ix, "tools", false)
	mustConflict(t, ix, "TOOLS", true, ConflictFileDir)
	mustConflict(t, ix, "tools", false, ConflictDuplicate)
}

// 目录层级交换：相同名字出现在不同层级时身份按层独立计算；
// 交换层级后撞到已有身份的仍被拒绝，未撞到的允许共存。
func TestDirectoryLevelSwap(t *testing.T) {
	ix := New(testConfig())
	mustAdd(t, ix, "Alpha/beta.txt", true)

	// 交换层级：把子级名字提到根级、根级名字降为文件，
	// 两者身份不冲突，允许共存。
	mustAdd(t, ix, "beta.txt/alpha", true)

	// 但折叠后撞到已有目录身份的交换必须拒绝。
	ce := mustConflict(t, ix, "alpha", true, ConflictFileDir)
	if ce.ExistingPath != "Alpha/beta.txt" || ce.ExistingSegment != "Alpha" {
		t.Fatalf("evidence lost: %+v", ce)
	}
	mustConflict(t, ix, "ALPHA/BETA.TXT", true, ConflictDuplicate)

	// 共存的两条路径都能按原名解析。
	if orig, _, ok := ix.Lookup("alpha/beta.txt"); !ok || orig != "Alpha/beta.txt" {
		t.Fatalf("Lookup(alpha/beta.txt) = %q, %v", orig, ok)
	}
	if orig, _, ok := ix.Lookup("BETA.TXT/ALPHA"); !ok || orig != "beta.txt/alpha" {
		t.Fatalf("Lookup(BETA.TXT/ALPHA) = %q, %v", orig, ok)
	}
}

// 被拒绝的登记不得改动索引（无静默覆盖、无部分写入）。
func TestRejectedAddLeavesIndexUntouched(t *testing.T) {
	ix := New(testConfig())
	mustAdd(t, ix, "keep", true)

	bad := []string{
		"KEEP",             // 折叠后重复
		"keep/sub",         // 文件当目录
		"newdir/../escape", // 非法段
		"newdir/",          // 尾空段
		"",                 // 空路径
	}
	for _, p := range bad {
		if err := ix.Add(p, true); err == nil {
			t.Fatalf("Add(%q) succeeded, want error", p)
		}
	}

	if _, _, ok := ix.Lookup("newdir"); ok {
		t.Fatal("rejected Add partially created \"newdir\"")
	}
	orig, isFile, ok := ix.Lookup("KEEP")
	if !ok || !isFile || orig != "keep" {
		t.Fatalf("existing entry damaged: %q, %v, %v", orig, isFile, ok)
	}
}

// 错误证据：Error() 输出同时包含双方原始名称。
func TestConflictErrorEvidence(t *testing.T) {
	ix := New(testConfig())
	mustAdd(t, ix, "Maßband", true)
	err := ix.Add("massband", true)
	if err == nil {
		t.Fatal("Add(massband) succeeded, want conflict")
	}
	msg := err.Error()
	for _, want := range []string{"Maßband", "massband", "duplicate path"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing evidence %q", msg, want)
		}
	}
}

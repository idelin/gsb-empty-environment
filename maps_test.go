package netsec

import (
	"strings"
	"testing"
)

func mustParse(t *testing.T, text string) []Mapping {
	t.Helper()
	maps, err := ParseMaps(strings.NewReader(text))
	if err != nil {
		t.Fatalf("ParseMaps: %v", err)
	}
	return maps
}

func TestParseMapsMemfdNameWithSpacesAndDeleted(t *testing.T) {
	text := "7f0000000000-7f0000002000 rw-s 00000000 00:01 12345 /memfd:jit code cache (deleted)\n" +
		"00400000-00452000 r-xp 00000000 08:02 173521 /usr/bin/dbus-daemon\n" +
		"7f0000100000-7f0000200000 rw-p 00000000 00:00 0 \n"
	maps := mustParse(t, text)
	if len(maps) != 3 {
		t.Fatalf("期望 3 条映射，得到 %d", len(maps))
	}
	m := maps[0]
	if m.Path != "/memfd:jit code cache (deleted)" {
		t.Errorf("memfd 路径解析错误: %q", m.Path)
	}
	if !m.Shared() || !m.Writable() || m.Executable() {
		t.Errorf("权限解析错误: %q", m.Perms)
	}
	if m.DevMajor != 0 || m.DevMinor != 1 || m.Inode != 12345 {
		t.Errorf("设备/inode 解析错误: %d:%d inode=%d", m.DevMajor, m.DevMinor, m.Inode)
	}
	if maps[2].Path != "" || maps[2].Inode != 0 {
		t.Errorf("匿名映射解析错误: %+v", maps[2])
	}
}

func TestPartialOverlapReported(t *testing.T) {
	// 可写段覆盖文件偏移 [0, 0x2000)，可执行段覆盖 [0x1000, 0x3000)。
	text := "7f0000000000-7f0000002000 rw-s 00000000 00:01 12345 /memfd:jit code (deleted)\n" +
		"7f0000001000-7f0000003000 r-xs 00001000 00:01 12345 /memfd:jit code (deleted)\n"
	aliases, err := FindAliases(strings.NewReader(text))
	if err != nil {
		t.Fatalf("FindAliases: %v", err)
	}
	if len(aliases) != 1 {
		t.Fatalf("期望 1 个别名，得到 %d", len(aliases))
	}
	a := aliases[0]
	if a.OverlapStart != 0x1000 || a.OverlapEnd != 0x2000 {
		t.Errorf("重叠区间错误: [%#x, %#x)", a.OverlapStart, a.OverlapEnd)
	}
	if a.Path != "/memfd:jit code (deleted)" {
		t.Errorf("别名路径错误: %q", a.Path)
	}
	if !a.Writable.Writable() || !a.Executable.Executable() {
		t.Errorf("别名方向错误: W=%q X=%q", a.Writable.Perms, a.Executable.Perms)
	}
	if a.Inode != 12345 || a.Device != "00:01" {
		t.Errorf("后备对象标识错误: %s inode=%d", a.Device, a.Inode)
	}
}

func TestAdjacentNonOverlappingNotReported(t *testing.T) {
	// 两段在文件偏移上首尾相接但不重叠：[0,0x1000) 与 [0x1000,0x2000)。
	text := "7f0000000000-7f0000001000 rw-s 00000000 00:01 12345 /memfd:jit code (deleted)\n" +
		"7f0000001000-7f0000002000 r-xs 00001000 00:01 12345 /memfd:jit code (deleted)\n"
	aliases, err := FindAliases(strings.NewReader(text))
	if err != nil {
		t.Fatalf("FindAliases: %v", err)
	}
	if len(aliases) != 0 {
		t.Fatalf("相邻不重叠不应报告，得到 %d 个别名: %+v", len(aliases), aliases)
	}
}

func TestPrivateMappingsNotReported(t *testing.T) {
	// 私有写时复制映射即使重叠也不算别名；混合 s/p 同样不算。
	text := "7f0000000000-7f0000002000 rw-p 00000000 08:02 99999 /usr/lib/libjit.so\n" +
		"7f0000001000-7f0000003000 r-xp 00001000 08:02 99999 /usr/lib/libjit.so\n" +
		"7f0000100000-7f0000102000 rw-s 00000000 08:02 88888 /usr/lib/libother.so\n" +
		"7f0000101000-7f0000103000 r-xp 00001000 08:02 88888 /usr/lib/libother.so\n"
	aliases, err := FindAliases(strings.NewReader(text))
	if err != nil {
		t.Fatalf("FindAliases: %v", err)
	}
	if len(aliases) != 0 {
		t.Fatalf("私有映射不应报告，得到 %d 个别名: %+v", len(aliases), aliases)
	}
}

func TestMultiSegmentMemfd(t *testing.T) {
	// 同一 memfd 的三个共享段：只有 [0x500,0x1000) 构成可写/可执行重叠。
	text := "7f0000000000-7f0000001000 rw-s 00000000 00:01 777 /memfd:jit code (deleted)\n" +
		"7f0000002000-7f0000003000 r-xs 00002000 00:01 777 /memfd:jit code (deleted)\n" +
		"7f0000004000-7f0000005000 r-xs 00000500 00:01 777 /memfd:jit code (deleted)\n"
	aliases, err := FindAliases(strings.NewReader(text))
	if err != nil {
		t.Fatalf("FindAliases: %v", err)
	}
	if len(aliases) != 1 {
		t.Fatalf("期望 1 个别名，得到 %d: %+v", len(aliases), aliases)
	}
	a := aliases[0]
	if a.OverlapStart != 0x500 || a.OverlapEnd != 0x1000 {
		t.Errorf("重叠区间错误: [%#x, %#x)", a.OverlapStart, a.OverlapEnd)
	}
}

func TestDifferentBackingObjectsNotReported(t *testing.T) {
	// inode 不同或匿名映射（inode 0）都不构成别名。
	text := "7f0000000000-7f0000002000 rw-s 00000000 00:01 111 /memfd:a (deleted)\n" +
		"7f0000001000-7f0000003000 r-xs 00001000 00:01 222 /memfd:b (deleted)\n" +
		"7f0000010000-7f0000012000 rw-s 00000000 00:00 0\n" +
		"7f0000011000-7f0000013000 r-xs 00001000 00:00 0\n"
	aliases, err := FindAliases(strings.NewReader(text))
	if err != nil {
		t.Fatalf("FindAliases: %v", err)
	}
	if len(aliases) != 0 {
		t.Fatalf("不同后备对象不应报告，得到 %d 个别名: %+v", len(aliases), aliases)
	}
}

func TestRwxSharedPairReportedOnce(t *testing.T) {
	// 两个 rwx 共享映射互相重叠只报告一次。
	text := "7f0000000000-7f0000002000 rwxs 00000000 00:01 555 /memfd:jit (deleted)\n" +
		"7f0000001000-7f0000003000 rwxs 00001000 00:01 555 /memfd:jit (deleted)\n"
	aliases, err := FindAliases(strings.NewReader(text))
	if err != nil {
		t.Fatalf("FindAliases: %v", err)
	}
	if len(aliases) != 1 {
		t.Fatalf("期望 1 个别名，得到 %d: %+v", len(aliases), aliases)
	}
	if aliases[0].OverlapStart != 0x1000 || aliases[0].OverlapEnd != 0x2000 {
		t.Errorf("重叠区间错误: [%#x, %#x)", aliases[0].OverlapStart, aliases[0].OverlapEnd)
	}
}

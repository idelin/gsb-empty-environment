package shadow

import "testing"

func mustMalloc(t *testing.T, m *Mem, addr, size uint64) {
	t.Helper()
	if err := m.Malloc(addr, size); err != nil {
		t.Fatalf("Malloc(0x%x, %d): %v", addr, size, err)
	}
}

func mustFree(t *testing.T, m *Mem, addr uint64) {
	t.Helper()
	if err := m.Free(addr); err != nil {
		t.Fatalf("Free(0x%x): %v", addr, err)
	}
}

func wantOK(t *testing.T, m *Mem, addr uint64, size int) {
	t.Helper()
	if err := m.Check(addr, size, false); err != nil {
		t.Errorf("Check(0x%x, %d) = %v, want ok", addr, size, err)
	}
}

func wantErr(t *testing.T, m *Mem, addr uint64, size int, write bool, kind Kind, badAddr uint64) {
	t.Helper()
	err := m.Check(addr, size, write)
	if err == nil {
		t.Fatalf("Check(0x%x, %d) = nil, want %v", addr, size, kind)
	}
	if err.Kind != kind {
		t.Errorf("Check(0x%x, %d).Kind = %v, want %v", addr, size, err.Kind, kind)
	}
	if err.Addr != badAddr {
		t.Errorf("Check(0x%x, %d).Addr = 0x%x, want 0x%x", addr, size, err.Addr, badAddr)
	}
	if err.Write != write {
		t.Errorf("Check(0x%x, %d).Write = %v, want %v", addr, size, err.Write, write)
	}
}

// 块尾部分字节：13 字节的块，最后一槽仅前 5 字节可访问。
func TestPartialTail(t *testing.T) {
	m := New(0)
	mustMalloc(t, m, 0x1000, 13)

	// 各种宽度的合法访问。
	wantOK(t, m, 0x1000, 1)
	wantOK(t, m, 0x1000, 8)
	wantOK(t, m, 0x1006, 2)
	wantOK(t, m, 0x1008, 4) // 尾槽前 4 字节
	wantOK(t, m, 0x100C, 1) // 最后一个可访问字节

	// 越过块尾一个字节即报错。
	wantErr(t, m, 0x100D, 1, true, KindRightRedzone, 0x100D)
	// 从合法字节开始但跨入尾槽不可访问部分。
	wantErr(t, m, 0x100C, 2, false, KindRightRedzone, 0x100D)
	wantErr(t, m, 0x100C, 4, false, KindRightRedzone, 0x100D)
	wantErr(t, m, 0x1008, 8, false, KindRightRedzone, 0x100D)
	// 16 字节访问从块首开始，越过块尾。
	wantErr(t, m, 0x1000, 16, false, KindRightRedzone, 0x100D)
	// 块后的完整右红区槽。
	wantErr(t, m, 0x1010, 8, false, KindRightRedzone, 0x1010)
	// 左红区。
	wantErr(t, m, 0x0FF8, 8, false, KindLeftRedzone, 0x0FF8)
	wantErr(t, m, 0x0FFF, 1, true, KindLeftRedzone, 0x0FFF)
	wantErr(t, m, 0x0FFC, 8, false, KindLeftRedzone, 0x0FFC)
}

// 跨段访问：一次访问从用户区跨入红区或相邻块的左红区。
func TestCrossSegment(t *testing.T) {
	m := New(0)
	mustMalloc(t, m, 0x2000, 16)
	mustMalloc(t, m, 0x2020, 16)

	// 恰好覆盖第一个块的 16 字节访问合法。
	wantOK(t, m, 0x2000, 16)
	// 从块内开始，跨入右红区。
	wantErr(t, m, 0x2008, 16, false, KindRightRedzone, 0x2010)
	wantErr(t, m, 0x200F, 2, true, KindRightRedzone, 0x2010)
	// 两相邻块之间的右红区与左红区各自独立。
	wantErr(t, m, 0x2010, 8, false, KindRightRedzone, 0x2010)
	wantErr(t, m, 0x2018, 8, false, KindLeftRedzone, 0x2018)
	// 从右红区跨入左红区的 16 字节访问，报首个非法字节。
	wantErr(t, m, 0x2010, 16, false, KindRightRedzone, 0x2010)
	// 第二个块本身可访问。
	wantOK(t, m, 0x2020, 16)
}

// 释放后使用：读写都会被发现。
func TestUseAfterFree(t *testing.T) {
	m := New(1 << 20)
	mustMalloc(t, m, 0x3000, 32)
	wantOK(t, m, 0x3000, 8)
	wantOK(t, m, 0x3018, 8)

	mustFree(t, m, 0x3000)

	for _, size := range []int{1, 2, 4, 8, 16} {
		wantErr(t, m, 0x3000, size, false, KindUseAfterFree, 0x3000)
		wantErr(t, m, 0x3000, size, true, KindUseAfterFree, 0x3000)
	}
	// 块中部与末尾同样被毒化。
	wantErr(t, m, 0x3010, 4, false, KindUseAfterFree, 0x3010)
	wantErr(t, m, 0x301F, 1, true, KindUseAfterFree, 0x301F)

	// 重复释放与释放未登记地址都是错误。
	if err := m.Free(0x3000); err == nil {
		t.Error("double Free(0x3000) = nil, want error")
	}
	if err := m.Free(0x5000); err == nil {
		t.Error("Free(0x5000) untracked = nil, want error")
	}
}

// 隔离区回收：总字节超过上限时才从最旧块开始回收，回收后可重新分配。
func TestQuarantineRecycle(t *testing.T) {
	m := New(32)
	mustMalloc(t, m, 0x4000, 16)
	mustMalloc(t, m, 0x4100, 16)
	mustMalloc(t, m, 0x4200, 16)

	mustFree(t, m, 0x4000) // 16 <= 32，不回收
	if got := m.QuarantineLen(); got != 1 {
		t.Fatalf("QuarantineLen = %d, want 1", got)
	}
	mustFree(t, m, 0x4100) // 32 <= 32，仍不回收
	if got := m.QuarantineBytes(); got != 32 {
		t.Fatalf("QuarantineBytes = %d, want 32", got)
	}
	wantErr(t, m, 0x4000, 8, false, KindUseAfterFree, 0x4000)

	mustFree(t, m, 0x4200) // 48 > 32，回收最旧的 0x4000
	if got := m.QuarantineLen(); got != 2 {
		t.Fatalf("QuarantineLen = %d, want 2", got)
	}
	if got := m.QuarantineBytes(); got != 32 {
		t.Fatalf("QuarantineBytes = %d, want 32", got)
	}

	// 最旧块已被回收：影子清除，不再报 use-after-free，可重新分配。
	wantOK(t, m, 0x4000, 8)
	mustMalloc(t, m, 0x4000, 16)
	wantOK(t, m, 0x4000, 16)

	// 较新的块仍在隔离区中。
	wantErr(t, m, 0x4100, 8, false, KindUseAfterFree, 0x4100)
	wantErr(t, m, 0x4200, 8, true, KindUseAfterFree, 0x4200)
}

// 隔离区上限为 0 时，释放即回收。
func TestQuarantineZeroLimit(t *testing.T) {
	m := New(0)
	mustMalloc(t, m, 0x6000, 8)
	mustFree(t, m, 0x6000)
	if got := m.QuarantineLen(); got != 0 {
		t.Fatalf("QuarantineLen = %d, want 0", got)
	}
	wantOK(t, m, 0x6000, 8)
}

// 所有支持的访问宽度在合法块内均放行。
func TestAccessSizes(t *testing.T) {
	m := New(0)
	mustMalloc(t, m, 0x7000, 32)
	for _, size := range []int{1, 2, 4, 8, 16} {
		for addr := uint64(0x7000); addr+uint64(size) <= 0x7020; addr += uint64(size) {
			wantOK(t, m, addr, size)
		}
	}
}

func TestErrorString(t *testing.T) {
	m := New(1 << 20)
	mustMalloc(t, m, 0x8000, 8)
	mustFree(t, m, 0x8000)
	err := m.Check(0x8000, 4, true)
	if err == nil {
		t.Fatal("Check = nil, want use-after-free")
	}
	want := "shadow: use-after-free: write of 4 byte(s) at 0x8000"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}

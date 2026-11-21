package admission

import "testing"

func TestIntervalMapOverlayAndClone(t *testing.T) {
	m := newIntervalMap(0)
	if got := m.get(100); got != 0 {
		t.Fatalf("base value=%d want 0", got)
	}
	m.overlay(Range{10, 20}, 1)
	m.overlay(Range{30, 40}, 2)

	cases := []struct {
		page uint64
		want int
	}{
		{0, 0}, {9, 0}, {10, 1}, {20, 1}, {21, 0},
		{29, 0}, {30, 2}, {40, 2}, {41, 0}, {MaxPage, 0},
	}
	for _, c := range cases {
		if got := m.get(c.page); got != c.want {
			t.Fatalf("page %d value=%d want %d", c.page, got, c.want)
		}
	}

	// 相邻等值区间必须被 compact 合并，边界保持最小。
	m.overlay(Range{21, 29}, 0)
	if len(m.bounds) != 5 {
		t.Fatalf("bounds after compact=%v want 5 segments", m.bounds)
	}

	// clone 必须独立：修改副本不得影响原映射。
	clone := m.clone()
	clone.overlay(Range{10, 40}, 9)
	if m.get(15) != 1 {
		t.Fatal("clone mutation leaked into original map")
	}
	if clone.get(15) != 9 {
		t.Fatal("clone overlay did not take effect")
	}
}

func TestIntervalMapSplitInheritsValue(t *testing.T) {
	m := newIntervalMap(pageState{perm: PermExec, digest: "d"})
	idx := m.split(50)
	if m.bounds[idx] != 50 {
		t.Fatalf("split index bound=%d want 50", m.bounds[idx])
	}
	// 新切出的段必须继承原段完整状态，而不是零值。
	if got := m.get(50); got.digest != "d" || got.perm != PermExec {
		t.Fatalf("split lost inherited state: %+v", got)
	}
	if got := m.get(49); got.digest != "d" {
		t.Fatalf("preceding segment changed: %+v", got)
	}
}

func TestRevokeExecDoesNotSpread(t *testing.T) {
	// 单段全空间映射上撤销子区间，邻页权限必须原样保留。
	m := newIntervalMap(pageState{perm: PermExec})
	revokeExec(m, Range{1, 40})
	for p := uint64(0); p <= 42; p++ {
		got := m.get(p).perm
		want := PermExec
		if p >= 1 && p <= 40 {
			want = PermNone
		}
		if got != want {
			t.Fatalf("page %d perm=%d want %d", p, got, want)
		}
	}
	// 直达 MaxPage 的撤销不得发生溢出。
	revokeExec(m, Range{MaxPage - 2, MaxPage})
	if m.get(MaxPage).perm != PermNone {
		t.Fatal("tail revocation failed")
	}
	if m.get(MaxPage-3).perm != PermExec {
		t.Fatal("revocation spread beyond its range at tail")
	}
}

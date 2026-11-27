package mvcc

import "testing"

func TestXidPrecedesSpecialConstants(t *testing.T) {
	// 三个特殊常量固定线性排在所有普通事务号之前，不参与环形运算。
	specials := []XID{InvalidXid, BootstrapXid, FrozenXid}
	for i, a := range specials {
		for j, b := range specials {
			got := XidPrecedes(a, b)
			want := i < j
			if got != want {
				t.Fatalf("XidPrecedes(%d, %d) = %v, want %v", a, b, got, want)
			}
		}
		if XidFollows(FirstNormalXid, a) != true {
			t.Fatalf("normal XID must follow special XID %d", a)
		}
		if XidPrecedes(MaxXid, a) != false {
			t.Fatalf("normal XID %d must not precede special XID %d", MaxXid, a)
		}
	}

	if XidPrecedes(FrozenXid, FrozenXid) {
		t.Fatal("FrozenXid must not precede itself")
	}
}

func TestXidPrecedesWraparound(t *testing.T) {
	// 跨过 0xFFFFFFFF -> 3 的回绕边界：老事务是接近 2^32-1 的编号，
	// 新事务回绕后从 3 重新开始。
	cases := []struct {
		a, b XID
		want bool
	}{
		{MaxXid, FirstNormalXid, true},
		{MaxXid, 10, true},
		// 半圆中点（差 2^31）按有符号差值为非负，判定为“不早于”。
		{MaxXid, 1 << 31, false},
		{FirstNormalXid, MaxXid, false},
		{10, MaxXid, false},
		{10, 11, true},
		{11, 10, false},
		{MaxXid, MaxXid, false},
		{1 << 31, (1 << 31) - 1, false},
		{(1 << 31) - 1, 1 << 31, true},
		{0xFFFFFF00, 0x00000100, true},
		{0x00000100, 0xFFFFFF00, false},
	}
	for _, tc := range cases {
		if got := XidPrecedes(tc.a, tc.b); got != tc.want {
			t.Errorf("XidPrecedes(0x%08X, 0x%08X) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestXidPrecedesOrEqual(t *testing.T) {
	if !XidPrecedesOrEqual(MaxXid, FirstNormalXid) {
		t.Fatal("MaxXid should precede wraparound XID")
	}
	if !XidPrecedesOrEqual(42, 42) {
		t.Fatal("equal XIDs should satisfy precedes-or-equal")
	}
	if XidPrecedesOrEqual(42, 41) {
		t.Fatal("newer XID must not precede older")
	}
}

func TestClogDefaultsToInProgress(t *testing.T) {
	clog := NewClog()
	if clog.StatusOf(100) != StatusInProgress {
		t.Fatal("unknown XID must be treated as in-progress")
	}
	clog.Set(100, StatusCommitted)
	clog.Set(101, StatusAborted)
	if clog.StatusOf(100) != StatusCommitted || clog.StatusOf(101) != StatusAborted {
		t.Fatal("Clog did not persist recorded statuses")
	}
}

package dma

import (
	"errors"
	"testing"
)

const testRingBase uint64 = 0x10000

// buildRing 把描述符数组写入客户机环并返回 guest。
func buildRing(t *testing.T, descs []Descriptor) (*fakeGuest, uint64) {
	t.Helper()
	g := newFakeGuest(1)
	for i, d := range descs {
		writeDesc(g, testRingBase, i, d)
	}
	return g, testRingBase
}

func TestFreezeHappyPath(t *testing.T) {
	descs := []Descriptor{
		{Addr: 0x2000, Length: 128, Next: 1, Flags: FlagRead},
		{Addr: 0x4000, Length: 64, Next: 0, Flags: FlagWrite | FlagEnd},
	}
	g, base := buildRing(t, descs)
	f := NewFreezer(nil, 0)

	chain, err := f.Freeze(g, base, len(descs), 0)
	if err != nil {
		t.Fatalf("Freeze returned error: %v", err)
	}
	if chain.Len() != 2 || chain.TotalBytes() != 192 {
		t.Fatalf("unexpected chain: len=%d total=%d", chain.Len(), chain.TotalBytes())
	}
	if chain.Slot(0) != 0 || chain.Slot(1) != 1 {
		t.Fatalf("slots not preserved: %v %v", chain.Slot(0), chain.Slot(1))
	}
	if got := chain.Descriptor(0); got.Addr != 0x2000 || got.Flags != FlagRead {
		t.Fatalf("descriptor 0 mismatch: %+v", got)
	}
	if !chain.Descriptor(1).Flags.End() {
		t.Fatalf("descriptor 1 must be END")
	}
}

func TestFreezeRingSizeAndHead(t *testing.T) {
	g := newFakeGuest(1)
	f := NewFreezer(nil, 0)

	if _, err := f.Freeze(g, testRingBase, 0, 0); !errors.Is(err, ErrRingSize) {
		t.Fatalf("size 0: want ErrRingSize, got %v", err)
	}
	if _, err := f.Freeze(g, testRingBase, MaxRingEntries+1, 0); !errors.Is(err, ErrRingSize) {
		t.Fatalf("size 65: want ErrRingSize, got %v", err)
	}
	if _, err := f.Freeze(g, testRingBase, 4, 4); !errors.Is(err, ErrIndexOutOfRange) {
		t.Fatalf("head==size: want ErrIndexOutOfRange, got %v", err)
	}
}

func TestFreezeDescriptorValidation(t *testing.T) {
	maxU64 := ^uint64(0)
	cases := []struct {
		name string
		desc Descriptor
		want error
	}{
		{"reserved flag", Descriptor{Addr: 0x2000, Length: 8, Flags: FlagRead | 0x100}, ErrBadFlags},
		{"no direction", Descriptor{Addr: 0x2000, Length: 8, Flags: FlagEnd}, ErrBadFlags},
		{"zero length", Descriptor{Addr: 0x2000, Length: 0, Flags: FlagRead | FlagEnd}, ErrBadLength},
		{"too long", Descriptor{Addr: 0x2000, Length: MaxTransferLen + 1, Flags: FlagRead | FlagEnd}, ErrBadLength},
		{"max length ok", Descriptor{Addr: 0x2000, Length: MaxTransferLen, Flags: FlagRead | FlagEnd}, nil},
		{"overflow", Descriptor{Addr: maxU64 - 7, Length: 16, Flags: FlagRead | FlagEnd}, ErrAddressOverflow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, base := buildRing(t, []Descriptor{tc.desc})
			f := NewFreezer(nil, 0)
			_, err := f.Freeze(g, base, 1, 0)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestFreezeEndStopsTraversal(t *testing.T) {
	// 槽位 1 是永远不应被访问的“毒丸”：越界 next + 非法长度。
	descs := []Descriptor{
		{Addr: 0x2000, Length: 8, Next: 1, Flags: FlagRead | FlagEnd},
		{Addr: 0x3000, Length: 0, Next: 99, Flags: 0},
	}
	g, base := buildRing(t, descs)
	chain, err := NewFreezer(nil, 0).Freeze(g, base, 2, 0)
	if err != nil {
		t.Fatalf("END must terminate before touching slot 1: %v", err)
	}
	if chain.Len() != 1 || chain.Slot(0) != 0 {
		t.Fatalf("chain must contain only slot 0")
	}
}

func TestFreezeIndexOutOfRange(t *testing.T) {
	descs := []Descriptor{
		{Addr: 0x2000, Length: 8, Next: 5, Flags: FlagRead},
	}
	g, base := buildRing(t, descs)
	_, err := NewFreezer(nil, 0).Freeze(g, base, 2, 0)
	if !errors.Is(err, ErrIndexOutOfRange) {
		t.Fatalf("want ErrIndexOutOfRange, got %v", err)
	}
}

func TestFreezeSharedNextSubchain(t *testing.T) {
	// 环中存在两条路径收敛到同一子链：head=0 先走 0 -> 1，槽位 1 的
	// next 又指回槽位 0，使槽位 0 被第二次访问。共享 next 子链必须按
	// 重复项拒绝，而不是默默走成一个环。
	descs := []Descriptor{
		{Addr: 0x2000, Length: 8, Next: 1, Flags: FlagRead},
		{Addr: 0x2100, Length: 8, Next: 0, Flags: FlagRead},
		{Addr: 0x2200, Length: 8, Next: 1, Flags: FlagRead},
	}
	g, base := buildRing(t, descs)
	_, err := NewFreezer(nil, 0).Freeze(g, base, 3, 0)
	if !errors.Is(err, ErrDuplicateEntry) {
		t.Fatalf("shared subchain: want ErrDuplicateEntry, got %v", err)
	}

	// 从另一个入口（槽位 2）出发同样收敛到槽位 1 -> 0，仍须拒绝。
	_, err = NewFreezer(nil, 0).Freeze(g, base, 3, 2)
	if !errors.Is(err, ErrDuplicateEntry) {
		t.Fatalf("alternate entry shared subchain: want ErrDuplicateEntry, got %v", err)
	}
}

func TestFreezeSelfLoopIsDuplicate(t *testing.T) {
	descs := []Descriptor{
		{Addr: 0x2000, Length: 8, Next: 0, Flags: FlagRead},
	}
	g, base := buildRing(t, descs)
	_, err := NewFreezer(nil, 0).Freeze(g, base, 1, 0)
	if !errors.Is(err, ErrDuplicateEntry) {
		t.Fatalf("self loop: want ErrDuplicateEntry, got %v", err)
	}
}

func TestFreezeUnterminatedChain(t *testing.T) {
	// 0 -> 1 -> 2 -> 1，无 END；第二次访问槽位 1 时按重复项拒绝。
	descs := []Descriptor{
		{Addr: 0x2000, Length: 8, Next: 1, Flags: FlagRead},
		{Addr: 0x2100, Length: 8, Next: 2, Flags: FlagRead},
		{Addr: 0x2200, Length: 8, Next: 1, Flags: FlagRead},
	}
	g, base := buildRing(t, descs)
	_, err := NewFreezer(nil, 0).Freeze(g, base, 3, 0)
	if !errors.Is(err, ErrDuplicateEntry) {
		t.Fatalf("want ErrDuplicateEntry, got %v", err)
	}
}

func TestFreezeBudgetExceeded(t *testing.T) {
	descs := []Descriptor{
		{Addr: 0x2000, Length: 100, Next: 1, Flags: FlagRead},
		{Addr: 0x3000, Length: 100, Next: 0, Flags: FlagRead | FlagEnd},
	}
	g, base := buildRing(t, descs)
	_, err := NewFreezer(nil, 150).Freeze(g, base, 2, 0)
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("want ErrBudgetExceeded, got %v", err)
	}
}

func TestFreeze64EntriesAccepted(t *testing.T) {
	const n = MaxRingEntries
	descs := make([]Descriptor, n)
	for i := range descs {
		d := Descriptor{Addr: 0x2000 + uint64(i)*PageSize, Length: 1, Next: uint16(i + 1), Flags: FlagRead}
		if i == n-1 {
			d.Flags = FlagRead | FlagEnd
			d.Next = 0
		}
		descs[i] = d
	}
	g, base := buildRing(t, descs)
	chain, err := NewFreezer(nil, 0).Freeze(g, base, n, 0)
	if err != nil {
		t.Fatalf("64-entry ring must be accepted: %v", err)
	}
	if chain.Len() != n {
		t.Fatalf("want %d entries, got %d", n, chain.Len())
	}
}

package netsec

import (
	"testing"
)

func TestFreezeValidChain(t *testing.T) {
	mem, _, fz := newTestRig()
	writeRing(mem, map[uint16]Descriptor{
		0: {Addr: 0x100, Len: 0x40, Next: 1, Flags: FlagRead},
		1: {Addr: 0x400, Len: 0x80, Next: 2, Flags: FlagWrite},
		2: {Addr: 0x800, Len: 0x20, Next: 0, Flags: FlagRead | FlagWrite | FlagEnd},
	})
	chain := mustFreeze(t, fz, 0)
	if chain.Len() != 3 {
		t.Fatalf("chain length = %d, want 3", chain.Len())
	}
	if got := chain.At(1); got.Addr != 0x400 || got.Len != 0x80 {
		t.Fatalf("desc 1 = %+v", got)
	}
}

func TestFreezeEndStopsTraversal(t *testing.T) {
	mem, _, fz := newTestRig()
	writeRing(mem, map[uint16]Descriptor{
		0: {Addr: 0x100, Len: 0x40, Next: 1, Flags: FlagRead | FlagEnd},
		// END 之后的项即使非法也不得被访问。
		1: {Addr: 0x9000_0000_0000_0000, Len: 0, Next: 63, Flags: 0x8000},
	})
	chain := mustFreeze(t, fz, 0)
	if chain.Len() != 1 {
		t.Fatalf("chain length = %d, want 1 (END must stop traversal)", chain.Len())
	}
}

func TestFreezeMissingEnd(t *testing.T) {
	mem, _, fz := newTestRig()
	descs := make(map[uint16]Descriptor, MaxDescriptors)
	for i := 0; i < MaxDescriptors; i++ {
		descs[uint16(i)] = Descriptor{
			Addr: 0x100, Len: 0x10,
			Next:  uint16((i + 1) % MaxDescriptors),
			Flags: FlagRead,
		}
	}
	writeRing(mem, descs)
	_, err := fz.Freeze(testRingBase, 0)
	expectErr(t, err, ErrMissingEnd)
}

func TestFreezeTraversalErrors(t *testing.T) {
	cases := map[string]struct {
		descs map[uint16]Descriptor
		head  uint16
		want  error
	}{
		"next 越界": {
			descs: map[uint16]Descriptor{
				0: {Addr: 0x100, Len: 0x10, Next: MaxDescriptors, Flags: FlagRead},
			},
			head: 0,
			want: ErrIndexOutOfRange,
		},
		"head 越界": {
			descs: map[uint16]Descriptor{},
			head:  MaxDescriptors,
			want:  ErrIndexOutOfRange,
		},
		"自环重复": {
			descs: map[uint16]Descriptor{
				0: {Addr: 0x100, Len: 0x10, Next: 0, Flags: FlagRead},
			},
			head: 0,
			want: ErrDuplicate,
		},
		"共享子链成环重复": {
			descs: map[uint16]Descriptor{
				0: {Addr: 0x100, Len: 0x10, Next: 1, Flags: FlagRead},
				1: {Addr: 0x100, Len: 0x10, Next: 2, Flags: FlagRead},
				2: {Addr: 0x100, Len: 0x10, Next: 1, Flags: FlagRead},
			},
			head: 0,
			want: ErrDuplicate,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mem, _, fz := newTestRig()
			writeRing(mem, tc.descs)
			_, err := fz.Freeze(testRingBase, tc.head)
			expectErr(t, err, tc.want)
		})
	}
}

// 共享 next 子链本身合法：两条链各自独立冻结都应成功。
func TestFreezeSharedSubchainOK(t *testing.T) {
	mem, _, fz := newTestRig()
	writeRing(mem, map[uint16]Descriptor{
		0: {Addr: 0x100, Len: 0x10, Next: 2, Flags: FlagRead},
		1: {Addr: 0x200, Len: 0x10, Next: 2, Flags: FlagRead},
		2: {Addr: 0x300, Len: 0x10, Next: 3, Flags: FlagRead},
		3: {Addr: 0x400, Len: 0x10, Flags: FlagRead | FlagEnd},
	})
	if c := mustFreeze(t, fz, 0); c.Len() != 3 {
		t.Fatalf("chain from 0: len = %d, want 3", c.Len())
	}
	if c := mustFreeze(t, fz, 1); c.Len() != 3 {
		t.Fatalf("chain from 1: len = %d, want 3", c.Len())
	}
}

func TestFreezeDescriptorValidation(t *testing.T) {
	cases := map[string]struct {
		d    Descriptor
		want error
	}{
		"未知 flag 位": {Descriptor{Addr: 0x100, Len: 0x10, Flags: FlagRead | FlagEnd | 0x8}, ErrBadFlags},
		"长度为 0":     {Descriptor{Addr: 0x100, Len: 0, Flags: FlagRead | FlagEnd}, ErrBadLength},
		"长度超 1MiB":  {Descriptor{Addr: 0x100, Len: MaxDescLen + 1, Flags: FlagRead | FlagEnd}, ErrBadLength},
		"地址+长度溢出":   {Descriptor{Addr: ^uint64(0) - 7, Len: 0x10, Flags: FlagRead | FlagEnd}, ErrAddrOverflow},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mem, _, fz := newTestRig()
			writeRing(mem, map[uint16]Descriptor{0: tc.d})
			_, err := fz.Freeze(testRingBase, 0)
			expectErr(t, err, tc.want)
		})
	}
}

func TestFreezeBudgetExceeded(t *testing.T) {
	mem, reg, fz := newTestRig()
	if err := reg.RegisterDMA(0x100000, 2<<20, PermRead|PermWrite); err != nil {
		t.Fatal(err)
	}
	descs := make(map[uint16]Descriptor)
	for i := 0; i < 9; i++ { // 9 * 1MiB > 8MiB 预算
		flags := uint16(FlagRead)
		if i == 8 {
			flags |= FlagEnd
		}
		descs[uint16(i)] = Descriptor{
			Addr: 0x100000, Len: MaxDescLen,
			Next: uint16(i + 1), Flags: flags,
		}
	}
	writeRing(mem, descs)
	_, err := fz.Freeze(testRingBase, 0)
	expectErr(t, err, ErrBudgetExceeded)
}

func TestFreezePermissions(t *testing.T) {
	setup := func(t *testing.T) (*fakeMemory, *Freezer) {
		mem := newFakeMemory(1 << 20)
		reg := NewRegistry()
		if err := reg.RegisterDMA(0, 0x1000, PermRead|PermWrite); err != nil {
			t.Fatal(err)
		}
		if err := reg.RegisterDMA(0x1000, 0x1000, PermRead); err != nil { // 只读页
			t.Fatal(err)
		}
		return mem, NewFreezer(mem, reg)
	}

	t.Run("写只读页", func(t *testing.T) {
		mem, fz := setup(t)
		writeRing(mem, map[uint16]Descriptor{
			0: {Addr: 0x1800, Len: 0x100, Flags: FlagWrite | FlagEnd},
		})
		_, err := fz.Freeze(testRingBase, 0)
		expectErr(t, err, ErrPermission)
	})

	t.Run("跨页权限放大", func(t *testing.T) {
		mem, fz := setup(t)
		writeRing(mem, map[uint16]Descriptor{
			0: {Addr: 0x800, Len: 0x1000, Flags: FlagRead | FlagWrite | FlagEnd},
		})
		_, err := fz.Freeze(testRingBase, 0)
		expectErr(t, err, ErrPermission)
	})

	t.Run("跨页只读放行", func(t *testing.T) {
		mem, fz := setup(t)
		writeRing(mem, map[uint16]Descriptor{
			0: {Addr: 0x800, Len: 0x1000, Flags: FlagRead | FlagEnd},
		})
		mustFreeze(t, fz, 0)
	})

	t.Run("未登记区间", func(t *testing.T) {
		mem, fz := setup(t)
		writeRing(mem, map[uint16]Descriptor{
			0: {Addr: 0x5000, Len: 0x100, Flags: FlagRead | FlagEnd},
		})
		_, err := fz.Freeze(testRingBase, 0)
		expectErr(t, err, ErrUnmapped)
	})
}

func TestFreezeReservedRegion(t *testing.T) {
	cases := map[string]Descriptor{
		"落在保留区内":  {Addr: 0x2800, Len: 0x100, Flags: FlagRead | FlagEnd},
		"跨界触碰保留区": {Addr: 0x1F00, Len: 0x200, Flags: FlagRead | FlagEnd},
	}
	for name, d := range cases {
		t.Run(name, func(t *testing.T) {
			mem, _, fz := newTestRig()
			writeRing(mem, map[uint16]Descriptor{0: d})
			_, err := fz.Freeze(testRingBase, 0)
			expectErr(t, err, ErrReserved)
		})
	}
}

// 相同版本下字节变化 = ABA，必须拒绝；最终版本相同不能证明期间没变。
func TestSnapshotABAConstantVersion(t *testing.T) {
	mem, _, _ := newTestRig()
	aba := &abaMemory{fakeMemory: mem, flipAddr: testRingBase}
	writeRing(mem, map[uint16]Descriptor{
		0: {Addr: 0x100, Len: 0x40, Flags: FlagRead | FlagEnd},
	})
	reg := NewRegistry()
	if err := reg.RegisterDMA(0, 0x10000, PermRead|PermWrite); err != nil {
		t.Fatal(err)
	}
	fz := NewFreezer(aba, reg)
	v0 := aba.Version()
	_, err := fz.Freeze(testRingBase, 0)
	expectErr(t, err, ErrABA)
	if aba.Version() != v0 {
		t.Fatalf("test premise broken: version should stay constant")
	}
}

// 版本号倒退 = ABA，必须拒绝。
func TestSnapshotVersionRegression(t *testing.T) {
	mem, _, _ := newTestRig()
	rm := &regressMemory{fakeMemory: mem}
	writeRing(mem, map[uint16]Descriptor{
		0: {Addr: 0x100, Len: 0x40, Flags: FlagRead | FlagEnd},
	})
	fz := NewFreezer(rm, NewRegistry())
	_, err := fz.Freeze(testRingBase, 0)
	expectErr(t, err, ErrABA)
}

// 版本持续前进、无法取得一致快照时，有限次重试后放弃。
func TestSnapshotUnstable(t *testing.T) {
	mem, _, _ := newTestRig()
	writeRing(mem, map[uint16]Descriptor{
		0: {Addr: 0x100, Len: 0x40, Flags: FlagRead | FlagEnd},
	})
	mem.onRead = func() { mem.version++ } // 每次读都有并发写
	fz := NewFreezer(mem, NewRegistry())
	_, err := fz.Freeze(testRingBase, 0)
	expectErr(t, err, ErrUnstable)
}

// 正常路径：读取期间的写使版本前进会触发重试，稳定后成功。
func TestSnapshotRetryThenSuccess(t *testing.T) {
	mem, _, fz := newTestRig()
	writeRing(mem, map[uint16]Descriptor{
		0: {Addr: 0x100, Len: 0x40, Flags: FlagRead | FlagEnd},
	})
	n := 0
	mem.onRead = func() {
		n++
		if n == 1 {
			mem.version++ // 仅第一次读期间有并发写
		}
	}
	mustFreeze(t, fz, 0)
}

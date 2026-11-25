package dma

import (
	"errors"
	"testing"
)

func simpleRing(g *fakeGuest, addr uint64) {
	writeDesc(g, testRingBase, 0, Descriptor{Addr: addr, Length: 64, Next: 0, Flags: FlagRead | FlagEnd})
}

func TestSnapshotVersionAdvanceRejected(t *testing.T) {
	g := newFakeGuest(1)
	simpleRing(g, 0x2000)

	// 第 1 次 ReadAt 期间把版本从 1 推到 2：读后版本与读前不一致。
	g.beforeRead = func(guest *fakeGuest) {
		if guest.reads == 1 {
			guest.version++
		}
	}
	_, err := NewFreezer(nil, 0).Freeze(g, testRingBase, 1, 0)
	if !errors.Is(err, ErrVersionChanged) {
		t.Fatalf("want ErrVersionChanged, got %v", err)
	}
}

func TestSnapshotVersionRegressionRejected(t *testing.T) {
	g := newFakeGuest(5)
	simpleRing(g, 0x2000)

	// 第一次快照内版本 5 -> 6，第二次快照内版本倒退到 4。
	g.beforeRead = func(guest *fakeGuest) {
		switch guest.reads {
		case 1:
			guest.version = 6
		case 2:
			guest.version = 4
		}
	}
	_, err := NewFreezer(nil, 0).Freeze(g, testRingBase, 1, 0)
	if !errors.Is(err, ErrVersionRegressed) {
		t.Fatalf("want ErrVersionRegressed, got %v", err)
	}
}

func TestSnapshotSameVersionByteChangeIsABA(t *testing.T) {
	g := newFakeGuest(7)
	simpleRing(g, 0x2000)

	// 版本号始终为 7，但在第二次整环读取时静默把地址改成受保护页。
	mutated := false
	g.beforeRead = func(guest *fakeGuest) {
		if guest.reads == 2 && !mutated {
			buf := make([]byte, DescriptorSize)
			encodeDescriptor(buf, Descriptor{
				Addr: 0x9000, Length: 64, Next: 0, Flags: FlagRead | FlagEnd,
			})
			guest.writeLocked(testRingBase, buf) // 故意不推进版本号
			mutated = true
		}
	}
	_, err := NewFreezer(nil, 0).Freeze(g, testRingBase, 1, 0)
	if !errors.Is(err, ErrABAMutation) {
		t.Fatalf("same-version byte change: want ErrABAMutation, got %v", err)
	}
}

func TestSnapshotEqualEndVersionDoesNotProveStability(t *testing.T) {
	g := newFakeGuest(1)
	simpleRing(g, 0x2000)

	// 第一次快照读到版本 1；第二次快照读取期间内存被改写并推进到 2，
	// 随后回滚到 1，使 Freeze 入口与最终版本相同。无论中途观察到的是
	// 前进还是倒退，快照都必须拒绝——最终版本相同不能证明期间没变化。
	g.beforeRead = func(guest *fakeGuest) {
		if guest.reads == 2 {
			buf := make([]byte, DescriptorSize)
			encodeDescriptor(buf, Descriptor{
				Addr: 0x9000, Length: 64, Next: 0, Flags: FlagRead | FlagEnd,
			})
			guest.putLocked(testRingBase, buf) // 把版本推到 2
		}
		if guest.reads == 3 && guest.version != 1 {
			guest.version = 1 // 回滚，最终版本与入口一致
		}
	}
	_, err := NewFreezer(nil, 0).Freeze(g, testRingBase, 1, 0)
	if err == nil {
		t.Fatal("snapshot must be rejected when memory changes between reads")
	}
	if !errors.Is(err, ErrVersionChanged) && !errors.Is(err, ErrVersionRegressed) && !errors.Is(err, ErrABAMutation) {
		t.Fatalf("want a snapshot consistency error, got %v", err)
	}
}

func TestFrozenChainImmuneToLaterGuestWrites(t *testing.T) {
	g := newFakeGuest(1)
	simpleRing(g, 0x2000)
	chain, err := NewFreezer(nil, 0).Freeze(g, testRingBase, 1, 0)
	if err != nil {
		t.Fatalf("Freeze failed: %v", err)
	}

	// 冻结成功后，攻击者再把客户机环里的地址换到受保护页。
	writeDesc(g, testRingBase, 0, Descriptor{
		Addr: 0x9000, Length: 64, Next: 0, Flags: FlagRead | FlagEnd,
	})
	if got := readDesc(g, testRingBase, 0); got.Addr != 0x9000 {
		t.Fatalf("guest mutation not applied: %+v", got)
	}
	if chain.Descriptor(0).Addr != 0x2000 {
		t.Fatalf("frozen chain must keep private snapshot, got addr=%#x", chain.Descriptor(0).Addr)
	}

	// 通过访问器拿到的是值拷贝，修改它也不能污染冻结链。
	modified := chain.Descriptor(0)
	modified.Addr = 0xDEADBEEF
	if chain.Descriptor(0).Addr != 0x2000 {
		t.Fatal("frozen chain must remain immutable after caller mutates returned value")
	}
}

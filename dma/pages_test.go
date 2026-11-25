package dma

import (
	"errors"
	"testing"
)

func registeredTable(t *testing.T) *PageTable {
	t.Helper()
	pt := NewPageTable()
	// 0x1000..0x2000 只读；0x2000..0x4000 读写。
	if err := pt.Register(0x1000, PageSize, PermRead); err != nil {
		t.Fatalf("register ro: %v", err)
	}
	if err := pt.Register(0x2000, 2*PageSize, PermRead|PermWrite); err != nil {
		t.Fatalf("register rw: %v", err)
	}
	if err := pt.Reserve(0x8000, PageSize); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	return pt
}

func TestPageTableCrossPageSamePerms(t *testing.T) {
	pt := registeredTable(t)
	// 跨 0x2000 与 0x3000 两页，二者同属一个读写登记区，逐页校验通过。
	d := Descriptor{Addr: 0x2800, Length: 0x1000, Flags: FlagWrite}
	if err := pt.validate(d); err != nil {
		t.Fatalf("cross-page RW must pass: %v", err)
	}

	// 跨越两个相邻登记区边界（0x3000 RW -> 0x4000 之外是空洞）应拒绝，
	// 而相邻同权限页（0x1000 RO、0x2000 RW）的读传输应通过。
	readAcross := Descriptor{Addr: 0x1f00, Length: 0x200, Flags: FlagRead}
	if err := pt.validate(readAcross); err != nil {
		t.Fatalf("read across adjacent regions must pass: %v", err)
	}
}

func TestPageTableCrossPageWeakerPageDenied(t *testing.T) {
	pt := registeredTable(t)
	// 区间从可读写页跨入只读页，写权限超过底层只读页。
	d := Descriptor{Addr: 0x1f00, Length: 0x400, Flags: FlagWrite}
	if err := pt.validate(d); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("want ErrPermissionDenied, got %v", err)
	}
}

func TestPageTableReadAcrossROAndRW(t *testing.T) {
	pt := registeredTable(t)
	d := Descriptor{Addr: 0x1f00, Length: 0x400, Flags: FlagRead}
	if err := pt.validate(d); err != nil {
		t.Fatalf("read across RO/RW must pass: %v", err)
	}
}

func TestPageTableUnmapped(t *testing.T) {
	pt := registeredTable(t)
	d := Descriptor{Addr: 0x6000, Length: 16, Flags: FlagRead | FlagEnd}
	if err := pt.validate(d); !errors.Is(err, ErrPageNotMapped) {
		t.Fatalf("want ErrPageNotMapped, got %v", err)
	}
	// 区间一端落在空洞上同样拒绝。
	d2 := Descriptor{Addr: 0x3ff0, Length: 32, Flags: FlagRead}
	if err := pt.validate(d2); !errors.Is(err, ErrPageNotMapped) {
		t.Fatalf("partial unmapped: want ErrPageNotMapped, got %v", err)
	}
}

func TestPageTableReserved(t *testing.T) {
	pt := registeredTable(t)
	d := Descriptor{Addr: 0x8010, Length: 16, Flags: FlagRead}
	if err := pt.validate(d); !errors.Is(err, ErrReservedRegion) {
		t.Fatalf("want ErrReservedRegion, got %v", err)
	}
	// 与保留区擦边即拒绝。
	d2 := Descriptor{Addr: 0x7fff, Length: 2, Flags: FlagRead}
	if err := pt.validate(d2); !errors.Is(err, ErrReservedRegion) {
		t.Fatalf("overlap edge: want ErrReservedRegion, got %v", err)
	}
}

func TestPageTableRegistrationRules(t *testing.T) {
	pt := NewPageTable()
	if err := pt.Register(0x1001, PageSize, PermRead); !errors.Is(err, ErrAlignment) {
		t.Fatalf("unaligned base: want ErrAlignment, got %v", err)
	}
	if err := pt.Register(0x1000, 100, PermRead); !errors.Is(err, ErrAlignment) {
		t.Fatalf("unaligned length: want ErrAlignment, got %v", err)
	}
	if err := pt.Register(0x1000, PageSize, 0); !errors.Is(err, ErrAlignment) {
		t.Fatalf("zero perm: want ErrAlignment, got %v", err)
	}
	if err := pt.Register(0x1000, PageSize, PermRead); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := pt.Register(0x1000, PageSize, PermRead); !errors.Is(err, ErrRegionOverlap) {
		t.Fatalf("overlap: want ErrRegionOverlap, got %v", err)
	}
	if err := pt.Reserve(0x2000, PageSize); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := pt.Register(0x2000, PageSize, PermRead); !errors.Is(err, ErrRegionOverlap) {
		t.Fatalf("register over reserve: want ErrRegionOverlap, got %v", err)
	}
}

func TestFreezeRejectsPageViolations(t *testing.T) {
	pt := registeredTable(t)
	g := newFakeGuest(1)
	writeDesc(g, testRingBase, 0, Descriptor{Addr: 0x8000, Length: 16, Next: 0, Flags: FlagRead | FlagEnd})

	_, err := NewFreezer(pt, 0).Freeze(g, testRingBase, 1, 0)
	if !errors.Is(err, ErrReservedRegion) {
		t.Fatalf("freeze must reject reserved DMA: %v", err)
	}
}

package netsec

import (
	"errors"
	"testing"
)

const (
	// Guest A memory layout.
	ramBaseA = 0x10000000 // 1 MiB RW
	ramSizeA = 0x100000
	roBaseA  = 0x10100000 // 1 MiB read-only, adjacent to ramBaseA
	roSizeA  = 0x100000

	// Guest B memory layout.
	ramBaseB = 0x40000000 // 1 MiB RW
	ramSizeB = 0x100000
)

// newTestManager builds a manager with two guests and one domain each.
func newTestManager(t *testing.T) (*Manager, *Domain, *Domain) {
	t.Helper()
	m := NewManager()

	ga, err := m.NewGuest("guest-a")
	if err != nil {
		t.Fatalf("NewGuest a: %v", err)
	}
	if err := ga.AddRegion(ramBaseA, ramSizeA, PermRead|PermWrite); err != nil {
		t.Fatalf("AddRegion a rw: %v", err)
	}
	if err := ga.AddRegion(roBaseA, roSizeA, PermRead); err != nil {
		t.Fatalf("AddRegion a ro: %v", err)
	}

	gb, err := m.NewGuest("guest-b")
	if err != nil {
		t.Fatalf("NewGuest b: %v", err)
	}
	if err := gb.AddRegion(ramBaseB, ramSizeB, PermRead|PermWrite); err != nil {
		t.Fatalf("AddRegion b: %v", err)
	}

	domA, err := m.NewDomain("dom-a", "guest-a")
	if err != nil {
		t.Fatalf("NewDomain a: %v", err)
	}
	domB, err := m.NewDomain("dom-b", "guest-b")
	if err != nil {
		t.Fatalf("NewDomain b: %v", err)
	}
	return m, domA, domB
}

func TestMapMSIWindowRejected(t *testing.T) {
	_, domA, _ := newTestManager(t)

	cases := []struct {
		name string
		iova uint64
		size uint64
	}{
		{"exact window", MSIWindowBase, MSIWindowEnd - MSIWindowBase},
		{"first page of window", MSIWindowBase, PageSize},
		{"last page of window", MSIWindowEnd - PageSize, PageSize},
		{"inside window", 0xFEE80000, PageSize},
		{"spills into window from below", MSIWindowBase - PageSize, 2 * PageSize},
		{"spills out of window above", MSIWindowEnd - PageSize, 2 * PageSize},
		{"covers whole window", MSIWindowBase - 0x10000, (MSIWindowEnd - MSIWindowBase) + 0x20000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := domA.Map(tc.iova, ramBaseA, tc.size, PermRead)
			if !errors.Is(err, ErrReservedIOVA) {
				t.Fatalf("Map(iova=%#x) = %v, want ErrReservedIOVA", tc.iova, err)
			}
		})
	}
}

func TestMapAdjacentToMSIWindowAllowed(t *testing.T) {
	_, domA, _ := newTestManager(t)

	// Ranges that touch but do not intersect the MSI window are legal.
	if err := domA.Map(MSIWindowBase-PageSize, ramBaseA, PageSize, PermRead); err != nil {
		t.Fatalf("map below window: %v", err)
	}
	if err := domA.Map(MSIWindowEnd, ramBaseA+PageSize, PageSize, PermRead); err != nil {
		t.Fatalf("map above window: %v", err)
	}
}

func TestMapOverlapRejected(t *testing.T) {
	_, domA, _ := newTestManager(t)

	if err := domA.Map(0x1000, ramBaseA, 4*PageSize, PermRead); err != nil {
		t.Fatalf("initial map: %v", err)
	}

	cases := []struct {
		name string
		iova uint64
		size uint64
	}{
		{"identical range", 0x1000, 4 * PageSize},
		{"same base smaller", 0x1000, PageSize},
		{"interior page", 0x2000, PageSize},
		{"overlaps head", 0x0, 2 * PageSize},
		{"overlaps tail", 0x4000, 2 * PageSize},
		{"covers existing", 0x0, 8 * PageSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := domA.Map(tc.iova, ramBaseA, tc.size, PermRead)
			if !errors.Is(err, ErrOverlap) {
				t.Fatalf("Map(iova=%#x) = %v, want ErrOverlap", tc.iova, err)
			}
		})
	}

	// A disjoint range directly after the existing one is fine.
	if err := domA.Map(0x5000, ramBaseA, PageSize, PermRead); err != nil {
		t.Fatalf("adjacent map: %v", err)
	}
}

func TestMapPermissionAmplificationRejected(t *testing.T) {
	_, domA, _ := newTestManager(t)

	// Read-only guest pages cannot be mapped writable...
	if err := domA.Map(0x10000, roBaseA, PageSize, PermRead|PermWrite); !errors.Is(err, ErrPermExceed) {
		t.Fatalf("rw map of ro pages = %v, want ErrPermExceed", err)
	}
	// ...not even write-only.
	if err := domA.Map(0x10000, roBaseA, PageSize, PermWrite); !errors.Is(err, ErrPermExceed) {
		t.Fatalf("wo map of ro pages = %v, want ErrPermExceed", err)
	}
	// A range spanning RW and RO regions is limited by the strictest one.
	if err := domA.Map(0x20000, ramBaseA+ramSizeA-PageSize, 2*PageSize, PermRead|PermWrite); !errors.Is(err, ErrPermExceed) {
		t.Fatalf("rw map spanning rw+ro = %v, want ErrPermExceed", err)
	}

	// Read-only mappings of read-only pages, and downgraded mappings of
	// RW pages, stay within the ceiling and succeed.
	if err := domA.Map(0x10000, roBaseA, PageSize, PermRead); err != nil {
		t.Fatalf("ro map of ro pages: %v", err)
	}
	if err := domA.Map(0x20000, ramBaseA+ramSizeA-PageSize, 2*PageSize, PermRead); err != nil {
		t.Fatalf("ro map spanning rw+ro: %v", err)
	}
	if err := domA.Map(0x30000, ramBaseA, PageSize, PermRead|PermWrite); err != nil {
		t.Fatalf("rw map of rw pages: %v", err)
	}
}

func TestMapForeignPageRejected(t *testing.T) {
	_, domA, domB := newTestManager(t)

	// Domain of guest A may not map guest B's pages.
	if err := domA.Map(0x10000, ramBaseB, PageSize, PermRead); !errors.Is(err, ErrForeignPage) {
		t.Fatalf("map of guest-b page = %v, want ErrForeignPage", err)
	}
	// A range starting in A's memory and spilling into unowned space.
	if err := domA.Map(0x20000, ramBaseA+ramSizeA+roSizeA-PageSize, 2*PageSize, PermRead); !errors.Is(err, ErrForeignPage) {
		t.Fatalf("map spilling out of guest-a = %v, want ErrForeignPage", err)
	}
	// A hole with no backing memory at all.
	if err := domA.Map(0x30000, 0x60000000, PageSize, PermRead); !errors.Is(err, ErrForeignPage) {
		t.Fatalf("map of unowned hole = %v, want ErrForeignPage", err)
	}
	// Guest B's domain mapping its own pages still works.
	if err := domB.Map(0x10000, ramBaseB, PageSize, PermRead|PermWrite); err != nil {
		t.Fatalf("guest-b map of own pages: %v", err)
	}
}

func TestMapAddressWrapRejected(t *testing.T) {
	_, domA, _ := newTestManager(t)
	const top = ^uint64(0) // 2^64 - 1

	cases := []struct {
		name string
		iova uint64
		gpa  uint64
		size uint64
	}{
		{"iova wraps", top - PageSize + 1, ramBaseA, 2 * PageSize},
		{"iova ends exactly at 2^64", top - PageSize + 1, ramBaseA, PageSize},
		{"gpa wraps", 0x10000, top - PageSize + 1, 2 * PageSize},
		{"gpa ends exactly at 2^64", 0x10000, top - PageSize + 1, PageSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := domA.Map(tc.iova, tc.gpa, tc.size, PermRead)
			if !errors.Is(err, ErrAddressWrap) {
				t.Fatalf("Map(iova=%#x gpa=%#x size=%#x) = %v, want ErrAddressWrap",
					tc.iova, tc.gpa, tc.size, err)
			}
		})
	}

	// The highest complete page that fits the address space is legal.
	if err := domA.Map(top-2*PageSize+1, ramBaseA, PageSize, PermRead); err != nil {
		t.Fatalf("map of top valid page: %v", err)
	}
}

func TestDeviceDomainDeterminedByGroup(t *testing.T) {
	m, domA, domB := newTestManager(t)

	for dev, grp := range map[string]int{"dev-a1": 1, "dev-a2": 1, "dev-b1": 2} {
		if err := m.RegisterDevice(dev, grp); err != nil {
			t.Fatalf("RegisterDevice %s: %v", dev, err)
		}
	}

	if err := m.AttachDevice("dom-a", "dev-a1"); err != nil {
		t.Fatalf("attach dev-a1: %v", err)
	}
	// Every device of group 1 resolves to dom-a, even one never attached.
	for _, dev := range []string{"dev-a1", "dev-a2"} {
		d, err := m.DomainOfDevice(dev)
		if err != nil || d != domA {
			t.Fatalf("DomainOfDevice(%s) = %v, %v; want dom-a", dev, d, err)
		}
	}
	// A group already bound to dom-a cannot be stolen into dom-b.
	if err := m.AttachDevice("dom-b", "dev-a2"); !errors.Is(err, ErrGroupAttached) {
		t.Fatalf("re-attach group 1 to dom-b = %v, want ErrGroupAttached", err)
	}
	// A different group attaches freely.
	if err := m.AttachDevice("dom-b", "dev-b1"); err != nil {
		t.Fatalf("attach dev-b1: %v", err)
	}
	if d, _ := m.DomainOfDevice("dev-b1"); d != domB {
		t.Fatalf("DomainOfDevice(dev-b1) = %v, want dom-b", d)
	}
}

func TestMapLookupUnmap(t *testing.T) {
	_, domA, _ := newTestManager(t)

	if err := domA.Map(0x8000, ramBaseA, 2*PageSize, PermRead|PermWrite); err != nil {
		t.Fatalf("map: %v", err)
	}
	gpa, perm, ok := domA.Lookup(0x8123)
	if !ok || gpa != ramBaseA+0x123 || perm != PermRead|PermWrite {
		t.Fatalf("Lookup = %#x, %v, %v", gpa, perm, ok)
	}
	if _, _, ok := domA.Lookup(0x7000); ok {
		t.Fatal("Lookup outside mapping succeeded")
	}

	// Unmap must cover whole mappings.
	if err := domA.Unmap(0x8000, PageSize); !errors.Is(err, ErrUnmapPartial) {
		t.Fatalf("partial unmap = %v, want ErrUnmapPartial", err)
	}
	if err := domA.Unmap(0x8000, 2*PageSize); err != nil {
		t.Fatalf("unmap: %v", err)
	}
	if _, _, ok := domA.Lookup(0x8123); ok {
		t.Fatal("Lookup after unmap succeeded")
	}
	// The IOVA range is reusable after unmap.
	if err := domA.Map(0x8000, ramBaseA, PageSize, PermRead); err != nil {
		t.Fatalf("remap after unmap: %v", err)
	}
}

func TestMapValidation(t *testing.T) {
	_, domA, _ := newTestManager(t)

	if err := domA.Map(0x1001, ramBaseA, PageSize, PermRead); !errors.Is(err, ErrUnaligned) {
		t.Fatalf("unaligned iova = %v, want ErrUnaligned", err)
	}
	if err := domA.Map(0x1000, ramBaseA, 0x100, PermRead); !errors.Is(err, ErrUnaligned) {
		t.Fatalf("unaligned size = %v, want ErrUnaligned", err)
	}
	if err := domA.Map(0x1000, ramBaseA, 0, PermRead); !errors.Is(err, ErrInvalidSize) {
		t.Fatalf("zero size = %v, want ErrInvalidSize", err)
	}
	if err := domA.Map(0x1000, ramBaseA+1, PageSize, PermRead); !errors.Is(err, ErrUnaligned) {
		t.Fatalf("unaligned gpa = %v, want ErrUnaligned", err)
	}
}

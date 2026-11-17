package netsec

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

const (
	tenantA TenantID = 1
	tenantB TenantID = 2
	dev1    DeviceID = 11
	dev2    DeviceID = 22
)

// testRig builds a host with two tenants and one domain per device. Physical
// frames are plain page-aligned simulated addresses; no hardware is touched.
type testRig struct {
	host *Host
	io   *IOMMU
	d1   *Domain
	d2   *Domain
}

func newRig(t *testing.T) *testRig {
	t.Helper()
	host := NewHost()
	io := NewIOMMU(host)
	d1, err := io.NewDomain(dev1)
	if err != nil {
		t.Fatalf("NewDomain dev1: %v", err)
	}
	d2, err := io.NewDomain(dev2)
	if err != nil {
		t.Fatalf("NewDomain dev2: %v", err)
	}
	return &testRig{host: host, io: io, d1: d1, d2: d2}
}

func phys(frame int) PhysAddr { return PhysAddr(frame * PageSize) }
func iova(frame int) IOVA     { return IOVA(frame * PageSize) }

// assignFrames gives tenant ownership of a run of physical frames.
func (r *testRig) assignFrames(t *testing.T, tenant TenantID, frames ...int) {
	t.Helper()
	for _, f := range frames {
		if err := r.host.Assign(phys(f), tenant); err != nil {
			t.Fatalf("Assign frame %d: %v", f, err)
		}
	}
}

func (r *testRig) mapPage(t *testing.T, d *Domain, iovaFrame, physFrame int, perm Perm, tenant TenantID) {
	t.Helper()
	if err := d.Map(iova(iovaFrame), phys(physFrame), perm, tenant); err != nil {
		t.Fatalf("Map iova %d -> phys %d: %v", iovaFrame, physFrame, err)
	}
}

// recordingCallback counts invocations and records every physical operation.
func recordingCallback() (MemCallback, *atomic.Int64, *[]MemOp) {
	var calls atomic.Int64
	var mu sync.Mutex
	var seen []MemOp
	cb := func(op MemOp) error {
		calls.Add(1)
		mu.Lock()
		seen = append(seen, op)
		mu.Unlock()
		return nil
	}
	return cb, &calls, &seen
}

func TestHostOwnershipSingleTenantPerFrame(t *testing.T) {
	r := newRig(t)
	r.assignFrames(t, tenantA, 100)

	if err := r.host.Assign(phys(100), tenantB); !errors.Is(err, ErrConflict) {
		t.Fatalf("reassign to other tenant: want ErrConflict, got %v", err)
	}
	if err := r.host.Assign(phys(100), tenantA); err != nil {
		t.Fatalf("idempotent same-tenant assign: %v", err)
	}
	if err := r.host.Assign(PhysAddr(7), tenantA); !errors.Is(err, ErrUnaligned) {
		t.Fatalf("unaligned assign: want ErrUnaligned, got %v", err)
	}
	owner, ok := r.host.OwnerOf(phys(100))
	if !ok || owner != tenantA {
		t.Fatalf("OwnerOf = (%d,%v), want tenantA,true", owner, ok)
	}
}

func TestIndependentDomainsAndGenerations(t *testing.T) {
	r := newRig(t)
	r.assignFrames(t, tenantA, 0, 1)
	r.mapPage(t, r.d1, 0, 0, PermRead|PermWrite, tenantA)

	g1 := r.d1.Generation()
	r.mapPage(t, r.d1, 1, 1, PermRead, tenantA)
	if g := r.d1.Generation(); g != g1+1 {
		t.Fatalf("d1 generation after map = %d, want %d", g, g1+1)
	}
	if g := r.d2.Generation(); g != 1 {
		t.Fatalf("d2 generation = %d, want 1", g)
	}
	cb, calls, _ := recordingCallback()
	_, err := r.io.Transfer(dev2, []Segment{{IOVA: iova(0), Len: 8}}, false, cb)
	if !errors.Is(err, ErrUnmapped) {
		t.Fatalf("d2 isolated table: want ErrUnmapped, got %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("callback ran on failed transfer: %d calls", calls.Load())
	}
}

func TestUnknownDeviceRejectedWithoutCallback(t *testing.T) {
	r := newRig(t)
	cb, calls, _ := recordingCallback()
	_, err := r.io.Transfer(999, []Segment{{IOVA: iova(0), Len: 1}}, false, cb)
	if !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("want ErrUnknownDevice, got %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("callback invoked for unknown device")
	}
}

func TestCrossPageSplitAndTransfer(t *testing.T) {
	r := newRig(t)
	r.assignFrames(t, tenantA, 10, 11, 12)
	r.mapPage(t, r.d1, 0, 10, PermRead, tenantA)
	r.mapPage(t, r.d1, 1, 11, PermRead, tenantA)
	r.mapPage(t, r.d1, 2, 12, PermRead, tenantA)

	// Start 100 bytes into frame 0 and span all three pages.
	list := []Segment{{IOVA: IOVA(100), Len: PageSize + PageSize + 200}}
	cb, calls, seen := recordingCallback()
	res, err := r.io.Transfer(dev1, list, false, cb)
	if err != nil {
		t.Fatalf("Transfer: %v", err)
	}
	if res.Operations != 3 || calls.Load() != 3 {
		t.Fatalf("want 3 page ops, got %d (%d callbacks)", res.Operations, calls.Load())
	}
	wantLens := []uint64{PageSize - 100, PageSize, 300}
	for i, op := range *seen {
		if op.Len != wantLens[i] {
			t.Fatalf("op %d len = %d, want %d", i, op.Len, wantLens[i])
		}
		if op.Tenant != tenantA || op.Write {
			t.Fatalf("op %d tenant/write wrong: %+v", i, op)
		}
	}
	if (*seen)[0].Phys != phys(10)+100 {
		t.Fatalf("first phys = %#x, want %#x", (*seen)[0].Phys, uint64(phys(10))+100)
	}
	if r.d1.ActiveLeases() != 0 {
		t.Fatalf("lease not released after transfer")
	}

	// Lease-level API reports the same three page references.
	lease, err := r.io.Acquire(dev1, list, false)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if ok, err := lease.Current(); !ok || err != nil {
		t.Fatalf("fresh lease Current = (%v,%v)", ok, err)
	}
	if lease.Pages() != 3 || len(lease.Operations()) != 3 {
		t.Fatalf("lease pages = %d ops = %d, want 3/3", lease.Pages(), len(lease.Operations()))
	}
	lease.Release()
}

func TestPermissionsEnforcedPerPage(t *testing.T) {
	r := newRig(t)
	r.assignFrames(t, tenantA, 0, 1)
	r.mapPage(t, r.d1, 0, 0, PermRead, tenantA)
	r.mapPage(t, r.d1, 1, 1, PermRead, tenantA)

	cb, calls, _ := recordingCallback()
	_, err := r.io.Transfer(dev1, []Segment{{IOVA: iova(0), Len: PageSize + 1}}, true, cb)
	if !errors.Is(err, ErrPermission) {
		t.Fatalf("want ErrPermission, got %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("callback ran despite permission failure")
	}

	cb2, calls2, _ := recordingCallback()
	if _, err := r.io.Transfer(dev1, []Segment{{IOVA: iova(0), Len: PageSize + 1}}, false, cb2); err != nil {
		t.Fatalf("read transfer: %v", err)
	}
	if calls2.Load() != 2 {
		t.Fatalf("want 2 read ops, got %d", calls2.Load())
	}
}

// TestSecondHalfUnauthorized is the controlled "later list entry is
// unauthorized" case. The first segment is fully valid; the failure is in a
// later segment, and the whole request must be rejected atomically.
func TestSecondHalfUnauthorized(t *testing.T) {
	r := newRig(t)
	r.assignFrames(t, tenantA, 0, 1, 2)
	r.mapPage(t, r.d1, 0, 0, PermRead|PermWrite, tenantA)
	r.mapPage(t, r.d1, 1, 1, PermRead, tenantA)
	r.mapPage(t, r.d1, 2, 2, PermRead|PermWrite, tenantA)

	list := []Segment{
		{IOVA: iova(0), Len: 64},
		{IOVA: iova(1), Len: 64}, // read-only page, write not permitted
		{IOVA: iova(2), Len: 64}, // would be valid, never reached
	}
	cb, calls, seen := recordingCallback()
	_, err := r.io.Transfer(dev1, list, true, cb)
	if !errors.Is(err, ErrPermission) {
		t.Fatalf("want ErrPermission from second segment, got %v", err)
	}
	if calls.Load() != 0 || len(*seen) != 0 {
		t.Fatalf("callback ran before full-list validation: %d calls", calls.Load())
	}

	list = []Segment{
		{IOVA: iova(0), Len: 64},
		{IOVA: iova(9), Len: 64}, // unmapped frame
	}
	cb2, calls2, _ := recordingCallback()
	if _, err := r.io.Transfer(dev1, list, false, cb2); !errors.Is(err, ErrUnmapped) {
		t.Fatalf("want ErrUnmapped, got %v", err)
	}
	if calls2.Load() != 0 {
		t.Fatalf("callback ran with unmapped later segment")
	}
}

func TestOverflowAndSizeCap(t *testing.T) {
	r := newRig(t)
	r.assignFrames(t, tenantA, 0)
	r.mapPage(t, r.d1, 0, 0, PermRead, tenantA)

	cases := []struct {
		name string
		list []Segment
		want error
	}{
		{"empty list", nil, ErrEmptyList},
		{"zero length", []Segment{{IOVA: iova(0), Len: 0}}, ErrEmptyList},
		{"uint64 overflow", []Segment{{IOVA: ^IOVA(0) - 9, Len: 100}}, ErrOverflow},
		{"end wraps zero", []Segment{{IOVA: 1, Len: ^uint64(0)}}, ErrOverflow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cb, calls, _ := recordingCallback()
			_, err := r.io.Transfer(dev1, tc.list, false, cb)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			if calls.Load() != 0 {
				t.Fatalf("callback ran on invalid request")
			}
		})
	}

	r.d1.SetMaxTransfer(PageSize)
	cb, calls, _ := recordingCallback()
	_, err := r.io.Transfer(dev1, []Segment{
		{IOVA: iova(0), Len: PageSize},
		{IOVA: iova(0), Len: 1},
	}, false, cb)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("callback ran past cap")
	}
}

func TestMixedTenantRejected(t *testing.T) {
	r := newRig(t)
	r.assignFrames(t, tenantA, 0)
	r.assignFrames(t, tenantB, 1)
	r.mapPage(t, r.d1, 0, 0, PermRead, tenantA)
	r.mapPage(t, r.d1, 1, 1, PermRead, tenantB)

	cb, calls, _ := recordingCallback()
	_, err := r.io.Transfer(dev1, []Segment{{IOVA: iova(0), Len: PageSize + 1}}, false, cb)
	if !errors.Is(err, ErrMixedTenant) {
		t.Fatalf("want ErrMixedTenant, got %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("callback ran across mixed tenants")
	}
}

// TestPhysicalPageCrossTenantAlias covers the "same physical page, cross
// tenant alias" requirement at two layers:
//  1. The trusted Host refuses to assign one physical frame to two tenants.
//  2. If a page table entry is forged (same-package test) to claim the wrong
//     tenant, resolveLocked cross-checks it against the Host table and refuses
//     the transfer with no callback.
func TestPhysicalPageCrossTenantAlias(t *testing.T) {
	r := newRig(t)
	r.assignFrames(t, tenantA, 50)
	if err := r.host.Assign(phys(50), tenantB); !errors.Is(err, ErrConflict) {
		t.Fatalf("host cross-tenant alias: want ErrConflict, got %v", err)
	}

	r.assignFrames(t, tenantA, 51)
	r.mapPage(t, r.d1, 0, 50, PermRead, tenantA)
	r.mapPage(t, r.d1, 1, 51, PermRead, tenantA)

	// Forge an entry: IOVA frame 1 points at frame 50 (owned by A) but claims
	// tenant B, a classic alias attempt inside the translation table.
	r.d1.mu.Lock()
	r.d1.tab[1] = pte{pfn: 50, perm: PermRead, tenant: tenantB}
	r.d1.mu.Unlock()

	cb, calls, _ := recordingCallback()
	_, err := r.io.Transfer(dev1, []Segment{{IOVA: iova(0), Len: PageSize + 1}}, false, cb)
	if !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("forged alias: want ErrOwnerMismatch, got %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("callback ran with forged cross-tenant alias")
	}

	// A second domain cannot legitimately map tenant B onto A's frame either.
	if err := r.d2.Map(iova(0), phys(50), PermRead, tenantB); !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("cross-tenant Map onto owned frame: want ErrOwnerMismatch, got %v", err)
	}
}

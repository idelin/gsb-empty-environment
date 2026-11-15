package netsec

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

const testTimeout = 3 * time.Second

func newTestIOMMU(t *testing.T) (*IOMMU, TenantID, DeviceID) {
	t.Helper()
	m := New()
	const tenant TenantID = 1
	const device DeviceID = 7
	if err := m.AddTenant(tenant); err != nil {
		t.Fatalf("AddTenant: %v", err)
	}
	if err := m.AssignPages(tenant, 100, 16); err != nil {
		t.Fatalf("AssignPages: %v", err)
	}
	if err := m.AddDevice(device, tenant); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	return m, tenant, device
}

func TestTransferSplitsAtPageBoundaries(t *testing.T) {
	m, _, device := newTestIOMMU(t)
	if err := m.MapRange(device, 0x1000, 2*PageSize, 100, Read|Write); err != nil {
		t.Fatalf("MapRange: %v", err)
	}

	var got []AccessOp
	result, err := m.Transfer(context.Background(), TransferRequest{
		Device: device,
		IOVA:   0x1fe0,
		Length: 0x40,
		Access: Read | Write,
	}, func(op AccessOp) error {
		got = append(got, op)
		return nil
	})
	if err != nil {
		t.Fatalf("Transfer: %v", err)
	}
	if result.Chunks != 2 || result.Bytes != 0x40 {
		t.Fatalf("result = %+v, want 2 chunks and 64 bytes", result)
	}
	if len(got) != 2 {
		t.Fatalf("callbacks = %d, want 2", len(got))
	}
	if got[0].IOVA != 0x1fe0 || got[0].PhysPage != 100 || got[0].PhysOffset != 0xfe0 || got[0].Length != 0x20 {
		t.Errorf("first chunk = %+v", got[0])
	}
	if got[1].IOVA != 0x2000 || got[1].PhysPage != 101 || got[1].PhysOffset != 0 || got[1].Length != 0x20 {
		t.Errorf("second chunk = %+v", got[1])
	}
	if got[0].Domain != got[1].Domain {
		t.Errorf("chunks use different domains: %d and %d", got[0].Domain, got[1].Domain)
	}
	stats, _ := m.Stats(device)
	if stats.Leases != 0 || stats.ActiveMappings != 2 {
		t.Fatalf("stats after transfer = %+v", stats)
	}
}

func TestScatterGatherPreservesListOrder(t *testing.T) {
	m, _, device := newTestIOMMU(t)
	if err := m.MapRange(device, 0, 3*PageSize, 100, Read); err != nil {
		t.Fatalf("MapRange: %v", err)
	}

	var pages []PhysPage
	_, err := m.Transfer(context.Background(), TransferRequest{
		Device: device,
		IOVA:   0,
		Length: 3 * PageSize,
		Access: Read,
		SG: []Segment{
			{IOVA: 2 * PageSize, Length: PageSize},
			{IOVA: 0, Length: PageSize},
			{IOVA: PageSize, Length: PageSize},
		},
	}, func(op AccessOp) error {
		pages = append(pages, op.PhysPage)
		return nil
	})
	if err != nil {
		t.Fatalf("Transfer: %v", err)
	}
	want := []PhysPage{102, 100, 101}
	if len(pages) != len(want) {
		t.Fatalf("pages = %v, want %v", pages, want)
	}
	for i := range want {
		if pages[i] != want[i] {
			t.Fatalf("pages = %v, want %v", pages, want)
		}
	}
}

func TestWholeListIsValidatedBeforeCallback(t *testing.T) {
	tests := []struct {
		name    string
		request func(DeviceID) TransferRequest
		setup   func(*testing.T, *IOMMU, DeviceID)
		want    error
	}{
		{
			name: "missing second page",
			setup: func(t *testing.T, m *IOMMU, device DeviceID) {
				t.Helper()
				if err := m.Map(device, 0, 100, Read|Write); err != nil {
					t.Fatal(err)
				}
			},
			request: func(device DeviceID) TransferRequest {
				return TransferRequest{Device: device, IOVA: 0, Length: 2 * PageSize, Access: Read}
			},
			want: ErrNotMapped,
		},
		{
			name: "second page lacks write permission",
			setup: func(t *testing.T, m *IOMMU, device DeviceID) {
				t.Helper()
				if err := m.Map(device, 0, 100, Read|Write); err != nil {
					t.Fatal(err)
				}
				if err := m.Map(device, PageSize, 101, Read); err != nil {
					t.Fatal(err)
				}
			},
			request: func(device DeviceID) TransferRequest {
				return TransferRequest{
					Device: device,
					IOVA:   0,
					Length: 2 * PageSize,
					Access: Write,
					SG: []Segment{
						{IOVA: 0, Length: PageSize},
						{IOVA: PageSize, Length: PageSize},
					},
				}
			},
			want: ErrPermission,
		},
		{
			name: "segment overflows",
			setup: func(t *testing.T, m *IOMMU, device DeviceID) {
				t.Helper()
				if err := m.MapRange(device, 0, 2*PageSize, 100, Read); err != nil {
					t.Fatal(err)
				}
			},
			request: func(device DeviceID) TransferRequest {
				return TransferRequest{
					Device: device,
					IOVA:   0,
					Length: 2 * PageSize,
					Access: Read,
					SG: []Segment{
						{IOVA: 0, Length: PageSize},
						{IOVA: math.MaxUint64 - 15, Length: 32},
					},
				}
			},
			want: ErrOverflow,
		},
		{
			name: "segment outside request window",
			setup: func(t *testing.T, m *IOMMU, device DeviceID) {
				t.Helper()
				if err := m.MapRange(device, 0, 2*PageSize, 100, Read); err != nil {
					t.Fatal(err)
				}
			},
			request: func(device DeviceID) TransferRequest {
				return TransferRequest{
					Device: device,
					IOVA:   0,
					Length: 2 * PageSize,
					Access: Read,
					SG: []Segment{
						{IOVA: 0, Length: PageSize},
						{IOVA: 2 * PageSize, Length: PageSize},
					},
				}
			},
			want: ErrInvalidIOVA,
		},
		{
			name: "scatter lengths do not cover request",
			setup: func(t *testing.T, m *IOMMU, device DeviceID) {
				t.Helper()
				if err := m.MapRange(device, 0, 2*PageSize, 100, Read); err != nil {
					t.Fatal(err)
				}
			},
			request: func(device DeviceID) TransferRequest {
				return TransferRequest{
					Device: device,
					IOVA:   0,
					Length: 2 * PageSize,
					Access: Read,
					SG:     []Segment{{IOVA: 0, Length: PageSize}},
				}
			},
			want: ErrInvalidLength,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _, device := newTestIOMMU(t)
			tt.setup(t, m, device)

			calls := 0
			_, err := m.Transfer(context.Background(), tt.request(device), func(AccessOp) error {
				calls++
				return nil
			})
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if calls != 0 {
				t.Fatalf("callbacks = %d, want 0", calls)
			}
			stats, _ := m.Stats(device)
			if stats.Leases != 0 {
				t.Fatalf("leases = %d after rejected transfer", stats.Leases)
			}
		})
	}
}

func TestTransferOverflowAndInvalidInput(t *testing.T) {
	m, _, device := newTestIOMMU(t)
	if err := m.Map(device, 0, 100, Read|Write); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		req  TransferRequest
		want error
	}{
		{
			name: "window overflow",
			req:  TransferRequest{Device: device, IOVA: math.MaxUint64 - 15, Length: 32, Access: Read},
			want: ErrOverflow,
		},
		{
			name: "zero length",
			req:  TransferRequest{Device: device, IOVA: 0, Length: 0, Access: Read},
			want: ErrInvalidLength,
		},
		{
			name: "invalid access",
			req:  TransferRequest{Device: device, IOVA: 0, Length: PageSize, Access: 0},
			want: ErrInvalidAccess,
		},
		{
			name: "unknown device",
			req:  TransferRequest{Device: 999, IOVA: 0, Length: PageSize, Access: Read},
			want: ErrUnknownDevice,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			_, err := m.Transfer(context.Background(), tt.req, func(AccessOp) error {
				calls++
				return nil
			})
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if calls != 0 {
				t.Fatalf("callbacks = %d, want 0", calls)
			}
		})
	}

	calls := 0
	_, err := m.Transfer(context.Background(), TransferRequest{
		Device: device,
		IOVA:   0,
		Length: PageSize,
		Access: Read,
	}, nil)
	if !errors.Is(err, ErrNilCallback) || calls != 0 {
		t.Fatalf("nil callback: err=%v calls=%d", err, calls)
	}
}

func TestCrossTenantPhysicalPageCannotBeAliased(t *testing.T) {
	m := New()
	const tenantA TenantID = 10
	const tenantB TenantID = 20
	const deviceA DeviceID = 1
	const deviceB DeviceID = 2
	for _, tenant := range []TenantID{tenantA, tenantB} {
		if err := m.AddTenant(tenant); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.AssignPage(tenantA, 500); err != nil {
		t.Fatal(err)
	}
	if err := m.AssignPage(tenantB, 501); err != nil {
		t.Fatal(err)
	}
	if err := m.AddDevice(deviceA, tenantA); err != nil {
		t.Fatal(err)
	}
	if err := m.AddDevice(deviceB, tenantB); err != nil {
		t.Fatal(err)
	}
	if err := m.Map(deviceA, 0, 500, Read|Write); err != nil {
		t.Fatal(err)
	}

	if err := m.AssignPage(tenantB, 500); !errors.Is(err, ErrPageOwned) {
		t.Fatalf("cross-tenant AssignPage error = %v, want ErrPageOwned", err)
	}
	if owner, _ := m.PageOwner(500); owner != tenantA {
		t.Fatalf("page owner = %d, want %d", owner, tenantA)
	}
	if err := m.Map(deviceB, 0, 500, Read|Write); !errors.Is(err, ErrForeignPage) {
		t.Fatalf("cross-tenant Map error = %v, want ErrForeignPage", err)
	}
	if err := m.Map(deviceB, 0, 501, Read|Write); err != nil {
		t.Fatalf("map tenant B's own page: %v", err)
	}
	if err := m.Remap(deviceB, 0, 500, Read); !errors.Is(err, ErrForeignPage) {
		t.Fatalf("cross-tenant Remap error = %v, want ErrForeignPage", err)
	}
	info, ok := m.Lookup(deviceB, 0)
	if !ok || info.PhysPage != 501 {
		t.Fatalf("failed remap changed mapping: %+v, ok=%v", info, ok)
	}

	var pages []PhysPage
	_, err := m.Transfer(context.Background(), TransferRequest{
		Device: deviceB,
		IOVA:   0,
		Length: PageSize,
		Access: Read,
	}, func(op AccessOp) error {
		pages = append(pages, op.PhysPage)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 || pages[0] != 501 {
		t.Fatalf("device B accessed pages %v", pages)
	}
}

func TestReadOnlyMappingRejectsWrite(t *testing.T) {
	m, _, device := newTestIOMMU(t)
	if err := m.Map(device, 0, 100, Read); err != nil {
		t.Fatal(err)
	}

	calls := 0
	_, err := m.Transfer(context.Background(), TransferRequest{
		Device: device,
		IOVA:   0,
		Length: PageSize,
		Access: Read | Write,
	}, func(AccessOp) error {
		calls++
		return nil
	})
	if !errors.Is(err, ErrPermission) {
		t.Fatalf("error = %v, want ErrPermission", err)
	}
	if calls != 0 {
		t.Fatalf("callbacks = %d, want 0", calls)
	}
}

func TestMixedDomainPageTableEntryNeverInvokesCallback(t *testing.T) {
	m, _, device := newTestIOMMU(t)
	if err := m.Map(device, 0, 100, Read|Write); err != nil {
		t.Fatal(err)
	}

	m.mu.Lock()
	entry := m.devices[device].mappings[0]
	entry.domain = 999
	m.mu.Unlock()

	calls := 0
	_, err := m.Transfer(context.Background(), TransferRequest{
		Device: device,
		IOVA:   0,
		Length: PageSize,
		Access: Read,
	}, func(AccessOp) error {
		calls++
		return nil
	})
	if !errors.Is(err, ErrMixedDomain) {
		t.Fatalf("error = %v, want ErrMixedDomain", err)
	}
	if calls != 0 {
		t.Fatalf("callbacks = %d, want 0", calls)
	}
}

func TestCallbackErrorReleasesLeases(t *testing.T) {
	m, _, device := newTestIOMMU(t)
	if err := m.MapRange(device, 0, 2*PageSize, 100, Read); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("boom")
	_, err := m.Transfer(context.Background(), TransferRequest{
		Device: device,
		IOVA:   0,
		Length: 2 * PageSize,
		Access: Read,
	}, func(AccessOp) error {
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want boom", err)
	}
	stats, _ := m.Stats(device)
	if stats.Leases != 0 {
		t.Fatalf("leases = %d after callback error", stats.Leases)
	}
	if err := m.Unmap(device, 0, 2*PageSize); err != nil {
		t.Fatalf("Unmap after callback error: %v", err)
	}
}

func waitValue[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(testTimeout):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(testTimeout):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func waitUntil(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

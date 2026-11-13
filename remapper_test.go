package netsec

import (
	"errors"
	"sync"
	"testing"
	"time"
)

const (
	domA DomainID = 1
	domB DomainID = 2
)

var (
	reqA = MustRequesterID(0, 1, 0)
	reqB = MustRequesterID(0, 2, 0)
)

type fixture struct {
	r *Remapper
	a *Device
	b *Device
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	r := NewRemapper()
	config := DomainConfig{
		Vectors: VectorRange{Min: 0x20, Max: 0x2f},
		VCPUs: []VCPU{
			{ID: 0, Generation: 1},
			{ID: 1, Generation: 1},
		},
	}
	if err := r.RegisterDomain(domA, config); err != nil {
		t.Fatalf("register domain A: %v", err)
	}
	if err := r.RegisterDomain(domB, config); err != nil {
		t.Fatalf("register domain B: %v", err)
	}
	a, err := r.AttachDevice(reqA, domA)
	if err != nil {
		t.Fatalf("attach A: %v", err)
	}
	b, err := r.AttachDevice(reqB, domB)
	if err != nil {
		t.Fatalf("attach B: %v", err)
	}
	return &fixture{r: r, a: a, b: b}
}

func mustInstall(t *testing.T, d *Device, owner DomainID, req InstallRequest) {
	t.Helper()
	if err := d.Install(owner, req); err != nil {
		t.Fatalf("install %+v: %v", req, err)
	}
}

func TestInstallAuthorizationAndValidation(t *testing.T) {
	f := newFixture(t)
	valid := InstallRequest{
		TableIndex:     0,
		VCPU:           0,
		VCPUGeneration: 1,
		Vector:         0x21,
		DeliveryMode:   DeliveryFixed,
	}
	mustInstall(t, f.a, domA, valid)

	entry, ok := f.r.Lookup(reqA, f.a.Generation(), 0)
	if !ok || !entry.Present {
		t.Fatalf("valid install not visible: ok=%v entry=%+v", ok, entry)
	}
	if entry.DomainID != domA || entry.VCPUID != 0 || entry.Vector != 0x21 {
		t.Fatalf("unexpected entry: %+v", entry)
	}

	tests := []struct {
		name   string
		caller DomainID
		req    InstallRequest
		want   error
	}{
		{
			name:   "foreign domain",
			caller: domB,
			req:    valid,
			want:   ErrForeignDomain,
		},
		{
			name:   "vector out of range",
			caller: domA,
			req: InstallRequest{
				TableIndex: 1, VCPU: 0, VCPUGeneration: 1,
				Vector: 0x40, DeliveryMode: DeliveryFixed,
			},
			want: ErrVectorOutOfRange,
		},
		{
			name:   "vector not representable",
			caller: domA,
			req: InstallRequest{
				TableIndex: 1, VCPU: 0, VCPUGeneration: 1,
				Vector: 0x100, DeliveryMode: DeliveryFixed,
			},
			want: ErrVectorInvalid,
		},
		{
			name:   "unknown vcpu",
			caller: domA,
			req: InstallRequest{
				TableIndex: 1, VCPU: 7, VCPUGeneration: 1,
				Vector: 0x21, DeliveryMode: DeliveryFixed,
			},
			want: ErrUnknownVCPU,
		},
		{
			name:   "stale vcpu generation",
			caller: domA,
			req: InstallRequest{
				TableIndex: 1, VCPU: 0, VCPUGeneration: 9,
				Vector: 0x21, DeliveryMode: DeliveryFixed,
			},
			want: ErrStaleVCPUGeneration,
		},
		{
			name:   "non fixed delivery mode",
			caller: domA,
			req: InstallRequest{
				TableIndex: 1, VCPU: 0, VCPUGeneration: 1,
				Vector: 0x21, DeliveryMode: DeliveryNMI,
			},
			want: ErrDeliveryModeNotFixed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := f.a.Install(tc.caller, tc.req)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want errors.Is(_, %v)", err, tc.want)
			}
		})
	}

	// Every rejected request above must leave the original mapping intact.
	entry, ok = f.r.Lookup(reqA, f.a.Generation(), 0)
	if !ok || !entry.Present || entry.DomainID != domA || entry.Vector != 0x21 {
		t.Fatalf("rejected install modified the existing entry: ok=%v entry=%+v", ok, entry)
	}
}

func TestVectorForgeryCannotCrossDomain(t *testing.T) {
	f := newFixture(t)
	mustInstall(t, f.a, domA, InstallRequest{
		TableIndex: 0, VCPU: 0, VCPUGeneration: 1,
		Vector: 0x21, DeliveryMode: DeliveryFixed,
	})
	mustInstall(t, f.b, domB, InstallRequest{
		TableIndex: 0, VCPU: 0, VCPUGeneration: 1,
		Vector: 0x22, DeliveryMode: DeliveryFixed,
	})

	var called bool
	callback := func(Delivery) error {
		called = true
		return nil
	}

	// Under a vector-only lookup this would select B's entry because A forged
	// the vector B registered. The requester-qualified key must reject it.
	err := f.a.SendInterrupt(0, 0x22, callback)
	if !errors.Is(err, ErrVectorMismatch) {
		t.Fatalf("forged vector: got %v, want ErrVectorMismatch", err)
	}
	if called {
		t.Fatal("forged vector reached a delivery callback")
	}

	// Same table index on B, but a message attributed to A, must also miss.
	err = f.r.Deliver(Interrupt{
		RequesterID: reqA, DeviceGeneration: f.a.Generation(),
		TableIndex: 0, Vector: 0x22,
	}, callback)
	if !errors.Is(err, ErrVectorMismatch) {
		t.Fatalf("raw forged vector: got %v, want ErrVectorMismatch", err)
	}
	if called {
		t.Fatal("raw forged vector reached a delivery callback")
	}

	var got Delivery
	err = f.a.SendInterrupt(0, 0x21, func(d Delivery) error {
		got = d
		return nil
	})
	if err != nil {
		t.Fatalf("legitimate interrupt: %v", err)
	}
	if got.DomainID != domA || got.VCPUID != 0 || got.Vector != 0x21 {
		t.Fatalf("legitimate interrupt delivered to %+v", got)
	}
}

func TestOldGenerationRequestsCannotMatch(t *testing.T) {
	f := newFixture(t)
	mustInstall(t, f.a, domA, InstallRequest{
		TableIndex: 0, VCPU: 0, VCPUGeneration: 1,
		Vector: 0x21, DeliveryMode: DeliveryFixed,
	})
	oldGen := f.a.Generation()

	if err := f.a.Reset(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if f.a.Generation() == oldGen {
		t.Fatal("reset did not advance device generation")
	}
	if _, ok := f.r.Lookup(reqA, oldGen, 0); ok {
		t.Fatal("old-generation entry survived reset")
	}

	called := false
	err := f.r.Deliver(Interrupt{
		RequesterID: reqA, DeviceGeneration: oldGen,
		TableIndex: 0, Vector: 0x21,
	}, func(Delivery) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrStaleDeviceGeneration) {
		t.Fatalf("old-generation message: got %v, want ErrStaleDeviceGeneration", err)
	}
	if called {
		t.Fatal("old-generation message reached a callback")
	}

	// A new generation can install again; the old generation still cannot
	// match even when the same table index and vector are reused.
	newGen := f.a.Generation()
	mustInstall(t, f.a, domA, InstallRequest{
		TableIndex: 0, VCPU: 1, VCPUGeneration: 1,
		Vector: 0x21, DeliveryMode: DeliveryFixed,
	})
	called = false
	err = f.r.Deliver(Interrupt{
		RequesterID: reqA, DeviceGeneration: oldGen,
		TableIndex: 0, Vector: 0x21,
	}, func(Delivery) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrStaleDeviceGeneration) || called {
		t.Fatalf("old generation matched a reused index: err=%v called=%v", err, called)
	}

	var got Delivery
	if err := f.r.Deliver(Interrupt{
		RequesterID: reqA, DeviceGeneration: newGen,
		TableIndex: 0, Vector: 0x21,
	}, func(d Delivery) error {
		got = d
		return nil
	}); err != nil {
		t.Fatalf("current-generation delivery: %v", err)
	}
	if got.DomainID != domA || got.VCPUID != 1 || got.DeviceGeneration != newGen {
		t.Fatalf("unexpected current-generation delivery: %+v", got)
	}
}

func TestReassignRevokesAndReauthorizes(t *testing.T) {
	f := newFixture(t)
	mustInstall(t, f.a, domA, InstallRequest{
		TableIndex: 0, VCPU: 0, VCPUGeneration: 1,
		Vector: 0x21, DeliveryMode: DeliveryFixed,
	})
	oldGen := f.a.Generation()

	replacement, err := f.r.Reassign(f.a, domB)
	if err != nil {
		t.Fatalf("reassign: %v", err)
	}
	if replacement.Owner() != domB {
		t.Fatalf("owner = %d, want %d", replacement.Owner(), domB)
	}
	if replacement.Generation() == oldGen {
		t.Fatal("reassign did not advance device generation")
	}

	err = replacement.Install(domA, InstallRequest{
		TableIndex: 1, VCPU: 0, VCPUGeneration: 1,
		Vector: 0x22, DeliveryMode: DeliveryFixed,
	})
	if !errors.Is(err, ErrForeignDomain) {
		t.Fatalf("old owner install: got %v, want ErrForeignDomain", err)
	}
	if err := f.a.Reset(); !errors.Is(err, ErrStaleDeviceHandle) {
		t.Fatalf("old handle reset: got %v, want ErrStaleDeviceHandle", err)
	}
	if _, ok := f.r.Lookup(reqA, oldGen, 0); ok {
		t.Fatal("old-generation entry survived reassignment")
	}

	mustInstall(t, replacement, domB, InstallRequest{
		TableIndex: 1, VCPU: 1, VCPUGeneration: 1,
		Vector: 0x22, DeliveryMode: DeliveryFixed,
	})
	var got Delivery
	if err := replacement.SendInterrupt(1, 0x22, func(d Delivery) error {
		got = d
		return nil
	}); err != nil {
		t.Fatalf("new owner interrupt: %v", err)
	}
	if got.DomainID != domB || got.VCPUID != 1 {
		t.Fatalf("new owner interrupt went to %+v", got)
	}
	if err := f.a.SendInterrupt(0, 0x21, func(Delivery) error {
		t.Fatal("revoked handle injected an interrupt")
		return nil
	}); !errors.Is(err, ErrStaleDeviceHandle) {
		t.Fatalf("revoked handle: got %v, want ErrStaleDeviceHandle", err)
	}
}

func TestVCPUHotRemovalClosesEntryAndWaitsForInflight(t *testing.T) {
	f := newFixture(t)
	mustInstall(t, f.a, domA, InstallRequest{
		TableIndex: 0, VCPU: 1, VCPUGeneration: 1,
		Vector: 0x21, DeliveryMode: DeliveryFixed,
	})

	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	done := make(chan error, 1)
	go func() {
		done <- f.a.SendInterrupt(0, 0x21, func(Delivery) error {
			close(entered)
			<-release
			return nil
		})
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("delivery did not start")
	}

	removed := make(chan error, 1)
	go func() { removed <- f.r.RemoveVCPU(domA, 1) }()
	select {
	case err := <-removed:
		t.Fatalf("RemoveVCPU returned before in-flight receipt: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-removed:
		if err != nil {
			t.Fatalf("RemoveVCPU: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RemoveVCPU did not complete after receipt")
	}
	if err := <-done; err != nil {
		t.Fatalf("in-flight interrupt: %v", err)
	}

	if _, ok := f.r.Lookup(reqA, f.a.Generation(), 0); ok {
		t.Fatal("entry for removed vCPU survived hot-removal")
	}
	err := f.a.SendInterrupt(0, 0x21, func(Delivery) error {
		t.Fatal("callback ran for removed vCPU")
		return nil
	})
	if !errors.Is(err, ErrNoMapping) {
		t.Fatalf("delivery after removal: got %v, want ErrNoMapping", err)
	}

	if err := f.r.AddVCPU(domA, 1, 1); !errors.Is(err, ErrVCPUGenerationRegress) {
		t.Fatalf("re-add with old generation: got %v", err)
	}
	if err := f.r.AddVCPU(domA, 1, 2); err != nil {
		t.Fatalf("re-add with new generation: %v", err)
	}
	err = f.a.Install(domA, InstallRequest{
		TableIndex: 0, VCPU: 1, VCPUGeneration: 1,
		Vector: 0x21, DeliveryMode: DeliveryFixed,
	})
	if !errors.Is(err, ErrStaleVCPUGeneration) {
		t.Fatalf("install with old vCPU generation: got %v", err)
	}
	mustInstall(t, f.a, domA, InstallRequest{
		TableIndex: 0, VCPU: 1, VCPUGeneration: 2,
		Vector: 0x21, DeliveryMode: DeliveryFixed,
	})
}

func TestVCPUGenerationBumpInvalidatesOldDelivery(t *testing.T) {
	f := newFixture(t)
	mustInstall(t, f.a, domA, InstallRequest{
		TableIndex: 0, VCPU: 0, VCPUGeneration: 1,
		Vector: 0x21, DeliveryMode: DeliveryFixed,
	})
	if err := f.r.AddVCPU(domA, 0, 2); err != nil {
		t.Fatalf("advance vCPU generation: %v", err)
	}
	err := f.a.SendInterrupt(0, 0x21, func(Delivery) error {
		t.Fatal("callback ran for stale vCPU generation")
		return nil
	})
	if !errors.Is(err, ErrStaleVCPUGeneration) {
		t.Fatalf("stale vCPU delivery: got %v, want ErrStaleVCPUGeneration", err)
	}
	err = f.a.Install(domA, InstallRequest{
		TableIndex: 1, VCPU: 0, VCPUGeneration: 1,
		Vector: 0x22, DeliveryMode: DeliveryFixed,
	})
	if !errors.Is(err, ErrStaleVCPUGeneration) {
		t.Fatalf("stale vCPU install: got %v", err)
	}
	mustInstall(t, f.a, domA, InstallRequest{
		TableIndex: 1, VCPU: 0, VCPUGeneration: 2,
		Vector: 0x22, DeliveryMode: DeliveryFixed,
	})
}

func TestVectorRangeChangeRevokesEntries(t *testing.T) {
	f := newFixture(t)
	mustInstall(t, f.a, domA, InstallRequest{
		TableIndex: 0, VCPU: 0, VCPUGeneration: 1,
		Vector: 0x2f, DeliveryMode: DeliveryFixed,
	})
	if err := f.r.SetVectorRange(domA, VectorRange{Min: 0x20, Max: 0x25}); err != nil {
		t.Fatalf("set range: %v", err)
	}
	if _, ok := f.r.Lookup(reqA, f.a.Generation(), 0); ok {
		t.Fatal("entry outside narrowed range survived")
	}
	err := f.a.SendInterrupt(0, 0x2f, func(Delivery) error {
		t.Fatal("callback ran after range narrowing")
		return nil
	})
	if !errors.Is(err, ErrNoMapping) {
		t.Fatalf("delivery after range narrowing: got %v", err)
	}
	err = f.a.Install(domA, InstallRequest{
		TableIndex: 0, VCPU: 0, VCPUGeneration: 1,
		Vector: 0x2f, DeliveryMode: DeliveryFixed,
	})
	if !errors.Is(err, ErrVectorOutOfRange) {
		t.Fatalf("install outside narrowed range: got %v", err)
	}
	if err := f.r.SetVectorRange(domA, VectorRange{Min: 0x20, Max: 0x2f}); err != nil {
		t.Fatalf("widen range: %v", err)
	}
	mustInstall(t, f.a, domA, InstallRequest{
		TableIndex: 0, VCPU: 0, VCPUGeneration: 1,
		Vector: 0x2f, DeliveryMode: DeliveryFixed,
	})
}

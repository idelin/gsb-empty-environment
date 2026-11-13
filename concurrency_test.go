package netsec

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestUpdateWaitsForAllInflightReceipts(t *testing.T) {
	f := newFixture(t)
	const vector Vector = 0x21
	mustInstall(t, f.a, domA, InstallRequest{
		TableIndex: 0, VCPU: 0, VCPUGeneration: 1,
		Vector: vector, DeliveryMode: DeliveryFixed,
	})

	entered := make(chan struct{}, 2)
	releases := []chan struct{}{make(chan struct{}), make(chan struct{})}
	var releaseOnce [2]sync.Once
	defer func() {
		for i := range releases {
			releaseOnce[i].Do(func() { close(releases[i]) })
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := f.a.SendInterrupt(0, vector, func(Delivery) error {
				entered <- struct{}{}
				<-releases[i]
				return nil
			})
			if err != nil {
				t.Errorf("in-flight interrupt: %v", err)
			}
		}(i)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("delivery did not start")
		}
	}

	updated := make(chan error, 1)
	go func() {
		updated <- f.a.Install(domA, InstallRequest{
			TableIndex: 0, VCPU: 1, VCPUGeneration: 1,
			Vector: 0x22, DeliveryMode: DeliveryFixed,
		})
	}()
	select {
	case err := <-updated:
		t.Fatalf("update completed with receipts outstanding: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	entry, ok := f.r.Lookup(reqA, f.a.Generation(), 0)
	if !ok || entry.Present {
		t.Fatalf("entry must be closed during the barrier: ok=%v present=%v", ok, entry.Present)
	}
	err := f.a.SendInterrupt(0, vector, func(Delivery) error {
		t.Fatal("callback ran while entry was closed")
		return nil
	})
	if !errors.Is(err, ErrNoMapping) {
		t.Fatalf("delivery while closed: got %v, want ErrNoMapping", err)
	}

	releaseOnce[0].Do(func() { close(releases[0]) })
	select {
	case err := <-updated:
		t.Fatalf("update completed with one receipt outstanding: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	releaseOnce[1].Do(func() { close(releases[1]) })
	select {
	case err := <-updated:
		if err != nil {
			t.Fatalf("update: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("update did not complete after receipts")
	}
	wg.Wait()

	entry, ok = f.r.Lookup(reqA, f.a.Generation(), 0)
	if !ok || !entry.Present || entry.VCPUID != 1 || entry.Vector != 0x22 {
		t.Fatalf("updated entry: ok=%v entry=%+v", ok, entry)
	}
}

func TestReassignRacesWithInflightDelivery(t *testing.T) {
	f := newFixture(t)
	mustInstall(t, f.a, domA, InstallRequest{
		TableIndex: 0, VCPU: 0, VCPUGeneration: 1,
		Vector: 0x21, DeliveryMode: DeliveryFixed,
	})
	oldGen := f.a.Generation()

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

	type reassignResult struct {
		device *Device
		err    error
	}
	reassigned := make(chan reassignResult, 1)
	go func() {
		device, err := f.r.Reassign(f.a, domB)
		reassigned <- reassignResult{device: device, err: err}
	}()
	select {
	case result := <-reassigned:
		t.Fatalf("Reassign returned before in-flight receipt: %v", result.err)
	case <-time.After(50 * time.Millisecond):
	}

	// The generation is advanced before the barrier, so an old-generation
	// message cannot hit the entry while reassignment is still draining.
	err := f.r.Deliver(Interrupt{
		RequesterID: reqA, DeviceGeneration: oldGen,
		TableIndex: 0, Vector: 0x21,
	}, func(Delivery) error {
		t.Fatal("old-generation message hit during reassignment")
		return nil
	})
	if !errors.Is(err, ErrStaleDeviceGeneration) {
		t.Fatalf("old-generation message: got %v, want ErrStaleDeviceGeneration", err)
	}

	releaseOnce.Do(func() { close(release) })
	var replacement *Device
	select {
	case result := <-reassigned:
		if result.err != nil {
			t.Fatalf("reassign: %v", result.err)
		}
		replacement = result.device
	case <-time.After(2 * time.Second):
		t.Fatal("reassign did not complete after receipt")
	}
	if err := <-done; err != nil {
		t.Fatalf("in-flight interrupt: %v", err)
	}
	if _, ok := f.r.Lookup(reqA, oldGen, 0); ok {
		t.Fatal("old-generation entry survived reassignment")
	}
	if replacement == nil || replacement.Owner() != domB {
		t.Fatalf("replacement handle owner = %v, want %d", replacement, domB)
	}
	if err := f.a.SendInterrupt(0, 0x21, func(Delivery) error {
		t.Fatal("revoked handle injected an interrupt")
		return nil
	}); !errors.Is(err, ErrStaleDeviceHandle) {
		t.Fatalf("revoked handle: got %v, want ErrStaleDeviceHandle", err)
	}
}

func TestConcurrentInstallAndDeliveryNeverCrossesOwner(t *testing.T) {
	f := newFixture(t)
	mustInstall(t, f.a, domA, InstallRequest{
		TableIndex: 0, VCPU: 0, VCPUGeneration: 1,
		Vector: 0x21, DeliveryMode: DeliveryFixed,
	})

	const iterations = 300
	var wg sync.WaitGroup
	start := make(chan struct{})

	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < iterations; i++ {
			vcpu := VCPUID(i % 2)
			vector := Vector(0x21 + (i % 0x0e))
			if err := f.a.Install(domA, InstallRequest{
				TableIndex: 0, VCPU: vcpu, VCPUGeneration: 1,
				Vector: vector, DeliveryMode: DeliveryFixed,
			}); err != nil {
				t.Errorf("install: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < iterations; i++ {
			_ = f.a.SendInterrupt(0, Vector(0x21+(i%0x0e)), func(d Delivery) error {
				if d.DomainID != domA {
					t.Errorf("cross-domain delivery: %+v", d)
				}
				return nil
			})
		}
	}()
	close(start)
	wg.Wait()
}

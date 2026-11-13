package netsec

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func installThree(t *testing.T, f *fixture) {
	t.Helper()
	for i := 0; i < 3; i++ {
		mustInstall(t, f.a, domA, InstallRequest{
			TableIndex:     TableIndex(i),
			VCPU:           0,
			VCPUGeneration: 1,
			Vector:         Vector(0x21 + i),
			DeliveryMode:   DeliveryFixed,
		})
	}
}

func snapshotTriple(t *testing.T, f *fixture) [3]Entry {
	t.Helper()
	var entries [3]Entry
	for i := 0; i < 3; i++ {
		entry, ok := f.r.Lookup(reqA, f.a.Generation(), TableIndex(i))
		if !ok {
			t.Fatalf("entry %d disappeared", i)
		}
		entries[i] = entry
	}
	return entries
}

func TestBatchValidationFailureClosesEverything(t *testing.T) {
	f := newFixture(t)
	installThree(t, f)

	err := f.a.InstallBatch(domA, []InstallRequest{
		{TableIndex: 0, VCPU: 1, VCPUGeneration: 1, Vector: 0x24, DeliveryMode: DeliveryFixed},
		{TableIndex: 1, VCPU: 0, VCPUGeneration: 1, Vector: 0x40, DeliveryMode: DeliveryFixed},
		{TableIndex: 2, VCPU: 1, VCPUGeneration: 1, Vector: 0x25, DeliveryMode: DeliveryFixed},
	})
	var batchErr *BatchError
	if !errors.As(err, &batchErr) {
		t.Fatalf("got %v, want *BatchError", err)
	}
	if !errors.Is(err, ErrVectorOutOfRange) {
		t.Fatalf("batch error lost validation cause: %v", err)
	}
	if !errors.Is(err, ErrBatchAborted) {
		t.Fatalf("batch error lost aborted cause: %v", err)
	}
	for _, entry := range snapshotTriple(t, f) {
		if entry.Present {
			t.Fatalf("entry %d remained deliverable after failed batch: %+v", entry.TableIndex, entry)
		}
	}

	// The failure is fail-closed: no old target is restored, no target is
	// published.
	for i := 0; i < 3; i++ {
		err := f.a.SendInterrupt(TableIndex(i), Vector(0x21+i), func(Delivery) error {
			t.Fatal("callback ran for a closed batch entry")
			return nil
		})
		if !errors.Is(err, ErrNoMapping) {
			t.Fatalf("index %d delivered after failed batch: %v", i, err)
		}
	}
}

func TestBatchSuccessPublishesAll(t *testing.T) {
	f := newFixture(t)
	installThree(t, f)

	err := f.a.InstallBatch(domA, []InstallRequest{
		{TableIndex: 0, VCPU: 1, VCPUGeneration: 1, Vector: 0x24, DeliveryMode: DeliveryFixed},
		{TableIndex: 1, VCPU: 1, VCPUGeneration: 1, Vector: 0x25, DeliveryMode: DeliveryFixed},
		{TableIndex: 2, VCPU: 1, VCPUGeneration: 1, Vector: 0x26, DeliveryMode: DeliveryFixed},
	})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	for i, entry := range snapshotTriple(t, f) {
		if !entry.Present || entry.VCPUID != 1 || entry.Vector != Vector(0x24+i) {
			t.Fatalf("entry %d not published: %+v", i, entry)
		}
	}
	for i := 0; i < 3; i++ {
		var got Delivery
		if err := f.a.SendInterrupt(TableIndex(i), Vector(0x24+i), func(d Delivery) error {
			got = d
			return nil
		}); err != nil {
			t.Fatalf("index %d delivery: %v", i, err)
		}
		if got.DomainID != domA || got.VCPUID != 1 || got.Vector != Vector(0x24+i) {
			t.Fatalf("index %d delivered to %+v", i, got)
		}
	}
}

func TestBatchRejectsForeignAndDuplicateBeforeClosing(t *testing.T) {
	f := newFixture(t)
	installThree(t, f)

	err := f.a.InstallBatch(domB, []InstallRequest{
		{TableIndex: 0, VCPU: 0, VCPUGeneration: 1, Vector: 0x24, DeliveryMode: DeliveryFixed},
	})
	if !errors.Is(err, ErrForeignDomain) {
		t.Fatalf("foreign batch: got %v", err)
	}
	for _, entry := range snapshotTriple(t, f) {
		if !entry.Present {
			t.Fatalf("foreign batch closed entry %d", entry.TableIndex)
		}
	}

	err = f.a.InstallBatch(domA, []InstallRequest{
		{TableIndex: 0, VCPU: 1, VCPUGeneration: 1, Vector: 0x24, DeliveryMode: DeliveryFixed},
		{TableIndex: 0, VCPU: 1, VCPUGeneration: 1, Vector: 0x25, DeliveryMode: DeliveryFixed},
	})
	if !errors.Is(err, ErrDuplicateTableIndex) {
		t.Fatalf("duplicate batch: got %v", err)
	}
	for _, entry := range snapshotTriple(t, f) {
		if !entry.Present {
			t.Fatalf("duplicate batch closed entry %d", entry.TableIndex)
		}
	}
}

func TestBatchCanceledBeforeStartLeavesEntriesUntouched(t *testing.T) {
	f := newFixture(t)
	installThree(t, f)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := f.a.InstallBatchContext(ctx, domA, []InstallRequest{
		{TableIndex: 0, VCPU: 1, VCPUGeneration: 1, Vector: 0x24, DeliveryMode: DeliveryFixed},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled batch: got %v", err)
	}
	for _, entry := range snapshotTriple(t, f) {
		if !entry.Present {
			t.Fatalf("canceled batch closed entry %d", entry.TableIndex)
		}
	}
}

func TestBatchContextCancellationDoesNotRollBackPublishedItems(t *testing.T) {
	f := newFixture(t)
	installThree(t, f)

	// Hold one in-flight receipt for index 1, so the batch must pause there
	// after publishing index 0.
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	inflight := make(chan error, 1)
	go func() {
		inflight <- f.a.SendInterrupt(1, 0x22, func(Delivery) error {
			close(entered)
			<-release
			return nil
		})
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight delivery did not start")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	batchDone := make(chan error, 1)
	go func() {
		batchDone <- f.a.InstallBatchContext(ctx, domA, []InstallRequest{
			{TableIndex: 0, VCPU: 1, VCPUGeneration: 1, Vector: 0x24, DeliveryMode: DeliveryFixed},
			{TableIndex: 1, VCPU: 1, VCPUGeneration: 1, Vector: 0x25, DeliveryMode: DeliveryFixed},
			{TableIndex: 2, VCPU: 1, VCPUGeneration: 1, Vector: 0x26, DeliveryMode: DeliveryFixed},
		})
	}()

	// Wait until index 0 has been published, proving the batch reached the
	// blocked barrier for index 1.
	deadline := time.Now().Add(2 * time.Second)
	for {
		entry, ok := f.r.Lookup(reqA, f.a.Generation(), 0)
		if ok && entry.Present && entry.Vector == 0x24 && entry.VCPUID == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("batch did not publish index 0 before blocking")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()

	var err error
	select {
	case err = <-batchDone:
	case <-time.After(2 * time.Second):
		t.Fatal("batch did not return after cancellation")
	}
	var batchErr *BatchError
	if !errors.As(err, &batchErr) {
		t.Fatalf("got %v, want *BatchError", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("batch error lost cancellation cause: %v", err)
	}

	entries := snapshotTriple(t, f)
	if !entries[0].Present || entries[0].Vector != 0x24 || entries[0].VCPUID != 1 {
		t.Fatalf("published item was rolled back: %+v", entries[0])
	}
	if entries[1].Present {
		t.Fatalf("blocked item was published: %+v", entries[1])
	}
	if entries[2].Present {
		t.Fatalf("unprocessed item was published: %+v", entries[2])
	}
	if !batchErr.Results[0].Applied {
		t.Fatalf("result 0 not marked applied: %+v", batchErr.Results[0])
	}
	if batchErr.Results[1].Applied || batchErr.Results[2].Applied {
		t.Fatalf("failed/unprocessed results marked applied: %+v", batchErr.Results)
	}

	releaseOnce.Do(func() { close(release) })
	if err := <-inflight; err != nil {
		t.Fatalf("in-flight interrupt: %v", err)
	}
}

package netsec

import (
	"context"
	"testing"
)

func TestRemapKeepsLeasedTransferOnOldGeneration(t *testing.T) {
	m, _, device := newTestIOMMU(t)
	if err := m.Map(device, 0, 100, Read|Write); err != nil {
		t.Fatal(err)
	}

	entered := make(chan AccessOp, 1)
	release := make(chan struct{})
	oldDone := make(chan error, 1)
	go func() {
		_, err := m.Transfer(context.Background(), TransferRequest{
			Device: device,
			IOVA:   0,
			Length: PageSize,
			Access: Read,
		}, func(op AccessOp) error {
			entered <- op
			<-release
			return nil
		})
		oldDone <- err
	}()

	oldOp := waitValue(t, entered, "old transfer callback")
	if oldOp.Generation != 1 || oldOp.PhysPage != 100 {
		t.Fatalf("old callback = %+v", oldOp)
	}
	if err := m.Remap(device, 0, 101, Read); err != nil {
		t.Fatalf("Remap: %v", err)
	}
	newInfo, ok := m.Lookup(device, 0)
	if !ok || newInfo.PhysPage != 101 || newInfo.Generation != 2 {
		t.Fatalf("new mapping = %+v, ok=%v", newInfo, ok)
	}

	select {
	case err := <-oldDone:
		t.Fatalf("old transfer finished before release: %v", err)
	default:
	}

	var newOp AccessOp
	_, err := m.Transfer(context.Background(), TransferRequest{
		Device: device,
		IOVA:   0,
		Length: PageSize,
		Access: Read,
	}, func(op AccessOp) error {
		newOp = op
		return nil
	})
	if err != nil {
		t.Fatalf("new Transfer: %v", err)
	}
	if newOp.Generation != 2 || newOp.PhysPage != 101 {
		t.Fatalf("new callback = %+v", newOp)
	}

	close(release)
	if err := waitValue(t, oldDone, "old transfer completion"); err != nil {
		t.Fatalf("old Transfer: %v", err)
	}
	if oldOp.PhysPage != 100 || oldOp.Generation != 1 {
		t.Fatalf("old lease was mutated: %+v", oldOp)
	}
}

func TestRemapDuringTransferDoesNotTearLaterChunks(t *testing.T) {
	m, _, device := newTestIOMMU(t)
	if err := m.MapRange(device, 0, 2*PageSize, 100, Read); err != nil {
		t.Fatal(err)
	}

	firstEntered := make(chan struct{})
	release := make(chan struct{})
	type observation struct {
		page PhysPage
		gen  Generation
	}
	observations := make(chan observation, 2)
	transferDone := make(chan error, 1)
	go func() {
		_, err := m.Transfer(context.Background(), TransferRequest{
			Device: device,
			IOVA:   0,
			Length: 2 * PageSize,
			Access: Read,
		}, func(op AccessOp) error {
			observations <- observation{page: op.PhysPage, gen: op.Generation}
			if op.IOVA == 0 {
				close(firstEntered)
				<-release
			}
			return nil
		})
		transferDone <- err
	}()

	waitClosed(t, firstEntered, "first page callback")
	if err := m.Remap(device, PageSize, 105, Read); err != nil {
		t.Fatalf("Remap second page: %v", err)
	}
	close(release)
	if err := waitValue(t, transferDone, "transfer completion"); err != nil {
		t.Fatalf("Transfer: %v", err)
	}

	first := waitValue(t, observations, "first observation")
	second := waitValue(t, observations, "second observation")
	if first.page != 100 || first.gen != 1 {
		t.Fatalf("first observation = %+v", first)
	}
	if second.page != 101 || second.gen != 1 {
		t.Fatalf("second observation = %+v, remap tore the leased transfer", second)
	}
}

func TestUnmapWaitsForLeaseAndRejectsNewTransfers(t *testing.T) {
	m, _, device := newTestIOMMU(t)
	if err := m.MapRange(device, 0, 2*PageSize, 100, Read); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	transferDone := make(chan error, 1)
	go func() {
		_, err := m.Transfer(context.Background(), TransferRequest{
			Device: device,
			IOVA:   0,
			Length: 2 * PageSize,
			Access: Read,
		}, func(op AccessOp) error {
			if op.IOVA == 0 {
				close(entered)
				<-release
			}
			return nil
		})
		transferDone <- err
	}()
	waitClosed(t, entered, "transfer callback")

	unmapStarted := make(chan struct{})
	unmapDone := make(chan error, 1)
	go func() {
		close(unmapStarted)
		unmapDone <- m.Unmap(device, 0, 2*PageSize)
	}()
	waitClosed(t, unmapStarted, "unmap start")
	waitUntil(t, "mapping invalidation", func() bool {
		_, ok := m.Lookup(device, 0)
		return !ok
	})
	stats, _ := m.Stats(device)
	if stats.ActiveMappings != 0 || stats.Leases != 2 {
		t.Fatalf("stats during revoke = %+v, want 0 mappings and 2 leases", stats)
	}

	select {
	case err := <-unmapDone:
		t.Fatalf("Unmap returned before lease release: %v", err)
	default:
	}

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
	if err != ErrNotMapped || calls != 0 {
		t.Fatalf("new transfer while revoked: err=%v calls=%d", err, calls)
	}

	close(release)
	if err := waitValue(t, transferDone, "leased transfer completion"); err != nil {
		t.Fatalf("leased Transfer: %v", err)
	}
	if err := waitValue(t, unmapDone, "unmap completion"); err != nil {
		t.Fatalf("Unmap: %v", err)
	}
	stats, _ = m.Stats(device)
	if stats.ActiveMappings != 0 || stats.Leases != 0 {
		t.Fatalf("stats after revoke = %+v", stats)
	}
}

func TestUnmapWaitsForPreRemapLease(t *testing.T) {
	m, _, device := newTestIOMMU(t)
	if err := m.Map(device, 0, 100, Read); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	transferDone := make(chan error, 1)
	go func() {
		_, err := m.Transfer(context.Background(), TransferRequest{
			Device: device,
			IOVA:   0,
			Length: PageSize,
			Access: Read,
		}, func(AccessOp) error {
			close(entered)
			<-release
			return nil
		})
		transferDone <- err
	}()
	waitClosed(t, entered, "old-generation callback")

	if err := m.Remap(device, 0, 101, Read); err != nil {
		t.Fatalf("Remap: %v", err)
	}
	unmapStarted := make(chan struct{})
	unmapDone := make(chan error, 1)
	go func() {
		close(unmapStarted)
		unmapDone <- m.Unmap(device, 0, PageSize)
	}()
	waitClosed(t, unmapStarted, "unmap start")
	waitUntil(t, "remapped page removal", func() bool {
		_, ok := m.Lookup(device, 0)
		return !ok
	})

	select {
	case err := <-unmapDone:
		t.Fatalf("Unmap ignored pre-remap lease: %v", err)
	default:
	}

	close(release)
	if err := waitValue(t, transferDone, "old-generation transfer completion"); err != nil {
		t.Fatalf("old Transfer: %v", err)
	}
	if err := waitValue(t, unmapDone, "unmap completion"); err != nil {
		t.Fatalf("Unmap: %v", err)
	}
}

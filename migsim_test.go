package migsim

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// barrier pauses the migrator between verified copy and generational
// switch, letting the test inject concurrent traffic at that exact point.
type barrier struct {
	arrived chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBarrier() *barrier {
	return &barrier{arrived: make(chan struct{}), release: make(chan struct{})}
}

func (b *barrier) wait() {
	b.once.Do(func() { close(b.arrived) })
	<-b.release
}

func currentGen(b *Block) *Generation {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cur
}

func genByID(b *Block, id uint64) *Generation {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, g := range b.gens {
		if g.ID == id {
			return g
		}
	}
	return nil
}

// TestSwitchConcurrentRefs holds the migrator at the pre-switch barrier
// while new references arrive, old receipts are replayed, a write is
// attempted and a read handle pins the old generation.
func TestSwitchConcurrentRefs(t *testing.T) {
	oldVol := NewVolume("old")
	newVol := NewVolume("new")
	led := NewLedger()
	blk := NewBlock("blk-1", []byte("shared-payload"), oldVol)

	var receipts []Receipt
	for i := 0; i < 3; i++ {
		receipts = append(receipts, led.AddRef(fmt.Sprintf("obj-%d", i), "blk-1"))
	}

	bar := newBarrier()
	mig := &Migrator{BeforeSwitch: bar.wait}
	mg, err := mig.Start(blk, newVol)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	<-bar.arrived

	// Content must be immutable while the copy is staged.
	if err := blk.Write([]byte("tamper")); !errors.Is(err, ErrFrozen) {
		t.Fatalf("write during copy: got %v, want ErrFrozen", err)
	}

	// New references land while the copy is staged.
	var wg sync.WaitGroup
	for i := 3; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			led.AddRef(fmt.Sprintf("obj-%d", i), "blk-1")
		}(i)
	}
	// Replaying old receipts before the switch must be a no-op.
	for _, r := range receipts {
		if led.Replay(r) {
			t.Fatalf("receipt %d applied twice before switch", r.Seq)
		}
	}
	// A read opened now pins the old generation.
	h, err := blk.OpenRead()
	if err != nil {
		t.Fatalf("open read: %v", err)
	}
	wg.Wait()

	close(bar.release)
	if err := mg.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}

	// New references resolve to the new volume after the switch.
	if got := currentGen(blk).Vol; got != newVol {
		t.Fatalf("current generation on %q, want new volume", got.Name())
	}
	// References added after the switch resolve there too.
	led.AddRef("obj-8", "blk-1")

	// The pinned handle keeps the old replica alive and readable.
	old := genByID(blk, 1)
	if !oldVol.Has(old.Addr) {
		t.Fatal("old replica reclaimed while a handle was pinned")
	}
	data, err := h.Read()
	if err != nil {
		t.Fatalf("read pinned handle: %v", err)
	}
	if string(data) != "shared-payload" {
		t.Fatalf("pinned read = %q, want original content", data)
	}

	// Releasing the last handle reclaims the old replica.
	h.Close()
	if oldVol.Has(old.Addr) {
		t.Fatal("old replica not reclaimed after last handle closed")
	}

	// Replaying old receipts after the switch is still a no-op.
	before := led.Snapshot()
	for _, r := range receipts {
		if led.Replay(r) {
			t.Fatalf("receipt %d applied twice after switch", r.Seq)
		}
	}
	if !reflect.DeepEqual(before, led.Snapshot()) {
		t.Fatal("receipt replay mutated the ledger")
	}

	// Replaying a stale physical delete receipt is a safe no-op and must
	// not touch the new generation's replica.
	oldVol.Delete(old.Addr)
	if !newVol.Has(currentGen(blk).Addr) {
		t.Fatal("stale delete receipt affected the new replica")
	}

	if err := Verify(led, blk); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// TestCancelMigration cancels at the pre-switch barrier: no switch, no
// leaked staged replica, block unfrozen, old generation still serving.
func TestCancelMigration(t *testing.T) {
	oldVol := NewVolume("old")
	newVol := NewVolume("new")
	led := NewLedger()
	blk := NewBlock("blk-c", []byte("cancel-me"), oldVol)
	led.AddRef("obj-x", "blk-c")

	bar := newBarrier()
	mig := &Migrator{BeforeSwitch: bar.wait}
	mg, err := mig.Start(blk, newVol)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// A second migration on the same block is rejected while frozen.
	if _, err := mig.Start(blk, newVol); !errors.Is(err, ErrMigrationBusy) {
		t.Fatalf("second start: got %v, want ErrMigrationBusy", err)
	}

	<-bar.arrived
	if err := mg.Cancel(); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	close(bar.release)
	if err := mg.Wait(); !errors.Is(err, ErrMigrationCancelled) {
		t.Fatalf("wait: got %v, want ErrMigrationCancelled", err)
	}

	if got := currentGen(blk).Vol; got != oldVol {
		t.Fatalf("current generation moved to %q despite cancel", got.Name())
	}
	if newVol.Count() != 0 {
		t.Fatal("staged replica leaked after cancel")
	}
	if blk.Frozen() {
		t.Fatal("block still frozen after cancel")
	}
	// Content is mutable again once the copy is gone.
	if err := blk.Write([]byte("after-cancel")); err != nil {
		t.Fatalf("write after cancel: %v", err)
	}
	// A settled migration cannot be cancelled again.
	if err := mg.Cancel(); !errors.Is(err, ErrMigrationFinished) {
		t.Fatalf("second cancel: got %v, want ErrMigrationFinished", err)
	}

	if err := Verify(led, blk); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// TestDigestFailureBlocksSwitch corrupts the staged copy; the migration
// must abort without switching and without leaking the staged replica.
func TestDigestFailureBlocksSwitch(t *testing.T) {
	oldVol := NewVolume("old")
	newVol := NewVolume("new")
	led := NewLedger()
	blk := NewBlock("blk-d", []byte("integrity"), oldVol)
	led.AddRef("obj-y", "blk-d")

	newVol.OnPut = func(addr string) { newVol.Corrupt(addr) }
	if _, err := (&Migrator{}).Start(blk, newVol); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("start: got %v, want ErrDigestMismatch", err)
	}

	if got := currentGen(blk).Vol; got != oldVol {
		t.Fatalf("switch happened despite digest failure, now on %q", got.Name())
	}
	if newVol.Count() != 0 {
		t.Fatal("corrupt staged replica leaked")
	}
	if blk.Frozen() {
		t.Fatal("block still frozen after digest failure")
	}
	if err := blk.Write([]byte("recovered")); err != nil {
		t.Fatalf("write after abort: %v", err)
	}

	if err := Verify(led, blk); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// TestReadHandlePinnedToReplica checks that a handle opened before the
// switch keeps reading the old replica, and that reclamation waits for it.
func TestReadHandlePinnedToReplica(t *testing.T) {
	oldVol := NewVolume("old")
	newVol := NewVolume("new")
	led := NewLedger()
	blk := NewBlock("blk-h", []byte("pin-me"), oldVol)
	led.AddRef("obj-z", "blk-h")

	h1, err := blk.OpenRead()
	if err != nil {
		t.Fatalf("open read: %v", err)
	}
	mg, err := (&Migrator{}).Start(blk, newVol)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := mg.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}

	old := genByID(blk, 1)
	if !oldVol.Has(old.Addr) {
		t.Fatal("old replica reclaimed while pinned")
	}
	data, err := h1.Read()
	if err != nil {
		t.Fatalf("read pinned handle: %v", err)
	}
	if string(data) != "pin-me" {
		t.Fatalf("pinned read = %q, want original content", data)
	}

	// Reads opened after the switch pin the new generation.
	h2, err := blk.OpenRead()
	if err != nil {
		t.Fatalf("open read after switch: %v", err)
	}
	if h2.gen != currentGen(blk) {
		t.Fatal("new read did not pin the new generation")
	}

	h1.Close()
	if oldVol.Has(old.Addr) {
		t.Fatal("old replica not reclaimed after last pinned handle closed")
	}
	h2.Close()

	if err := Verify(led, blk); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// TestConcurrentStress hammers blocks with references, receipt replays and
// pinned reads while migrations (some cancelled) run, then lets the
// independent ledger verify reference/replica consistency.
func TestConcurrentStress(t *testing.T) {
	oldVol := NewVolume("old")
	newVol := NewVolume("new")
	led := NewLedger()

	const nBlocks = 6
	blocks := make([]*Block, nBlocks)
	for i := range blocks {
		blocks[i] = NewBlock(fmt.Sprintf("blk-%d", i), []byte(fmt.Sprintf("payload-%d", i)), oldVol)
	}

	var recMu sync.Mutex
	var receipts []Receipt
	record := func(r Receipt) {
		recMu.Lock()
		receipts = append(receipts, r)
		recMu.Unlock()
	}

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			held := make(map[[2]string]int)
			for i := 0; i < 300; i++ {
				b := blocks[rng.Intn(nBlocks)]
				obj := fmt.Sprintf("obj-%d-%d", seed, rng.Intn(4))
				key := [2]string{obj, b.id}
				switch rng.Intn(10) {
				case 0, 1: // pinned read
					if h, err := b.OpenRead(); err == nil {
						h.Read()
						h.Close()
					}
				case 2: // delete a reference we hold
					if held[key] > 0 {
						if r, err := led.DelRef(obj, b.id); err == nil {
							held[key]--
							record(r)
						}
					}
				case 3: // replay an old receipt
					recMu.Lock()
					n := len(receipts)
					var r Receipt
					if n > 0 {
						r = receipts[rng.Intn(n)]
					}
					recMu.Unlock()
					if n > 0 {
						led.Replay(r)
					}
				default: // add a reference
					r := led.AddRef(obj, b.id)
					held[key]++
					record(r)
				}
			}
		}(int64(w + 1))
	}

	// Two rounds of migrations per block, odd ones cancelled.
	for round := 0; round < 2; round++ {
		for bi, b := range blocks {
			wg.Add(1)
			go func(round, bi int, b *Block) {
				defer wg.Done()
				mg, err := (&Migrator{}).Start(b, newVol)
				if err != nil {
					return // busy with the other round's migration
				}
				if (round+bi)%2 == 1 {
					mg.Cancel()
				}
				mg.Wait()
			}(round, bi, b)
		}
	}
	wg.Wait()

	if err := Verify(led, blocks...); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

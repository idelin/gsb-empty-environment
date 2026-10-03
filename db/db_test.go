package db

import (
	"sync"
	"testing"

	"aries/recovery"
	"aries/storage"
	"aries/wal"
)

func newTestDB() (*DB, *wal.MemStore, *storage.MemDisk) {
	store := wal.NewMemStore()
	disk := storage.NewMemDisk()
	pool := storage.NewBufferPool(disk)
	return New(wal.New(store, 1), pool), store, disk
}

func TestRuntimeAbortWritesCLRs(t *testing.T) {
	d, store, _ := newTestDB()
	txn := d.Begin()
	if err := d.Update(txn, 1, 10); err != nil {
		t.Fatal(err)
	}
	if err := d.Update(txn, 1, 20); err != nil {
		t.Fatal(err)
	}
	if err := d.Abort(txn); err != nil {
		t.Fatal(err)
	}
	if pg := d.Pool.Fetch(1); pg.Value != 0 {
		t.Fatalf("page value = %d after abort, want 0", pg.Value)
	}
	d.FlushLog()
	recs, err := wal.New(store, 1).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	var clrs, ends int
	for _, r := range recs {
		switch r.Type {
		case wal.RecCLR:
			clrs++
		case wal.RecEnd:
			ends++
		}
	}
	if clrs != 2 || ends != 1 {
		t.Fatalf("log has %d CLRs and %d ENDs, want 2 and 1", clrs, ends)
	}
	if d.ActiveTxns() != 0 {
		t.Fatalf("%d active txns leaked", d.ActiveTxns())
	}
}

func TestCommitForcesLog(t *testing.T) {
	d, store, _ := newTestDB()
	txn := d.Begin()
	if err := d.Update(txn, 1, 5); err != nil {
		t.Fatal(err)
	}
	before := store.StableLen()
	if err := d.Commit(txn); err != nil {
		t.Fatal(err)
	}
	if store.StableLen() <= before {
		t.Fatal("commit did not force the log")
	}
}

func TestCheckpointContents(t *testing.T) {
	d, store, _ := newTestDB()
	t1 := d.Begin()
	if err := d.Update(t1, 3, 30); err != nil {
		t.Fatal(err)
	}
	t2 := d.Begin()
	if err := d.Update(t2, 4, 40); err != nil {
		t.Fatal(err)
	}
	if err := d.Commit(t2); err != nil {
		t.Fatal(err)
	}
	d.Checkpoint()
	d.FlushLog()

	recs, err := wal.New(store, 1).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	ckpt := recs[len(recs)-1]
	if ckpt.Type != wal.RecCheckpoint {
		t.Fatalf("last record = %s, want CHECKPOINT", ckpt.Type)
	}
	if len(ckpt.Txns) != 1 || ckpt.Txns[0].TxnID != t1 {
		t.Fatalf("checkpoint txns = %+v, want only txn %d", ckpt.Txns, t1)
	}
	// Pages 3 and 4 are both dirty: committing a txn does not flush pages.
	if len(ckpt.Dirty) != 2 || ckpt.Dirty[0].PageID != 3 || ckpt.Dirty[1].PageID != 4 {
		t.Fatalf("checkpoint DPT = %+v, want pages 3 and 4", ckpt.Dirty)
	}
}

func TestUnknownAndInactiveTxnErrors(t *testing.T) {
	d, _, _ := newTestDB()
	if err := d.Update(999, 1, 1); err == nil {
		t.Fatal("update on unknown txn succeeded")
	}
	txn := d.Begin()
	if err := d.Commit(txn); err != nil {
		t.Fatal(err)
	}
	if err := d.Update(txn, 1, 1); err == nil {
		t.Fatal("update on committed txn succeeded")
	}
	if err := d.Commit(txn); err == nil {
		t.Fatal("double commit succeeded")
	}
}

// TestConcurrentWorkload hammers the DB from many goroutines, then
// crashes and recovers, verifying the final state against the
// independent committed-state model. Run with -race. Each goroutine
// owns a disjoint set of pages, mirroring the strict-2PL isolation that
// ARIES undo relies on.
func TestConcurrentWorkload(t *testing.T) {
	d, store, disk := newTestDB()
	pool := d.Pool
	const workers = 8
	const txnsPerWorker = 50
	var wg sync.WaitGroup
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < txnsPerWorker; i++ {
				txn := d.Begin()
				page := uint64(g*txnsPerWorker + i) // disjoint per goroutine
				if err := d.Update(txn, page, int64(g*1000+i)); err != nil {
					t.Error(err)
					return
				}
				if (g+i)%3 == 0 {
					if err := d.Abort(txn); err != nil {
						t.Error(err)
					}
				} else {
					if err := d.Commit(txn); err != nil {
						t.Error(err)
					}
				}
			}
		}(g)
	}
	wg.Wait()
	d.Checkpoint()
	d.FlushLog()
	d.Crash()

	res, _, err := recovery.Recover(store, pool, disk, nil)
	if err != nil {
		t.Fatal(err)
	}
	pool.FlushAll()
	if len(res.TxnTable) != 0 {
		t.Fatalf("txn table not empty after recovery: %v", res.TxnTable)
	}
	recs, err := wal.New(store, 1).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	model := recovery.ExpectedCommittedState(recs)
	snap := disk.Snapshot()
	for page, want := range model {
		if got := snap[page].Value; got != want {
			t.Errorf("page %d = %d, model wants %d", page, got, want)
		}
	}
	for page, img := range snap {
		if _, ok := model[page]; !ok && img.Value != 0 {
			t.Errorf("page %d = %d on disk but absent from model", page, img.Value)
		}
	}
	if dirty := pool.DirtyPages(); len(dirty) != 0 {
		t.Errorf("dirty pages leaked: %v", dirty)
	}
	if d.ActiveTxns() != 0 {
		t.Errorf("%d active txns leaked", d.ActiveTxns())
	}
}

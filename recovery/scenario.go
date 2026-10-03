package recovery

import (
	"fmt"
	"strings"

	"aries/storage"
)

// Expectation describes the durable state a correct restart recovery
// must produce for the reference crash scenario.
type Expectation struct {
	Pages   map[int]string // pageID -> expected payload after recovery
	Winners []int          // committed transactions
	Losers  []int          // transactions rolled back
}

// BuildCrashScenario runs the reference workload against sys:
//
//	t1..t5 begin; t1 and t2 update pages; a checkpoint is taken and
//	the buffer pool is flushed; afterwards t1 and t2 commit while
//	t3, t4 and t5 remain in flight (one of t3's updates overwrites a
//	page written by committed t1, and another dirties t1's page P3).
//	The log is flushed; the caller simulates the crash by discarding
//	the buffer pool.
//
// It returns the expected post-recovery state and the checkpoint LSN.
func BuildCrashScenario(sys *System, disk *storage.DiskStore) (*Expectation, uint64, error) {
	for page := 1; page <= 5; page++ {
		if err := disk.WritePage(page, 0, []byte(fmt.Sprintf("p%d-v0", page))); err != nil {
			return nil, 0, err
		}
	}
	disk.Sync()

	for txn := 1; txn <= 5; txn++ {
		if _, err := sys.Begin(txn); err != nil {
			return nil, 0, err
		}
	}
	if _, err := sys.Update(1, 1, []byte("p1-t1")); err != nil {
		return nil, 0, err
	}
	if _, err := sys.Update(2, 2, []byte("p2-t2a")); err != nil {
		return nil, 0, err
	}
	chkLSN, err := sys.Checkpoint()
	if err != nil {
		return nil, 0, err
	}
	// Checkpoint flushes the pool: pages 1 and 2 are durable up to
	// the pre-checkpoint updates, everything later is memory-only.
	if err := sys.Pool.FlushAll(); err != nil {
		return nil, 0, err
	}

	if _, err := sys.Update(3, 1, []byte("p1-t3")); err != nil { // overwrites committed t1 page
		return nil, 0, err
	}
	if _, err := sys.Update(1, 3, []byte("p3-t1")); err != nil {
		return nil, 0, err
	}
	if _, err := sys.Commit(1); err != nil {
		return nil, 0, err
	}
	if _, err := sys.Update(2, 2, []byte("p2-t2b")); err != nil {
		return nil, 0, err
	}
	if _, err := sys.Commit(2); err != nil {
		return nil, 0, err
	}
	if _, err := sys.Update(4, 4, []byte("p4-t4")); err != nil {
		return nil, 0, err
	}
	if _, err := sys.Update(5, 5, []byte("p5-t5")); err != nil {
		return nil, 0, err
	}
	if _, err := sys.Update(3, 3, []byte("p3-t3")); err != nil { // dirties committed t1 page
		return nil, 0, err
	}
	sys.Log.Flush(sys.Log.LastLSN())

	exp := &Expectation{
		Pages: map[int]string{
			1: "p1-t1",  // t3's overwrite must be undone
			2: "p2-t2b", // t2's committed post-checkpoint update must be redone
			3: "p3-t1",  // t3's dirtying of t1's page must be undone
			4: "p4-v0",  // t4 never committed
			5: "p5-v0",  // t5 never committed
		},
		Winners: []int{1, 2},
		Losers:  []int{3, 4, 5},
	}
	return exp, chkLSN, nil
}

// VerifyExpectation compares the durable page contents against exp.
func VerifyExpectation(disk *storage.DiskStore, exp *Expectation) error {
	for page, want := range exp.Pages {
		_, data, err := disk.ReadPage(page)
		if err != nil {
			return err
		}
		if got := string(data); got != want {
			return fmt.Errorf("page %d: got %q, want %q", page, got, want)
		}
	}
	return nil
}

// PageContents returns the durable payload of pageID as a string.
func PageContents(disk *storage.DiskStore, pageID int) (string, error) {
	_, data, err := disk.ReadPage(pageID)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\x00"), nil
}

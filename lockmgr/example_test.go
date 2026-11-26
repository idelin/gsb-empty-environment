package lockmgr_test

import (
	"errors"
	"fmt"

	"prescreen.local/ps-a9cfa18e01/lockmgr"
)

func Example() {
	m := lockmgr.NewManager()

	db := lockmgr.NewDB("shop")
	table := lockmgr.NewTable("shop", "orders")
	page := lockmgr.NewPage("shop", "orders", "p1")
	row := lockmgr.NewRow("shop", "orders", "p1", "order-1")

	t1 := m.Begin(1)
	t2 := m.Begin(2)

	// Top-down intention locks before touching the row.
	for _, txn := range []*lockmgr.Transaction{t1, t2} {
		if err := m.Lock(txn, db, lockmgr.IS); err != nil {
			panic(err)
		}
		if err := m.Lock(txn, table, lockmgr.IS); err != nil {
			panic(err)
		}
		if err := m.Lock(txn, page, lockmgr.IS); err != nil {
			panic(err)
		}
	}

	if err := m.Lock(t1, row, lockmgr.S); err != nil {
		panic(err)
	}
	if err := m.Lock(t2, row, lockmgr.S); err != nil {
		panic(err)
	}

	// Concurrent S -> X upgrades form an immediate upgrade deadlock; the
	// manager aborts the later-started transaction and completes the other.
	errCh := make(chan error, 2)
	go func() { errCh <- m.Lock(t1, row, lockmgr.X) }()
	go func() { errCh <- m.Lock(t2, row, lockmgr.X) }()

	victim := 0
	for range 2 {
		if err := <-errCh; errors.Is(err, lockmgr.ErrDeadlockRollback) {
			victim++
		}
	}

	for _, resolution := range m.Resolutions() {
		fmt.Printf("victim=%d cycle-len=%d\n", resolution.Victim, len(resolution.Cycle))
	}
	fmt.Printf("deadlocked=%d t1-mode=%s\n", victim, m.HeldLock(t1, row))

	// Output:
	// victim=2 cycle-len=2
	// deadlocked=1 t1-mode=X
}

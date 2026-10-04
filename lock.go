package waldb

import "sync"

// lockTable implements strict two-phase locking with exclusive page
// locks: locks are acquired on first access and held until the
// transaction ends (commit or abort), which prevents dirty writes —
// no two transactions can ever modify the same page concurrently.
type lockTable struct {
	mu    sync.Mutex
	cond  *sync.Cond
	owner map[uint32]uint64 // pageID -> txnID
}

func newLockTable() *lockTable {
	lt := &lockTable{owner: map[uint32]uint64{}}
	lt.cond = sync.NewCond(&lt.mu)
	return lt
}

// lock blocks until the exclusive page lock can be granted to txn.
func (lt *lockTable) lock(pageID uint32, txn uint64) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	for {
		o, held := lt.owner[pageID]
		if !held || o == txn {
			lt.owner[pageID] = txn
			return
		}
		lt.cond.Wait()
	}
}

// releaseAll drops every lock held by txn (called at commit/abort).
func (lt *lockTable) releaseAll(txn uint64) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	for pg, o := range lt.owner {
		if o == txn {
			delete(lt.owner, pg)
		}
	}
	lt.cond.Broadcast()
}

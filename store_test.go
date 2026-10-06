package seqstore

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"time"
)

var tables = []string{"east", "west"}

func tableOf(id int) string { return tables[id%len(tables)] }

func allKeys(accounts int) map[string][]string {
	keys := map[string][]string{tables[0]: {}, tables[1]: {}}
	for i := 0; i < accounts; i++ {
		t := tableOf(i)
		keys[t] = append(keys[t], acct(i))
	}
	return keys
}

func acct(i int) string { return fmt.Sprintf("acct-%d", i) }

func sumRows(rows map[string]map[string]int64) int64 {
	var total int64
	for _, row := range rows {
		for _, v := range row {
			total += v
		}
	}
	return total
}

// ledger commits a genesis transaction and a series of random
// cross-table transfers, returning the store, the per-sequence
// reference balances (history[seq-1][account] = balance), and the
// invariant total.
func ledger(t *testing.T, accounts, txns int, seed int64) (*Store, []map[string]int64, int64) {
	t.Helper()
	p := NewPrimary()
	s := NewStore(p, tables...)
	rng := rand.New(rand.NewSource(seed))

	const initial = int64(100)
	genesis := map[string]map[string]int64{tables[0]: {}, tables[1]: {}}
	balance := make(map[string]int64, accounts)
	for i := 0; i < accounts; i++ {
		genesis[tableOf(i)][acct(i)] = initial
		balance[acct(i)] = initial
	}
	p.Commit(genesis)
	history := []map[string]int64{copyBalances(balance)}

	for len(history) <= txns {
		from := rng.Intn(accounts)
		to := rng.Intn(accounts)
		if from == to {
			continue
		}
		amt := int64(1 + rng.Intn(50))
		if balance[acct(from)] < amt {
			continue
		}
		balance[acct(from)] -= amt
		balance[acct(to)] += amt
		writes := map[string]map[string]int64{}
		for _, id := range []int{from, to} {
			if writes[tableOf(id)] == nil {
				writes[tableOf(id)] = map[string]int64{}
			}
			writes[tableOf(id)][acct(id)] = balance[acct(id)]
		}
		p.Commit(writes)
		history = append(history, copyBalances(balance))
	}
	return s, history, int64(accounts) * initial
}

func copyBalances(b map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(b))
	for k, v := range b {
		out[k] = v
	}
	return out
}

// deliver sends one log entry to every table replica, modelling a
// replication stream that may arrive out of order.
func deliver(s *Store, e LogEntry) {
	for _, table := range tables {
		s.Replica(table).Receive(e)
	}
}

// Out-of-order replication must never produce a read that mixes part
// of a transaction: at every delivery step, a read at the latest
// committed sequence either fails with WaitError or reflects complete
// transactions only, and the snapshot read always conserves the total.
func TestCrossTableTransferConservation(t *testing.T) {
	const accounts, txns = 10, 300
	s, history, total := ledger(t, accounts, txns, 42)
	keys := allKeys(accounts)
	head := s.Primary().Head()

	rng := rand.New(rand.NewSource(7))
	entries := s.Primary().Entries()
	for _, idx := range rng.Perm(len(entries)) {
		deliver(s, entries[idx])

		// A read at the head must be all-or-nothing.
		rows, err := s.ReadAt(head, keys)
		if err != nil {
			var we *WaitError
			if !errors.As(err, &we) {
				t.Fatalf("ReadAt(head) error = %v, want *WaitError", err)
			}
			if we.Want != head || we.Have >= we.Want {
				t.Fatalf("WaitError = %+v, want Have < Want == %d", we, head)
			}
		} else if got := sumRows(rows); got != total {
			t.Fatalf("ReadAt(head) total = %d, want %d (partial transaction leaked)", got, total)
		}

		// A snapshot read must match the reference history exactly.
		rows, snap := s.Read(keys)
		if snap == 0 {
			if got := sumRows(rows); got != 0 {
				t.Fatalf("snapshot 0 total = %d, want 0", got)
			}
			continue
		}
		for table, ks := range keys {
			for _, k := range ks {
				if got, want := rows[table][k], history[snap-1][k]; got != want {
					t.Fatalf("snapshot %d: %s/%s = %d, want %d", snap, table, k, got, want)
				}
			}
		}
		if got := sumRows(rows); got != total {
			t.Fatalf("snapshot %d total = %d, want conserved %d", snap, got, total)
		}
	}

	// Fully caught up: the head read must succeed and conserve.
	rows, err := s.ReadAt(head, keys)
	if err != nil {
		t.Fatalf("ReadAt(head) after full replication: %v", err)
	}
	if got := sumRows(rows); got != total {
		t.Fatalf("final total = %d, want %d", got, total)
	}
}

// A transfer committed across two tables but replicated to only one of
// them must surface as WaitError, not as a half-visible transfer.
func TestLaggingTableNeverMixesPartialTransaction(t *testing.T) {
	p := NewPrimary()
	s := NewStore(p, tables...)
	p.Commit(map[string]map[string]int64{
		tables[0]: {"x": 100},
		tables[1]: {"y": 100},
	})
	transfer := p.Commit(map[string]map[string]int64{
		tables[0]: {"x": 60},
		tables[1]: {"y": 140},
	})
	keys := map[string][]string{tables[0]: {"x"}, tables[1]: {"y"}}

	entries := p.Entries()
	deliver(s, entries[0])                   // genesis reaches both tables
	s.Replica(tables[0]).Receive(entries[1]) // transfer reaches only one

	_, err := s.ReadAt(transfer.Seq, keys)
	var we *WaitError
	if !errors.As(err, &we) {
		t.Fatalf("ReadAt error = %v, want *WaitError", err)
	}
	if we.Table != tables[1] || we.Have != 1 || we.Want != transfer.Seq {
		t.Fatalf("WaitError = %+v, want table %q Have 1 Want %d", we, tables[1], transfer.Seq)
	}

	// The non-blocking read falls back to the last consistent snapshot.
	rows, snap := s.Read(keys)
	if snap != 1 || rows[tables[0]]["x"] != 100 || rows[tables[1]]["y"] != 100 {
		t.Fatalf("Read = %v @%d, want pre-transfer state @1", rows, snap)
	}

	// Once the lagging table catches up, the full transfer is visible.
	s.Replica(tables[1]).Receive(entries[1])
	rows, err = s.ReadAt(transfer.Seq, keys)
	if err != nil {
		t.Fatalf("ReadAt after catch-up: %v", err)
	}
	if rows[tables[0]]["x"] != 60 || rows[tables[1]]["y"] != 140 {
		t.Fatalf("ReadAt after catch-up = %v, want x=60 y=140", rows)
	}
}

// After fully replicating out of order, historical reads at any past
// sequence number must reproduce the state as of that commit.
func TestHistoricalQueries(t *testing.T) {
	const accounts, txns = 8, 200
	s, history, _ := ledger(t, accounts, txns, 99)
	keys := allKeys(accounts)
	head := s.Primary().Head()

	rng := rand.New(rand.NewSource(3))
	entries := s.Primary().Entries()
	for _, idx := range rng.Perm(len(entries)) {
		deliver(s, entries[idx])
	}

	for _, seq := range rng.Perm(int(head))[:50] {
		asOf := uint64(seq) + 1
		rows, err := s.ReadAt(asOf, keys)
		if err != nil {
			t.Fatalf("ReadAt(%d): %v", asOf, err)
		}
		for table, ks := range keys {
			for _, k := range ks {
				if got, want := rows[table][k], history[asOf-1][k]; got != want {
					t.Fatalf("seq %d: %s/%s = %d, want %d", asOf, table, k, got, want)
				}
			}
		}
	}

	// A sequence beyond every watermark must ask the caller to wait.
	_, err := s.ReadAt(head+1, keys)
	var we *WaitError
	if !errors.As(err, &we) || we.Want != head+1 {
		t.Fatalf("ReadAt(head+1) error = %v, want WaitError Want=%d", err, head+1)
	}
}

func TestWaitForBlocksUntilReplicaCatchesUp(t *testing.T) {
	p := NewPrimary()
	s := NewStore(p, tables...)
	for i := 0; i < 5; i++ {
		p.Commit(map[string]map[string]int64{tables[0]: {"x": int64(i)}})
	}
	entries := p.Entries()
	head := p.Head()

	go func() {
		time.Sleep(20 * time.Millisecond)
		for _, e := range entries {
			deliver(s, e)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.WaitFor(ctx, tables, head); err != nil {
		t.Fatalf("WaitFor: %v", err)
	}

	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := s.WaitFor(ctx, tables, head+1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitFor beyond head = %v, want DeadlineExceeded", err)
	}
}

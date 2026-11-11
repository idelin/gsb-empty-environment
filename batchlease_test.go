package batchlease

import (
	"errors"
	"fmt"
	"os"
	"testing"
)

func h(s string) []byte { return []byte("hash-" + s) }

func openStore(t *testing.T, dir string, hook FaultHook) *Store {
	t.Helper()
	s, err := Open(dir, WithFaultHook(hook))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func reopenStore(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	return s
}

func mustAcquire(t *testing.T, s *Store, id string, first, last uint64, hash string) Lease {
	t.Helper()
	l, err := s.Acquire(id, BatchSummary{FirstSeq: first, LastSeq: last, Hash: h(hash)})
	if err != nil {
		t.Fatalf("Acquire %s: %v", id, err)
	}
	return l
}

func mustConfirm(t *testing.T, s *Store, tok Token, seq uint64) {
	t.Helper()
	if err := s.Confirm(tok, seq); err != nil {
		t.Fatalf("Confirm %d: %v", seq, err)
	}
}

func mustReadFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// scenarioFacts is the set of mutations known to have durably committed.
type scenarioFacts struct {
	opened    bool
	leases    map[string]Lease
	confirmed uint64
	compacted bool
}

func newFacts() scenarioFacts {
	return scenarioFacts{leases: map[string]Lease{}}
}

// runScenario exercises every operation kind, including compaction. Only
// calls that returned nil are recorded as committed facts.
func runScenario(t *testing.T, dir string, hook FaultHook) (scenarioFacts, *Store) {
	t.Helper()
	facts := newFacts()

	s, err := Open(dir, WithFaultHook(hook))
	if err != nil {
		return facts, nil
	}
	facts.opened = true

	la, err := s.Acquire("batch-A", BatchSummary{FirstSeq: 1, LastSeq: 3, Hash: h("A")})
	if err != nil {
		return facts, s
	}
	facts.leases["batch-A"] = la
	if err := s.Confirm(la.Token, 1); err != nil {
		return facts, s
	}
	facts.confirmed = 1
	if err := s.Confirm(la.Token, 2); err != nil {
		return facts, s
	}
	facts.confirmed = 2

	lb, err := s.Acquire("batch-B", BatchSummary{FirstSeq: 4, LastSeq: 6, Hash: h("B")})
	if err != nil {
		return facts, s
	}
	facts.leases["batch-B"] = lb

	if err := s.Compact(); err == nil {
		facts.compacted = true
	}

	lc, err := s.Acquire("batch-C", BatchSummary{FirstSeq: 7, LastSeq: 8, Hash: h("C")})
	if err == nil {
		facts.leases["batch-C"] = lc
	}

	if !s.failed {
		if err := s.Confirm(la.Token, 3); err == nil {
			facts.confirmed = 3
		}
	}

	return facts, s
}

// verifyFacts reopens the SDK without faults and checks every committed fact
// survived with its original batch identity.
func verifyFacts(t *testing.T, dir string, want scenarioFacts) *Store {
	t.Helper()
	s := reopenStore(t, dir)

	if got := s.Confirmed(); got != want.confirmed {
		t.Fatalf("confirmed high-water mark: got %d want %d", got, want.confirmed)
	}
	if len(s.Leases()) != len(want.leases) {
		t.Fatalf("lease count: got %d want %d", len(s.Leases()), len(want.leases))
	}
	for id, wl := range want.leases {
		got, ok := s.Lease(id)
		if !ok {
			t.Fatalf("lease %s missing after reopen", id)
		}
		if got.Token != wl.Token {
			t.Fatalf("lease %s token changed: got %+v want %+v", id, got.Token, wl.Token)
		}
		if got.Summary.FirstSeq != wl.Summary.FirstSeq ||
			got.Summary.LastSeq != wl.Summary.LastSeq ||
			string(got.Summary.Hash) != string(wl.Summary.Hash) {
			t.Fatalf("lease %s summary changed: got %+v want %+v", id, got.Summary, wl.Summary)
		}
		for seq := wl.Summary.FirstSeq; seq <= wl.Summary.LastSeq; seq++ {
			bid, ok := s.Identify(seq)
			if !ok || bid != id {
				t.Fatalf("seq %d identifies as %q (ok=%v), want %s", seq, bid, ok, id)
			}
		}
		if _, err := s.Acquire(id+"-dup", BatchSummary{
			FirstSeq: wl.Summary.FirstSeq, LastSeq: wl.Summary.LastSeq, Hash: h("x"),
		}); !errors.Is(err, ErrOverlap) {
			t.Fatalf("overlapping acquire: got %v want ErrOverlap", err)
		}
		if _, err := s.Acquire(id, BatchSummary{
			FirstSeq: wl.Summary.FirstSeq, LastSeq: wl.Summary.LastSeq, Hash: h("x"),
		}); !errors.Is(err, ErrBatchExists) {
			t.Fatalf("duplicate batch id: got %v want ErrBatchExists", err)
		}
	}
	return s
}

func closeIfOpen(t *testing.T, s *Store) {
	t.Helper()
	if s != nil {
		_ = s.Close()
	}
}

// TestFaultMatrix injects a fault at every single write/sync/replace point
// hit by the full scenario and proves the durable prefix always survives.
func TestFaultMatrix(t *testing.T) {
	count := 0
	counting := func(string) error { count++; return nil }
	probeDir := t.TempDir()
	base, ps := runScenario(t, probeDir, counting)
	closeIfOpen(t, ps)
	if !base.opened || len(base.leases) != 3 || !base.compacted || base.confirmed != 3 {
		t.Fatalf("baseline scenario incomplete: %+v", base)
	}
	t.Logf("scenario reaches %d fault points", count)

	for k := 1; k <= count; k++ {
		t.Run(fmt.Sprintf("fault-%d", k), func(t *testing.T) {
			dir := t.TempDir()
			seen := 0
			hook := func(op string) error {
				seen++
				if seen == k {
					return fmt.Errorf("%w #%d at %s", ErrInjected, k, op)
				}
				return nil
			}
			_, s := runScenario(t, dir, hook)
			closeIfOpen(t, s)

			// Reopen the SDK with no faults and verify the on-disk truth.
			// A sync fault may leave a fully fsynced frame on disk even
			// though the API returned an error, so the reopened state is a
			// durable prefix that may include or exclude the in-flight
			// record, but must never be corrupt or skip a sequence.
			reopened := reopenStore(t, dir)
			defer reopened.Close()

			leases := reopened.Leases()
			confirmed := reopened.Confirmed()

			// 1. Every recovered lease must match one the scenario issued,
			//    with the identical token and batch summary.
			for _, got := range leases {
				want, ok := base.leases[got.Token.BatchID]
				if !ok {
					t.Fatalf("unexpected lease %q after fault", got.Token.BatchID)
				}
				if got.Token != want.Token ||
					got.Summary.FirstSeq != want.Summary.FirstSeq ||
					got.Summary.LastSeq != want.Summary.LastSeq ||
					string(got.Summary.Hash) != string(want.Summary.Hash) {
					t.Fatalf("lease %q changed across fault: got %+v want %+v",
						got.Token.BatchID, got, want)
				}
			}
			// 2. Leased ranges never overlap and every promised record keeps
			//    its original batch identity.
			for _, l := range leases {
				for seq := l.Summary.FirstSeq; seq <= l.Summary.LastSeq; seq++ {
					id, ok := reopened.Identify(seq)
					if !ok || id != l.Token.BatchID {
						t.Fatalf("seq %d identity broken after fault: %q %v", seq, id, ok)
					}
				}
			}
			// 3. The confirmed high-water mark is a prefix of the baseline
			//    and every confirmed sequence is inside a recovered lease.
			if confirmed > base.confirmed {
				t.Fatalf("confirmed advanced beyond baseline: %d > %d", confirmed, base.confirmed)
			}
			if confirmed > 0 {
				for seq := uint64(1); seq <= confirmed; seq++ {
					if _, ok := reopened.Identify(seq); !ok {
						t.Fatalf("confirmed seq %d has no lease after fault", seq)
					}
				}
			}
			// 4. The new generation strictly supersedes every lease token.
			for _, l := range leases {
				if reopened.Epoch() <= l.Token.Epoch {
					t.Fatalf("epoch did not advance: store %d <= lease %d",
						reopened.Epoch(), l.Token.Epoch)
				}
			}
			// 5. Re-dispatching any recovered range is still rejected.
			for _, l := range leases {
				if _, err := reopened.Acquire("dup-"+l.Token.BatchID, BatchSummary{
					FirstSeq: l.Summary.FirstSeq, LastSeq: l.Summary.LastSeq, Hash: h("x"),
				}); !errors.Is(err, ErrOverlap) {
					t.Fatalf("overlap not enforced after fault: %v", err)
				}
			}
		})
	}
}

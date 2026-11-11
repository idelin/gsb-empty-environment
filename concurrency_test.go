package netsec

import (
	"sync"
	"testing"
)

func TestWithdrawBeforeArrival(t *testing.T) {
	s, _ := NewSelector(testKey, 2, 2)

	// Withdrawing an unseen event reports false but records an orphan
	// tombstone so the retraction survives routing across shards.
	if s.Withdraw("late") {
		t.Fatal("withdraw of an event never seen must report false")
	}
	if !s.IsWithdrawn("late") {
		t.Fatal("orphan tombstone must be queryable via IsWithdrawn")
	}

	r := rec("late", "u1", "gA", 1, "p")
	if err := s.Ingest(r); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if !s.IsWithdrawn("late") {
		t.Fatal("late arrival must be auto-retracted by the pending tombstone")
	}

	// A retransmission cannot resurrect the retracted event.
	if err := s.Ingest(r); err != nil {
		t.Fatalf("retransmission: %v", err)
	}

	res := s.Trim()
	if len(res.Kept) != 0 || len(res.Withdrawn) != 1 || res.Withdrawn[0].EventID != "late" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestConcurrentIngestAndWithdraw(t *testing.T) {
	s, _ := NewSelector(testKey, 4, 4)

	const goroutines = 16
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				id := "e" + itoa(g*50+i)
				r := rec(id, "u1", "gA", 1, "payload")
				if err := s.Ingest(r); err != nil {
					t.Errorf("Ingest %s: %v", id, err)
					return
				}
				// Every event is delivered twice; the second call must stay
				// idempotent under concurrency.
				if err := s.Ingest(r); err != nil {
					t.Errorf("retransmit %s: %v", id, err)
					return
				}
			}
			s.Withdraw("e" + itoa(g*50))
			_ = s.Trim()
		}(g)
	}

	wg.Wait()

	res := s.Trim()
	const total = goroutines * 50
	if s.Len() != total {
		t.Fatalf("Len = %d, want %d", s.Len(), total)
	}
	if len(res.Kept)+len(res.Dropped)+len(res.Withdrawn) != total {
		t.Fatalf("accounting: kept=%d dropped=%d withdrawn=%d total=%d",
			len(res.Kept), len(res.Dropped), len(res.Withdrawn), total)
	}
	if len(res.Withdrawn) != goroutines {
		t.Fatalf("withdrawn = %d, want %d", len(res.Withdrawn), goroutines)
	}
	if len(res.Kept) > 4 {
		t.Fatalf("local cap violated: kept %d", len(res.Kept))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}

package applog

import (
	"sync"
	"testing"
)

func TestConcurrentAppendAckReclaim(t *testing.T) {
	dir := t.TempDir()
	l := openTestLog(t, dir, 8, Hooks{})
	const consumers = 4
	for i := 0; i < consumers; i++ {
		id := string(rune('a' + i))
		if err := l.RegisterConsumer(id, 0); err != nil {
			t.Fatal(err)
		}
	}
	const total = 200
	var wg sync.WaitGroup
	wg.Add(consumers + 2)

	go func() {
		defer wg.Done()
		for i := 0; i < total; i++ {
			if _, err := l.Append([]byte("x")); err != nil {
				t.Errorf("append: %v", err)
				return
			}
		}
	}()

	for i := 0; i < consumers; i++ {
		id := string(rune('a' + i))
		go func() {
			defer wg.Done()
			last := uint64(0)
			for last < total {
				hw := l.NextOffset()
				if hw > last {
					if err := l.Ack(id, hw); err != nil {
						t.Errorf("ack %s: %v", id, err)
						return
					}
					last = hw
				}
			}
		}()
	}

	go func() {
		defer wg.Done()
		for {
			if _, err := l.Reclaim(); err != nil {
				t.Errorf("reclaim: %v", err)
				return
			}
			if l.NextOffset() == total {
				// One final reclaim once everyone has caught up.
				for i := 0; i < consumers; i++ {
					id := string(rune('a' + i))
					if p, _ := l.Position(id); p < total {
						return
					}
				}
				_, _ = l.Reclaim()
				return
			}
		}
	}()

	wg.Wait()
	if l.NextOffset() != total {
		t.Fatalf("nextOffset = %d, want %d", l.NextOffset(), total)
	}
	// Every record up to the active segment is still readable; only offsets
	// below FirstOffset were reclaimed.
	for off := l.FirstOffset(); off < total; off++ {
		if _, err := l.Read(off); err != nil {
			t.Fatalf("read %d after concurrent workload: %v", off, err)
		}
	}
}

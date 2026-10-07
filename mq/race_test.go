package mq

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestConcurrentClaimConfirm(t *testing.T) {
	clock := &manualClock{now: time.Unix(1000, 0)}
	q := newTestQueue(t, NewMemPersister(), clock.Now, time.Minute)

	const n = 200
	var bodies [][]byte
	for i := 0; i < n; i++ {
		bodies = append(bodies, []byte(fmt.Sprintf("m-%d", i)))
	}
	if _, err := q.Send(bodies...); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	confirmed := make(chan string, n)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				m, r, err := q.Take()
				if err != nil {
					return
				}
				if err := q.Confirm(r); err != nil {
					t.Errorf("Confirm: %v", err)
					return
				}
				confirmed <- m.ID
			}
		}()
	}
	wg.Wait()
	close(confirmed)
	seen := map[string]int{}
	for id := range confirmed {
		seen[id]++
	}
	if len(seen) != n {
		t.Fatalf("confirmed %d distinct messages, want %d", len(seen), n)
	}
	for id, c := range seen {
		if c != 1 {
			t.Fatalf("message %s confirmed %d times", id, c)
		}
	}
}

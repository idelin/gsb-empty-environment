package dnswindow

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"
)

// TestDifferentialAgainstFullRecomputation drives a randomized workload of
// out-of-order inserts, exact duplicates, corrections (including group and
// window moves), late rejections and watermark seals, checking the incremental
// Manager against the from-scratch oracle after every seal.
func TestDifferentialAgainstFullRecomputation(t *testing.T) {
	windowSize := 10 * time.Second
	for _, seed := range []int64{1, 2, 7, 42, 99, 20261111} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			manager, _ := NewManager(windowSize)
			oracleState := newOracle(windowSize)
			oracleSealed := map[string]bool{}

			clients := []string{"c-a", "c-b", "c-c"}
			registered := []string{"example.com", "test.org", "dns.net"}
			labels := []string{"aa", "bbb", "cccc", "dd", "eeeee", "x", "ffff", "gg"}

			mkName := func() string {
				depth := 1 + rng.Intn(3)
				parts := make([]byte, 0, 32)
				for i := 0; i < depth; i++ {
					if i > 0 {
						parts = append(parts, '.')
					}
					parts = append(parts, labels[rng.Intn(len(labels))]...)
				}
				if rng.Intn(4) == 0 {
					parts = append(parts, '.')
				}
				return string(parts)
			}
			mkEvent := func(id int, ts time.Time) Event {
				return Event{
					ID:               fmt.Sprintf("id-%04d", id),
					Client:           clients[rng.Intn(len(clients))],
					RegisteredDomain: registered[rng.Intn(len(registered))],
					QueriedName:      mkName(),
					NXDOMAIN:         rng.Intn(3) == 0,
					Time:             ts,
				}
			}

			nextID := 0
			watermark := time.Time{}
			for step := 0; step < 4000; step++ {
				roll := rng.Intn(100)
				switch {
				case roll < 45: // fresh event, often out of order
					ts := at(int64(1 + rng.Intn(180)))
					event := mkEvent(nextID, ts)
					nextID++
					outcome, err := manager.Submit(event)

					_, existed := oracleState.events[event.ID]
					if oracleSealed[event.ID] {
						if err != ErrSealed {
							t.Fatalf("step %d: tombstoned accept got %v, want ErrSealed", step, err)
						}
					} else if ts.After(watermark) && !existed {
						if err != nil || outcome != OutcomeAccepted {
							t.Fatalf("step %d: expected accept, got %v/%v", step, outcome, err)
						}
						oracleState.events[event.ID] = event
					} else if !existed && err != ErrLate {
						t.Fatalf("step %d: expected ErrLate, got %v", step, err)
					}

				case roll < 70 && nextID > 0: // correction of a known ID
					id := fmt.Sprintf("id-%04d", rng.Intn(nextID))
					old, open := oracleState.events[id]
					event := mkEvent(nextID, at(int64(1+rng.Intn(180))))
					event.ID = id
					nextID++
					outcome, err := manager.Submit(event)

					if oracleSealed[id] {
						if err != ErrSealed {
							t.Fatalf("step %d: tombstoned correction got %v, want ErrSealed", step, err)
						}
						continue
					}
					if !open {
						// The ID never made it in (e.g. its first submit was
						// late), so this is a fresh submission, not a correction.
						if !event.Time.After(watermark) {
							if err != ErrLate {
								t.Fatalf("step %d: unknown-ID late submit got %v, want ErrLate", step, err)
							}
							continue
						}
						if err != nil || outcome != OutcomeAccepted {
							t.Fatalf("step %d: unknown-ID submit got %v/%v", step, outcome, err)
						}
						oracleState.events[id] = event
						continue
					}
					if !event.Time.After(watermark) {
						if err != ErrLate {
							t.Fatalf("step %d: late correction got %v, want ErrLate", step, err)
						}
						continue
					}
					if old.equal(event) {
						if err != nil || outcome != OutcomeDuplicate {
							t.Fatalf("step %d: identical resubmit got %v/%v", step, outcome, err)
						}
						continue
					}
					if err != nil || outcome != OutcomeCorrected {
						t.Fatalf("step %d: correction got %v/%v", step, outcome, err)
					}
					oracleState.events[id] = event

				case roll < 85 && nextID > 0: // exact duplicate resubmission
					id := fmt.Sprintf("id-%04d", rng.Intn(nextID))
					if oracleSealed[id] {
						if _, err := manager.Submit(Event{ID: id, Time: at(1)}); err != ErrSealed {
							t.Fatalf("step %d: tombstoned duplicate got %v, want ErrSealed", step, err)
						}
					} else if event, open := oracleState.events[id]; open {
						outcome, err := manager.Submit(event)
						if err != nil || outcome != OutcomeDuplicate {
							t.Fatalf("step %d: duplicate got %v/%v", step, outcome, err)
						}
					}

				default: // advance watermark and cross-check sealed windows
					advance := at(int64(1 + rng.Intn(200)))
					if advance.After(watermark) {
						watermark = advance
					}
					got := manager.Advance(watermark)
					want := oracleState.seal(watermark)
					compareResults(t, got, want)

					// Oracle forgets sealed events; the Manager must reject
					// any later operation on those IDs as sealed.
					for start, members := range oracleState.sealed {
						for _, event := range members {
							delete(oracleState.events, event.ID)
							oracleSealed[event.ID] = true
							if _, err := manager.Submit(event); err != ErrSealed {
								t.Fatalf("sealed ID %s reuse: got %v, want ErrSealed", event.ID, err)
							}
						}
						delete(oracleState.sealed, start)
					}
				}
			}

			// Final drain: seal everything still open and compare again.
			got := manager.Advance(at(300))
			want := oracleState.seal(at(300))
			compareResults(t, got, want)
		})
	}
}

func TestConcurrentSubmitAndAdvance(t *testing.T) {
	manager, _ := NewManager(5 * time.Second)
	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			manager.Submit(Event{
				ID:               fmt.Sprintf("w-%d", i),
				Client:           "c",
				RegisteredDomain: "d",
				QueriedName:      "label.d",
				Time:             at(int64(i % 60)),
			})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			manager.Advance(at(int64(i * 5)))
		}
	}()
	wg.Wait()

	// Every admitted event has a unique ID; sealing the rest must not panic
	// and the total event count must never exceed the number of distinct IDs.
	results := manager.Advance(at(600))
	var total int64
	for _, result := range results {
		for _, group := range result.Groups {
			total += group.TotalEvents
		}
	}
	if total > 500 {
		t.Fatalf("admitted %d events from 500 unique IDs", total)
	}
}

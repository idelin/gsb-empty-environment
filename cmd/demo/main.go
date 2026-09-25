// Demo of the soft/hard expiry cache with an injected manual clock.
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"softcache"
)

type manualClock struct{ now time.Time }

func (m *manualClock) Now() time.Time          { return m.now }
func (m *manualClock) Advance(d time.Duration) { m.now = m.now.Add(d) }

func main() {
	clock := &manualClock{now: time.Unix(0, 0)}
	fetches := 0
	fail := false

	c := softcache.New(softcache.Config{
		Now:     clock.Now,
		SoftTTL: 10 * time.Second,
		HardTTL: 30 * time.Second,
		Backoff: func(failures int) time.Duration { return time.Duration(failures) * 20 * time.Second },
		Source: func(ctx context.Context, key string) (interface{}, uint64, error) {
			fetches++
			if fail {
				return nil, 0, errors.New("source unavailable")
			}
			return fmt.Sprintf("value-%d", fetches), uint64(fetches), nil
		},
		Release: func(v interface{}) { fmt.Printf("  [release] %v\n", v) },
	})
	defer c.Close()

	get := func(label string) {
		r, err := c.Get(context.Background(), "user:42")
		if err != nil {
			fmt.Printf("%-30s -> error: %v\n", label, err)
			return
		}
		fmt.Printf("%-30s -> value=%v version=%d stale=%v usable-for=+%ds\n",
			label, r.Value, r.Version, r.Stale, int64(r.UsableUntil.Sub(clock.now).Seconds()))
	}
	settle := func() { time.Sleep(50 * time.Millisecond) } // let background refresh land

	fmt.Println("== soft/hard expiry cache demo (manual clock) ==")
	get("t=0  initial load") // hard miss: synchronous fetch

	clock.Advance(15 * time.Second) // soft window: 10s..30s
	get("t=15 soft window read")    // stale value + one background refresh
	settle()
	get("t=15 after refresh") // fresh value-2

	clock.Advance(35 * time.Second) // past hard expiry of value-2
	get("t=50 hard window read")    // waits for refresh, returns value-3

	fail = true
	clock.Advance(35 * time.Second) // past hard expiry of value-3
	get("t=85 source failing")      // error, never the stale value
	get("t=85 backoff suppresses")  // same failure, no new fetch (fetches stays 4)

	c.Invalidate("user:42")
	fmt.Println("  [invalidate] user:42")
	fail = false
	get("t=85 refetch after delete") // fresh fetch, old refresh cannot resurrect

	fmt.Printf("total source fetches: %d\n", fetches)
}

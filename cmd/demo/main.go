// Command demo walks through the tenantcache feature set with a deterministic
// fake clock: guarantee admission, borrowing, fair reclamation, unsat
// certificates, idle lending with gradual return, and lease-pinned deletion.
package main

import (
	"fmt"
	"sort"
	"time"

	"tenantcache"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func stats(e *tenantcache.Engine) {
	s := e.Stats()
	fmt.Printf("  stats: used=%d free=%d", s.Used, s.Free)
	ids := make([]string, 0, len(s.Tenants))
	for id := range s.Tenants {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		ts := s.Tenants[id]
		fmt.Printf(" | %s g=%d g/b/r=%d/%d/%d idle=%v",
			id, ts.Guarantee, ts.Guaranteed, ts.Borrowed, ts.Reserved, ts.Idle)
	}
	fmt.Println()
}

func put(e *tenantcache.Engine, tenant, key string, size int64) *tenantcache.Certificate {
	cert, err := e.Put(tenant, key, size)
	if err != nil {
		panic(err)
	}
	return cert
}

func main() {
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	e, err := tenantcache.NewEngine(tenantcache.Config{
		Capacity:        1000,
		Clock:           clk.now,
		IdleAfter:       5 * time.Minute,
		ReclaimFraction: 0.25,
	})
	must(err)

	fmt.Println("== 1. guarantee admission: guarantees may not exceed physical capacity ==")
	must(e.AddTenant("analytics", "", 400))
	must(e.AddTenant("search", "", 300))
	must(e.AddTenant("batch", "", 0))
	must(e.AddTenant("adhoc", "", 0))
	fmt.Println("  added analytics=400 search=300 batch=0 adhoc=0 (capacity 1000)")
	if err := e.AddTenant("rogue", "", 301); err != nil {
		fmt.Println("  rogue guarantee 301 rejected:", err)
	}

	fmt.Println("== 2. guaranteed usage plus borrowing from the shared pool ==")
	put(e, "search", "doc-0", 100)
	for i := 0; i < 4; i++ {
		put(e, "analytics", fmt.Sprintf("cube-%d", i), 100)
	}
	for i := 0; i < 16; i++ { // shared pool while all active = 1000-400-300 = 300
		put(e, "batch", fmt.Sprintf("job-%d", i), 25)
	}
	stats(e)

	fmt.Println("== 3. pressure: adhoc burst reclaims batch's over-fair-share borrow ==")
	cert := put(e, "adhoc", "burst", 150)
	fmt.Println(" ", cert)
	stats(e)

	fmt.Println("== 4. unsatisfiable allocation carries an explicit reason ==")
	cert, err = e.Put("batch", "too-big", 500)
	fmt.Println(" ", cert)
	fmt.Println("  error:", err)

	fmt.Println("== 5. idle tenants lend quota; idle borrowers yield first ==")
	clk.advance(10 * time.Minute) // analytics, search, adhoc go idle
	e.Touch("batch")              // batch stays active
	stats(e)
	for i := 0; i < 16; i++ { // batch borrows into the lent guarantees
		if c := put(e, "batch", fmt.Sprintf("extra-%d", i), 25); len(c.Evicted) > 0 {
			fmt.Println(" ", c)
		}
	}
	stats(e)

	fmt.Println("== 6. analytics and search return: borrowers yield gradually ==")
	e.Touch("analytics")
	e.Touch("search")
	for round := 1; ; round++ {
		ev := e.Tick()
		fmt.Printf("  tick %d evictions: %v\n", round, ev)
		stats(e)
		if len(ev) == 0 {
			break
		}
	}

	fmt.Println("== 7. delete with an active read lease: no early release ==")
	lease, _, err := e.Get("search", "doc-0")
	must(err)
	must(e.DeleteTenant("search"))
	fmt.Println("  search deleted; leased key still readable:", lease.Key())
	stats(e)
	lease.Release()
	fmt.Println("  lease released; bytes freed")
	stats(e)
}

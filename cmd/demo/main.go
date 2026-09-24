// Command demo walks through the tagcache features with an in-memory
// data source: load, reverse index, invalidation race, billing, delete.
package main

import (
	"errors"
	"fmt"
	"time"

	"tagcache"
)

func main() {
	releases := 0
	// Injectable data source: entries per tag, plus a release hook so we
	// can see resources being freed.
	src := func(tag string, gen uint64) (tagcache.LoadResult, error) {
		data := map[string][]tagcache.Entry{
			"go":   {{ID: "1", Data: "generics"}, {ID: "2", Data: "slices"}},
			"news": {{ID: "2", Data: "slices"}, {ID: "3", Data: "maps"}},
		}
		return tagcache.LoadResult{
			Entries: data[tag],
			Release: func() { releases++ },
		}, nil
	}
	// Injectable clock.
	clock := func() time.Time { return time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC) }

	s := tagcache.New(src, clock, nil)

	fmt.Println("== load two tags (entry 2 shared) ==")
	must(s.LoadTag("go"))
	must(s.LoadTag("news"))
	fmt.Println("go:  ", ids(s.EntriesForTag("go")))
	fmt.Println("news:", ids(s.EntriesForTag("news")))
	fmt.Println("tags of entry 2:", s.TagsOf("2"))
	fmt.Println("billable entries (no double charge):", ids(s.BillableEntries()))
	if at, ok := s.LoadedAt("1"); ok {
		fmt.Println("entry 1 loaded at (injected clock):", at.Format(time.RFC3339))
	}

	fmt.Println("\n== delete tag 'go': other relations survive ==")
	s.DeleteTag("go")
	fmt.Println("tags of entry 2:", s.TagsOf("2"))
	fmt.Println("news:", ids(s.EntriesForTag("news")))

	fmt.Println("\n== delete entry 2: reverse index cleaned ==")
	s.DeleteEntry("2")
	fmt.Println("news:", ids(s.EntriesForTag("news")))

	fmt.Println("\n== invalidate during load: stale load discarded ==")
	blocking := func(tag string, gen uint64) (tagcache.LoadResult, error) {
		time.Sleep(50 * time.Millisecond) // simulate slow backend
		return src(tag, gen)
	}
	s2 := tagcache.New(blocking, nil, nil)
	errCh := make(chan error, 1)
	go func() { errCh <- s2.LoadTag("go") }()
	time.Sleep(10 * time.Millisecond)
	s2.InvalidateTag("go") // version changes mid-load
	err := <-errCh
	fmt.Println("load result:", err, "| ErrStaleGeneration?", errors.Is(err, tagcache.ErrStaleGeneration))
	fmt.Println("entries after stale load:", ids(s2.EntriesForTag("go")))
	fmt.Println("invariants:", ok(s2.CheckInvariants()))

	fmt.Println("\nresource releases observed:", releases)
}

func ids(entries []tagcache.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.ID
	}
	return out
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func ok(err error) string {
	if err != nil {
		return err.Error()
	}
	return "consistent"
}

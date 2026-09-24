// Command demo exercises the mvcc store end to end: atomic batch
// commits, consistent multi-key snapshot reads, delete visibility,
// GC bounded by active snapshots, and the snapshot expiry policy.
package main

import (
	"fmt"
	"log"
	"time"

	mvcc "example.com/mvcc"
)

func main() {
	// Injectable clock: here a manual one so the expiry demo is instant.
	now := time.Now()
	clock := func() time.Time { return now }

	store := mvcc.NewStore(mvcc.Options{
		Now:         clock,
		SnapshotTTL: 5 * time.Second,
		Load: func(key string) (string, bool, error) { // injectable data source
			if key == "config" {
				return "from-data-source", true, nil
			}
			return "", false, nil
		},
		Fail: func(op string) error { return nil }, // injectable failure hook
	})

	v1, _ := store.Commit(mvcc.Put("A", "a1"))
	v2, _ := store.Commit(mvcc.Put("B", "b1"))
	fmt.Printf("committed A=a1 at v%d, B=b1 at v%d\n", v1, v2)

	snap, err := store.Begin()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("snapshot opened at v%d (active snapshots: %d)\n", snap.Version(), store.ActiveSnapshots())

	v3, _ := store.Commit(mvcc.Put("A", "a2"), mvcc.Put("B", "b2"))
	fmt.Printf("committed A=a2,B=b2 atomically at v%d\n", v3)

	view, _ := snap.Get("A", "B")
	fmt.Printf("snapshot reads A=%s B=%s (consistent old view)\n", view["A"], view["B"])
	latest, _ := store.GetLatest("A", "B")
	fmt.Printf("latest   reads A=%s B=%s\n", latest["A"], latest["B"])

	if _, err := store.Commit(mvcc.Del("A")); err != nil {
		log.Fatal(err)
	}
	view, _ = snap.Get("A")
	fmt.Printf("after delete: snapshot still sees A=%s, latest sees %d keys\n", view["A"], len(must(store.GetLatest("A"))))

	fmt.Printf("GC with snapshot open reclaimed %d version(s)\n", store.GC())
	snap.Close()
	fmt.Printf("snapshot closed (active: %d); GC reclaimed %d more\n",
		store.ActiveSnapshots(), store.GC())

	ext, _ := store.GetLatest("config")
	fmt.Printf("loader fallback: config=%s\n", ext["config"])

	// Expiry policy: reads fail with ErrSnapshotExpired past the TTL and
	// the snapshot is released automatically.
	short, _ := store.Begin()
	now = now.Add(10 * time.Second) // advance the injected clock past the TTL
	_, err = short.Get("A")
	fmt.Printf("expired snapshot read -> %v (active: %d)\n", err, store.ActiveSnapshots())
}

func must(m map[string]string, err error) map[string]string {
	if err != nil {
		log.Fatal(err)
	}
	return m
}

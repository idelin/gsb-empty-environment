package main

import (
	"fmt"
	"time"

	"mvcc/mvcc"
)

func main() {
	now := int64(0)
	store, err := mvcc.New(mvcc.Config{
		Clock:       func() int64 { return now },
		SnapshotTTL: 100 * time.Millisecond,
		Source: func(keys []string) (mvcc.SourceBatch, error) {
			records := make([]mvcc.Record, 0, len(keys))
			for _, key := range keys {
				if key == "B" {
					records = append(records, mvcc.Record{Key: key, Value: []byte("B-v2-from-source"), Revision: 20})
				}
			}
			return mvcc.SourceBatch{Revision: 20, Records: records}, nil
		},
	})
	if err != nil {
		panic(err)
	}
	defer store.Close()

	if _, err := store.Write([]mvcc.Record{
		{Key: "A", Value: []byte("A-v1")},
		{Key: "B", Value: []byte("B-v1")},
	}); err != nil {
		panic(err)
	}
	snapshot, err := store.Snapshot()
	if err != nil {
		panic(err)
	}

	if _, err := store.Write([]mvcc.Record{{Key: "A", Value: []byte("A-v2")}}); err != nil {
		panic(err)
	}
	if _, err := store.Load([]string{"B"}); err != nil {
		panic(err)
	}

	printRead("old snapshot", mustRead(snapshot, []string{"A", "B"}))
	printRead("latest state", mustRead(store, []string{"A", "B"}))

	if _, err := store.Write([]mvcc.Record{{Key: "B", Delete: true}}); err != nil {
		panic(err)
	}
	printRead("deleted B is still visible in old snapshot", mustRead(snapshot, []string{"A", "B"}))

	now = 100
	fmt.Printf("ttl expired=%v; read after expiry error=%v\n", snapshot.Expired(), expectExpired(snapshot, []string{"A"}))
	if err := snapshot.Close(); err != nil {
		panic(err)
	}

	now = 200
	old, err := store.Snapshot()
	if err != nil {
		panic(err)
	}
	if _, err := store.Write([]mvcc.Record{{Key: "A", Value: []byte("A-v3")}}); err != nil {
		panic(err)
	}
	stats, err := store.GC()
	if err != nil {
		panic(err)
	}
	fmt.Printf("gc while snapshot active: removed entries=%d watermark=%d active=%d\n", stats.EntriesRemoved, stats.Watermark, stats.ActiveSnapshots)
	if err := old.Close(); err != nil {
		panic(err)
	}
	stats, err = store.GC()
	if err != nil {
		panic(err)
	}
	fmt.Printf("gc after snapshot release: removed entries=%d watermark=%d active=%d\n", stats.EntriesRemoved, stats.Watermark, stats.ActiveSnapshots)
}

type reader interface {
	Read([]string) (mvcc.ReadResult, error)
}

func mustRead(source reader, keys []string) mvcc.ReadResult {
	result, err := source.Read(keys)
	if err != nil {
		panic(err)
	}
	return result
}

func expectExpired(snapshot *mvcc.Snapshot, keys []string) error {
	_, err := snapshot.Read(keys)
	return err
}

func printRead(label string, result mvcc.ReadResult) {
	fmt.Printf("%s (version %d): A=%q B=%q deleted=%v missing=%v\n",
		label,
		result.Version,
		result.Values["A"],
		result.Values["B"],
		result.Deleted,
		result.Missing,
	)
}

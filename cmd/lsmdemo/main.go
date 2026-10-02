// Command lsmdemo drives the WAL/MemTable/SSTable state machine through the
// acceptance scenario: sustained 1 KiB writes against a 4 MiB MemTable
// threshold, printing every rotation and flush event as it happens.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"time"

	"lsm"
)

func value(i int) []byte {
	v := make([]byte, 1024)
	binary.BigEndian.PutUint64(v[:8], uint64(i))
	for j := 8; j < len(v); j++ {
		v[j] = byte('a' + i%26)
	}
	return v
}

func main() {
	dir, err := os.MkdirTemp("", "lsmdemo-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	db, err := lsm.Open(lsm.Options{
		Dir:          dir,
		MemTableSize: 4 << 20, // 4 MiB freeze threshold
		Logger: func(format string, args ...any) {
			fmt.Printf("  %s\n", fmt.Sprintf(format, args...))
		},
	})
	if err != nil {
		panic(err)
	}
	fmt.Printf("opened db in %s (MemTable threshold = 4 MiB)\n", dir)

	const total = 10000 // ~10.4 MiB -> two threshold crossings
	fmt.Printf("writing %d entries of 1 KiB ...\n", total)
	start := time.Now()
	for i := 0; i < total; i++ {
		if err := db.Put([]byte(fmt.Sprintf("key%08d", i)), value(i)); err != nil {
			panic(err)
		}
	}
	writeElapsed := time.Since(start)

	db.WaitForIdle()
	stats := db.Stats()
	fmt.Printf("writes finished in %v (%.0f ops/s, no foreground stall)\n",
		writeElapsed, float64(total)/writeElapsed.Seconds())
	fmt.Printf("state: rotations=%d flushes=%d queuedImmutables=%d L0 SSTables=%d activeMemTable=%.2f MiB (%d entries)\n",
		stats.Rotations, stats.Flushes, stats.QueuedImmutables, stats.SSTables,
		float64(stats.ActiveSize)/(1<<20), stats.ActiveEntries)

	fmt.Println("verifying all keys ...")
	for i := 0; i < total; i++ {
		v, ok, err := db.Get([]byte(fmt.Sprintf("key%08d", i)))
		if err != nil || !ok || !bytes.Equal(v, value(i)) {
			panic(fmt.Sprintf("verification failed at key %d: ok=%v err=%v", i, ok, err))
		}
	}
	fmt.Printf("verified %d/%d keys: OK\n", total, total)

	entries, _ := os.ReadDir(dir)
	fmt.Print("files on disk:")
	for _, e := range entries {
		fmt.Print(" ", e.Name())
	}
	fmt.Println()

	if err := db.Close(); err != nil {
		panic(err)
	}
	fmt.Println("closed cleanly (remaining MemTable flushed to L0)")
}

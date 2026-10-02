package lsm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"
)

const testValueSize = 1024

func testKey(i int) []byte { return []byte(fmt.Sprintf("key%08d", i)) }

func testValue(i int) []byte {
	v := make([]byte, testValueSize)
	binary.BigEndian.PutUint64(v[:8], uint64(i))
	for j := 8; j < len(v); j++ {
		v[j] = byte('a' + i%26)
	}
	return v
}

func keyIndex(t *testing.T, key []byte) int {
	t.Helper()
	i, err := strconv.Atoi(string(key)[3:])
	if err != nil {
		t.Fatalf("bad key %q: %v", key, err)
	}
	return i
}

func listFiles(t *testing.T, dir, prefix, suffix string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if id, ok := parseFileName(e.Name(), prefix, suffix); ok {
			_ = id
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// TestRotationAndSequentialFlush is the acceptance scenario: sustained
// writes cross the 4 MiB threshold twice, producing exactly two immutable
// MemTables that are flushed sequentially into L0, with safe WAL rotation
// and full data integrity.
func TestRotationAndSequentialFlush(t *testing.T) {
	dir := t.TempDir()

	var logMu sync.Mutex
	var events []string
	logger := func(format string, args ...any) {
		logMu.Lock()
		events = append(events, fmt.Sprintf(format, args...))
		logMu.Unlock()
	}

	db, err := Open(Options{Dir: dir, MemTableSize: 4 << 20, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}

	const total = 9000 // ~9.3 MiB of 1 KiB values: crosses 4 MiB twice
	for i := 0; i < total; i++ {
		if err := db.Put(testKey(i), testValue(i)); err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
	}
	db.WaitForIdle()

	// Exactly two rotations and two sequential flushes into L0.
	stats := db.Stats()
	if stats.Rotations != 2 || stats.Flushes != 2 {
		t.Fatalf("rotations=%d flushes=%d, want 2/2", stats.Rotations, stats.Flushes)
	}
	if stats.QueuedImmutables != 0 {
		t.Fatalf("immutable queue not drained: %d", stats.QueuedImmutables)
	}

	// Two L0 SSTable files on disk, in sequence.
	ssts := listFiles(t, dir, "L0-", ".sst")
	if len(ssts) != 2 || ssts[0] != "L0-000001.sst" || ssts[1] != "L0-000002.sst" {
		t.Fatalf("L0 files = %v, want [L0-000001.sst L0-000002.sst]", ssts)
	}

	// WAL safely rotated: segments of flushed MemTables are gone, only the
	// segment paired with the active MemTable remains.
	wals := listFiles(t, dir, "wal-", ".log")
	if len(wals) != 1 || wals[0] != "wal-000003.log" {
		t.Fatalf("WAL segments = %v, want [wal-000003.log]", wals)
	}

	// Every key is readable through the DB with the exact value written.
	for i := 0; i < total; i++ {
		v, ok, err := db.Get(testKey(i))
		if err != nil || !ok {
			t.Fatalf("get %d: ok=%v err=%v", i, ok, err)
		}
		if !bytes.Equal(v, testValue(i)) {
			t.Fatalf("get %d: value mismatch", i)
		}
	}

	// The SSTable files themselves contain sorted, correct entries, and
	// together with the active MemTable they cover all writes exactly once.
	covered := make(map[int]bool)
	for _, name := range ssts {
		id, _ := parseFileName(name, "L0-", ".sst")
		st, err := openSSTable(filepath.Join(dir, name), id)
		if err != nil {
			t.Fatal(err)
		}
		var prev []byte
		err = st.Iterate(func(k, v []byte) bool {
			if prev != nil && bytes.Compare(prev, k) >= 0 {
				t.Errorf("%s: keys out of order at %q", name, k)
			}
			prev = bytes.Clone(k)
			i := keyIndex(t, k)
			if !bytes.Equal(v, testValue(i)) {
				t.Errorf("%s: value mismatch for %q", name, k)
			}
			if covered[i] {
				t.Errorf("key %q covered by more than one component", k)
			}
			covered[i] = true
			return true
		})
		if err != nil {
			t.Fatal(err)
		}
		st.Close()
	}
	if len(covered)+db.mem.Len() != total {
		t.Fatalf("SSTables cover %d keys, active MemTable holds %d, total %d",
			len(covered), db.mem.Len(), total)
	}

	// Rotation/flush events were logged in order.
	logMu.Lock()
	var rotates, flushes int
	for _, e := range events {
		switch {
		case len(e) >= 8 && e[:8] == "[rotate]":
			rotates++
			if flushes > rotates-1 {
				t.Errorf("flush of table %d logged before its rotate: %v", flushes+1, events)
			}
		case len(e) >= 7 && e[:7] == "[flush]":
			flushes++
		}
	}
	logMu.Unlock()
	if rotates != 2 || flushes != 2 {
		t.Fatalf("logged rotates=%d flushes=%d, want 2/2", rotates, flushes)
	}

	// Close flushes the remaining active MemTable; a fresh Open must see all
	// data from L0 files alone.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db2, err := Open(Options{Dir: dir, MemTableSize: 4 << 20})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < total; i++ {
		v, ok, err := db2.Get(testKey(i))
		if err != nil || !ok || !bytes.Equal(v, testValue(i)) {
			t.Fatalf("reopened get %d: ok=%v err=%v", i, ok, err)
		}
	}
	if err := db2.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestForegroundWritesDoNotStall proves the write path never waits for the
// flusher: while the background flush is blocked, foreground Puts (including
// ones that trigger further rotations) keep completing.
func TestForegroundWritesDoNotStall(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(Options{Dir: dir, MemTableSize: 256 << 10})
	if err != nil {
		t.Fatal(err)
	}

	flusherEntered := make(chan struct{})
	releaseFlusher := make(chan struct{})
	var once sync.Once
	db.flushHook = func() {
		once.Do(func() { close(flusherEntered) })
		<-releaseFlusher
	}

	// Write enough to trigger two rotations (~2.5 thresholds).
	const total = 700
	writesDone := make(chan struct{})
	go func() {
		defer close(writesDone)
		for i := 0; i < total; i++ {
			if err := db.Put(testKey(i), testValue(i)); err != nil {
				t.Errorf("put %d: %v", i, err)
				return
			}
		}
	}()

	// Wait until the flusher is stuck inside the first flush...
	select {
	case <-flusherEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("flusher never started")
	}

	// ...then require all foreground writes to finish while it is stuck.
	select {
	case <-writesDone:
	case <-time.After(2 * time.Second):
		t.Fatal("foreground writes stalled behind the background flush")
	}

	stats := db.Stats()
	if stats.Rotations < 2 {
		t.Fatalf("rotations=%d, want >= 2", stats.Rotations)
	}
	if stats.QueuedImmutables == 0 {
		t.Fatal("expected immutable MemTables queued behind the blocked flusher")
	}

	close(releaseFlusher)
	db.WaitForIdle()

	for i := 0; i < total; i++ {
		v, ok, err := db.Get(testKey(i))
		if err != nil || !ok || !bytes.Equal(v, testValue(i)) {
			t.Fatalf("get %d after flush: ok=%v err=%v", i, ok, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestCrashRecovery verifies WAL replay: after a simulated crash (no clean
// Close, no flush), reopening the directory restores every write.
func TestCrashRecovery(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(Options{Dir: dir, MemTableSize: 4 << 20})
	if err != nil {
		t.Fatal(err)
	}

	const total = 500
	for i := 0; i < total; i++ {
		if err := db.Put(testKey(i), testValue(i)); err != nil {
			t.Fatal(err)
		}
	}
	db.debugCrash() // WAL synced, process "dies" before any flush

	db2, err := Open(Options{Dir: dir, MemTableSize: 4 << 20})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < total; i++ {
		v, ok, err := db2.Get(testKey(i))
		if err != nil || !ok || !bytes.Equal(v, testValue(i)) {
			t.Fatalf("recovered get %d: ok=%v err=%v", i, ok, err)
		}
	}

	// The database keeps accepting writes after recovery.
	if err := db2.Put(testKey(total), testValue(total)); err != nil {
		t.Fatal(err)
	}
	if err := db2.Close(); err != nil {
		t.Fatal(err)
	}

	db3, err := Open(Options{Dir: dir, MemTableSize: 4 << 20})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= total; i++ {
		v, ok, err := db3.Get(testKey(i))
		if err != nil || !ok || !bytes.Equal(v, testValue(i)) {
			t.Fatalf("post-recovery get %d: ok=%v err=%v", i, ok, err)
		}
	}
	if err := db3.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestOverwriteAcrossComponents checks that a newer value in the MemTable
// shadows an older one already flushed to L0.
func TestOverwriteAcrossComponents(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(Options{Dir: dir, MemTableSize: 256 << 10})
	if err != nil {
		t.Fatal(err)
	}
	const total = 400 // forces at least one flush
	for i := 0; i < total; i++ {
		if err := db.Put(testKey(i), testValue(i)); err != nil {
			t.Fatal(err)
		}
	}
	db.WaitForIdle()
	if db.Stats().Flushes == 0 {
		t.Fatal("expected at least one flush")
	}
	// Overwrite keys that now live in L0; new values land in the MemTable.
	for i := 0; i < 10; i++ {
		if err := db.Put(testKey(i), testValue(total+i)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 10; i++ {
		v, ok, err := db.Get(testKey(i))
		if err != nil || !ok || !bytes.Equal(v, testValue(total+i)) {
			t.Fatalf("overwrite get %d: ok=%v err=%v", i, ok, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

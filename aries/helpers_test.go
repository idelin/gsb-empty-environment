package aries

import (
	"io/ioutil"
	"os"
	"runtime"
	"testing"
	"time"
)

// tempDir creates a temporary directory that is removed when the test
// ends (via the returned cleanup func; call with defer).
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := ioutil.TempDir("", "aries-test-")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	return dir
}

// countGoroutines lets the scheduler settle and returns the goroutine
// count. Used for leak assertions.
func countGoroutines() int {
	for i := 0; i < 20; i++ {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
	return runtime.NumGoroutine()
}

// assertNoLeak fails the test if more goroutines are alive now than when
// the returned baseline was taken.
func assertNoLeak(t *testing.T, baseline int) {
	t.Helper()
	after := countGoroutines()
	if after > baseline {
		t.Fatalf("goroutine leak: baseline=%d after=%d", baseline, after)
	}
}

// mustWrite is a test helper performing a logged write, reading the old
// image from the pool first.
func mustWrite(t *testing.T, tm *TxnMgr, txn uint64, page uint32, val string) {
	t.Helper()
	old, err := tm.ReadAt(page, 0, len(val))
	if err != nil {
		t.Fatalf("read page %d: %v", page, err)
	}
	if _, err := tm.Write(txn, page, 0, old, []byte(val)); err != nil {
		t.Fatalf("write txn=%d page=%d: %v", txn, page, err)
	}
}

// pageString reads n bytes at offset 0 of a page directly from the store.
func pageString(t *testing.T, store *PageStore, page uint32, n int) string {
	t.Helper()
	_, data, err := store.Read(page)
	if err != nil {
		t.Fatalf("read page %d: %v", page, err)
	}
	return string(data[:n])
}

// cleanup removes dir; meant for defer.
func cleanup(dir string) { os.RemoveAll(dir) }

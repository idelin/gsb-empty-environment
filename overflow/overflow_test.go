package overflow

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

const testPageSize = 128

func testCap() int { return testPageSize - headerSize }

func pagesFor(n int) uint64 {
	if n == 0 {
		return 0
	}
	return uint64((n + testCap() - 1) / testCap())
}

func payloadOf(seed byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = seed + byte(i%23)
	}
	return b
}

func openTestStore(t *testing.T) (*Store, string, func()) {
	t.Helper()
	dir, err := ioutil.TempDir("", "overflow-test")
	if err != nil {
		t.Fatal(err)
	}
	st, err := Open(filepath.Join(dir, "data.ovf"), testPageSize)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}
	return st, dir, func() { os.RemoveAll(dir) }
}

func reopen(t *testing.T, st *Store, dir string) *Store {
	t.Helper()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st2, err := Open(filepath.Join(dir, "data.ovf"), 0)
	if err != nil {
		t.Fatal(err)
	}
	return st2
}

func mustGet(t *testing.T, st *Store, name string) []byte {
	t.Helper()
	got, err := st.Get(name)
	if err != nil {
		t.Fatalf("Get(%q): %v", name, err)
	}
	return got
}

func mustVerify(t *testing.T, st *Store) (Ledger, [sha256.Size]byte) {
	t.Helper()
	led, digest, err := st.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	return led, digest
}

// digestOf recomputes the expected content digest independently.
func digestOf(recs map[string][]byte) [sha256.Size]byte {
	names := make([]string, 0, len(recs))
	for n := range recs {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		h.Write([]byte(n))
		h.Write(recs[n])
	}
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}

func TestUpdateShrink(t *testing.T) {
	st, _, cleanup := openTestStore(t)
	defer cleanup()

	big := payloadOf(0x11, 1000)
	if err := st.Put("r", big); err != nil {
		t.Fatal(err)
	}
	if err := st.Reclaim(); err != nil {
		t.Fatal(err)
	}
	led1, _ := mustVerify(t, st)
	if led1.Reachable != pagesFor(1000) || led1.Garbage != 0 {
		t.Fatalf("after big put: %+v", led1)
	}

	small := payloadOf(0x22, 100)
	if err := st.Put("r", small); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, st, "r"); !bytes.Equal(got, small) {
		t.Fatalf("got %d bytes, want %d", len(got), len(small))
	}
	// Old chain is unreachable but not yet deleted.
	led2, d2 := mustVerify(t, st)
	if led2.Reachable != pagesFor(100) {
		t.Fatalf("reachable = %d, want %d", led2.Reachable, pagesFor(100))
	}
	if led2.Garbage != pagesFor(1000) {
		t.Fatalf("garbage = %d, want old chain of %d", led2.Garbage, pagesFor(1000))
	}
	if d2 != digestOf(map[string][]byte{"r": small}) {
		t.Fatal("digest mismatch after shrink")
	}

	if err := st.Reclaim(); err != nil {
		t.Fatal(err)
	}
	led3, d3 := mustVerify(t, st)
	if led3.Garbage != 0 || led3.Free != led2.Free+pagesFor(1000) {
		t.Fatalf("after reclaim: %+v (before %+v)", led3, led2)
	}
	if d3 != d2 {
		t.Fatal("digest changed after reclaim")
	}
	if got := mustGet(t, st, "r"); !bytes.Equal(got, small) {
		t.Fatal("record lost after reclaim")
	}
}

func TestEmptyPayload(t *testing.T) {
	st, _, cleanup := openTestStore(t)
	defer cleanup()

	if err := st.Put("e", nil); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, st, "e"); len(got) != 0 {
		t.Fatalf("empty record returned %d bytes", len(got))
	}
	led, d := mustVerify(t, st)
	if led.Reachable != 0 || led.Garbage != 0 {
		t.Fatalf("empty record should own no pages: %+v", led)
	}
	if d != digestOf(map[string][]byte{"e": {}}) {
		t.Fatal("digest mismatch for empty record")
	}

	big := payloadOf(0x33, 500)
	if err := st.Put("e", big); err != nil {
		t.Fatal(err)
	}
	if err := st.Reclaim(); err != nil {
		t.Fatal(err)
	}
	if err := st.Put("e", []byte{}); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, st, "e"); len(got) != 0 {
		t.Fatalf("emptied record returned %d bytes", len(got))
	}
	led2, _ := mustVerify(t, st)
	if led2.Garbage != pagesFor(500) {
		t.Fatalf("garbage = %d, want %d", led2.Garbage, pagesFor(500))
	}
	if err := st.Reclaim(); err != nil {
		t.Fatal(err)
	}
	led3, _ := mustVerify(t, st)
	if led3.Garbage != 0 || led3.Reachable != 0 {
		t.Fatalf("after reclaim: %+v", led3)
	}
}

func TestRestartAfterRootSwitch(t *testing.T) {
	st, dir, cleanup := openTestStore(t)
	defer cleanup()

	v1 := payloadOf(0x44, 700)
	v2 := payloadOf(0x55, 900)
	if err := st.Put("k", v1); err != nil {
		t.Fatal(err)
	}
	if err := st.Put("k", v2); err != nil {
		t.Fatal(err)
	}

	st = reopen(t, st, dir)
	if got := mustGet(t, st, "k"); !bytes.Equal(got, v2) {
		t.Fatalf("after restart got %d bytes, want %d", len(got), len(v2))
	}
	led, d := mustVerify(t, st)
	if led.Reachable != pagesFor(900) || led.Garbage != pagesFor(700) {
		t.Fatalf("after restart: %+v", led)
	}
	if d != digestOf(map[string][]byte{"k": v2}) {
		t.Fatal("digest mismatch after restart")
	}
	if err := st.Reclaim(); err != nil {
		t.Fatal(err)
	}
	led2, _ := mustVerify(t, st)
	if led2.Garbage != 0 {
		t.Fatalf("after reclaim: %+v", led2)
	}
}

// failPager fails the failAt-th writePage call (1-based).
type failPager struct {
	inner   pager
	writes  int
	failAt  int
	failErr error
}

func (p *failPager) readPage(id uint64) ([]byte, error) { return p.inner.readPage(id) }
func (p *failPager) sync() error                        { return p.inner.sync() }
func (p *failPager) close() error                       { return p.inner.close() }

func (p *failPager) writePage(id uint64, data []byte) error {
	p.writes++
	if p.failAt > 0 && p.writes == p.failAt {
		return p.failErr
	}
	return p.inner.writePage(id, data)
}

func TestWriteFailureKeepsOldRecord(t *testing.T) {
	old := payloadOf(0x66, 500)
	nu := payloadOf(0x77, 300)
	chainWrites := int(pagesFor(300)) // superblock write comes after these

	cases := []struct {
		name   string
		failAt int
	}{
		{"chain write fails", 2},
		{"root switch fails", chainWrites + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, dir, cleanup := openTestStore(t)
			defer cleanup()
			if err := st.Put("rec", old); err != nil {
				t.Fatal(err)
			}
			if err := st.Reclaim(); err != nil {
				t.Fatal(err)
			}
			fp := &failPager{inner: st.p, failAt: tc.failAt, failErr: errors.New("injected write failure")}
			st.p = fp
			if err := st.Put("rec", nu); err == nil {
				t.Fatal("Put should have failed")
			}
			if got := mustGet(t, st, "rec"); !bytes.Equal(got, old) {
				t.Fatal("old record lost after failed update")
			}

			st = reopen(t, st, dir)
			if got := mustGet(t, st, "rec"); !bytes.Equal(got, old) {
				t.Fatal("old record lost after failed update + restart")
			}
			led, d := mustVerify(t, st)
			if led.Reachable != pagesFor(500) {
				t.Fatalf("reachable = %d, want %d", led.Reachable, pagesFor(500))
			}
			if d != digestOf(map[string][]byte{"rec": old}) {
				t.Fatal("digest mismatch after failed update")
			}
			// Partial new chain is unreachable garbage; Reclaim deletes it.
			if err := st.Reclaim(); err != nil {
				t.Fatal(err)
			}
			led2, _ := mustVerify(t, st)
			if led2.Garbage != 0 {
				t.Fatalf("after reclaim: %+v", led2)
			}
		})
	}
}

// corruptNext rewrites the next pointer of page id in the open store.
func corruptNext(t *testing.T, st *Store, id, next uint64) {
	t.Helper()
	page, err := st.p.readPage(id)
	if err != nil {
		t.Fatal(err)
	}
	_, chunk := decodeChainPage(page)
	buf := make([]byte, st.pageSize)
	encodeChainPage(buf, next, chunk)
	if err := st.p.writePage(id, buf); err != nil {
		t.Fatal(err)
	}
	if err := st.p.sync(); err != nil {
		t.Fatal(err)
	}
}

func TestSharedPageDetected(t *testing.T) {
	st, _, cleanup := openTestStore(t)
	defer cleanup()

	a := payloadOf(0x88, 400)
	b := payloadOf(0x99, 200)
	if err := st.Put("a", a); err != nil {
		t.Fatal(err)
	}
	if err := st.Put("b", b); err != nil {
		t.Fatal(err)
	}
	// Illegally point b's chain head at a's head: pages become shared.
	corruptNext(t, st, st.roots["b"], st.roots["a"])

	if _, _, err := st.Verify(); err == nil {
		t.Fatal("Verify should report shared page")
	} else {
		ce, ok := err.(*CorruptError)
		if !ok || ce.Kind != CorruptSharedPage {
			t.Fatalf("err = %v, want shared-page corruption", err)
		}
	}
	// Reclaim must refuse to delete anything from a corrupt structure.
	if err := st.Reclaim(); err == nil {
		t.Fatal("Reclaim should refuse corrupt structure")
	}
	// The untouched record is still readable.
	if got := mustGet(t, st, "a"); !bytes.Equal(got, a) {
		t.Fatal("record a damaged")
	}
}

func TestCycleDetected(t *testing.T) {
	st, _, cleanup := openTestStore(t)
	defer cleanup()

	p := payloadOf(0xAA, 400)
	if err := st.Put("c", p); err != nil {
		t.Fatal(err)
	}
	head := st.roots["c"]
	page, err := st.p.readPage(head)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := decodeChainPage(page)
	// Close a loop: second page points back at the head.
	corruptNext(t, st, next, head)

	if _, err := st.Get("c"); err == nil {
		t.Fatal("Get should report cycle")
	} else if ce, ok := err.(*CorruptError); !ok || ce.Kind != CorruptCycle {
		t.Fatalf("err = %v, want cycle corruption", err)
	}
	if _, _, err := st.Verify(); err == nil {
		t.Fatal("Verify should report cycle")
	} else if ce, ok := err.(*CorruptError); !ok || ce.Kind != CorruptCycle {
		t.Fatalf("err = %v, want cycle corruption", err)
	}
	if err := st.Reclaim(); err == nil {
		t.Fatal("Reclaim should refuse corrupt structure")
	}
}

func TestLedgerAndDigest(t *testing.T) {
	st, _, cleanup := openTestStore(t)
	defer cleanup()

	recs := map[string][]byte{
		"alpha": payloadOf(0x01, 1000),
		"beta":  payloadOf(0x02, 1),
		"gamma": payloadOf(0x03, 250),
		"empty": {},
	}
	for n, p := range recs {
		if err := st.Put(n, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Delete("gamma"); err != nil {
		t.Fatal(err)
	}
	delete(recs, "gamma")
	if err := st.Reclaim(); err != nil {
		t.Fatal(err)
	}

	led, d := mustVerify(t, st)
	var wantReachable uint64
	for _, p := range recs {
		wantReachable += pagesFor(len(p))
	}
	if led.Reachable != wantReachable {
		t.Fatalf("reachable = %d, want %d", led.Reachable, wantReachable)
	}
	if led.Garbage != 0 {
		t.Fatalf("garbage = %d, want 0", led.Garbage)
	}
	if led.Total != led.Free+led.Reachable+led.Garbage {
		t.Fatalf("ledger does not balance: %+v", led)
	}
	if d != digestOf(recs) {
		t.Fatal("content digest mismatch")
	}
	for n, p := range recs {
		if got := mustGet(t, st, n); !bytes.Equal(got, p) {
			t.Fatalf("record %q corrupted", n)
		}
	}

	// Update one record and confirm the ledger tracks the orphan chain.
	if err := st.Put("alpha", payloadOf(0x04, 40)); err != nil {
		t.Fatal(err)
	}
	recs["alpha"] = payloadOf(0x04, 40)
	led2, d2 := mustVerify(t, st)
	if led2.Garbage != pagesFor(1000) {
		t.Fatalf("garbage = %d, want %d", led2.Garbage, pagesFor(1000))
	}
	if d2 != digestOf(recs) {
		t.Fatal("content digest mismatch after update")
	}
	if err := st.Reclaim(); err != nil {
		t.Fatal(err)
	}
	led3, _ := mustVerify(t, st)
	if led3.Garbage != 0 || led3.Total != led3.Free+led3.Reachable {
		t.Fatalf("ledger does not balance after reclaim: %+v", led3)
	}
}

package overflow

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

const pageCap = PageSize - headerSize

func tempStore(t *testing.T) (*Store, string) {
	dir, err := ioutil.TempDir("", "overflow-test-")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "data.ovf")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func payload(seed byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = seed + byte(i%251)
	}
	return out
}

func mustPut(t *testing.T, s *Store, name string, data []byte) {
	t.Helper()
	if err := s.Put(name, data); err != nil {
		t.Fatalf("Put(%q): %v", name, err)
	}
}

func mustGet(t *testing.T, s *Store, name string, want []byte) {
	t.Helper()
	got, err := s.Get(name)
	if err != nil {
		t.Fatalf("Get(%q): %v", name, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Get(%q): got %d bytes, want %d bytes", name, len(got), len(want))
	}
}

func mustVerify(t *testing.T, s *Store) *Ledger {
	t.Helper()
	l, err := s.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if l.Used+l.Free+1 != l.Pages {
		t.Fatalf("ledger does not balance: used=%d free=%d pages=%d", l.Used, l.Free, l.Pages)
	}
	return l
}

func reopen(t *testing.T, s *Store, path string) *Store {
	t.Helper()
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	return s
}

func TestPutGetRoundTrip(t *testing.T) {
	s, _ := tempStore(t)
	defer s.Close()
	big := payload(7, 3*pageCap+17)
	mustPut(t, s, "big", big)
	mustGet(t, s, "big", big)
	mustVerify(t, s)
}

func TestUpdateShortens(t *testing.T) {
	s, path := tempStore(t)
	defer s.Close()

	long := payload(1, 2000) // 9 pages
	mustPut(t, s, "rec", long)
	before := mustVerify(t, s)
	if before.Used != 9 {
		t.Fatalf("long record should use 9 pages, got %d", before.Used)
	}

	short := payload(2, 100) // 1 page
	mustPut(t, s, "rec", short)
	mustGet(t, s, "rec", short)
	after := mustVerify(t, s)
	if after.Used != 1 {
		t.Fatalf("short record should use 1 page, got %d", after.Used)
	}
	// The 9 old pages must be recycled; the new page was appended.
	if after.Free != 9 {
		t.Fatalf("expected 9 recycled pages, got %d", after.Free)
	}

	// Recycled pages must be reused before the file grows again.
	pagesBefore := after.Pages
	mustPut(t, s, "rec", payload(3, 500)) // 3 pages, fits in recycled set
	l := mustVerify(t, s)
	if l.Pages != pagesBefore {
		t.Fatalf("file grew despite recycled pages: %d -> %d", pagesBefore, l.Pages)
	}

	s = reopen(t, s, path)
	mustGet(t, s, "rec", payload(3, 500))
	mustVerify(t, s)
}

func TestEmptyPayload(t *testing.T) {
	s, path := tempStore(t)
	defer s.Close()

	mustPut(t, s, "e", payload(9, 1000))
	mustPut(t, s, "e", nil)
	got, err := s.Get("e")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty payload, got %d bytes", len(got))
	}
	l := mustVerify(t, s)
	if l.Records != 0 || l.Used != 0 {
		t.Fatalf("empty payload must free the whole chain: %+v", l)
	}
	if l.Free != l.Pages-1 {
		t.Fatalf("all pages should be free: %+v", l)
	}

	// Empty put on a missing record is a no-op.
	mustPut(t, s, "missing", nil)
	mustVerify(t, s)

	s = reopen(t, s, path)
	mustGet(t, s, "e", nil)
	mustVerify(t, s)
}

func TestRestartAfterRootSwitch(t *testing.T) {
	s, path := tempStore(t)

	v1 := payload(1, 700)
	v2 := payload(2, 1200)
	mustPut(t, s, "a", v1)
	mustPut(t, s, "b", payload(3, 50))
	mustPut(t, s, "a", v2) // switch root of "a" to a new chain

	s = reopen(t, s, path)
	defer s.Close()
	mustGet(t, s, "a", v2)
	mustGet(t, s, "b", payload(3, 50))
	l := mustVerify(t, s)
	if l.Records != 2 {
		t.Fatalf("expected 2 records after restart, got %d", l.Records)
	}
	// v1's chain (3 pages) must have been recycled.
	if l.Free != 3 {
		t.Fatalf("expected 3 recycled pages after restart, got %d", l.Free)
	}
}

func TestWriteFailureKeepsOldRecord(t *testing.T) {
	errInject := errors.New("injected write failure")
	oldData := payload(1, 900)
	newData := payload(2, 1500) // 7 pages

	failOn := func(op string, call int) func(string) error {
		n := 0
		return func(got string) error {
			if got == op {
				n++
				if n == call {
					return errInject
				}
			}
			return nil
		}
	}

	cases := []struct {
		name string
		hook func(string) error
	}{
		{"first chain page write", failOn("data", 1)},
		{"middle chain page write", failOn("data", 3)},
		{"data fsync", failOn("data", 8)}, // 7 page writes, 8th "data" op is the sync
		{"directory write", failOn("dir", 1)},
		{"directory rename", failOn("rename", 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, path := tempStore(t)
			defer s.Close()
			mustPut(t, s, "rec", oldData)
			freeBefore := mustVerify(t, s).Free

			s.failHook = tc.hook
			err := s.Put("rec", newData)
			if err == nil {
				t.Fatal("expected injected failure")
			}
			s.failHook = nil

			// Old record survives in the live store.
			mustGet(t, s, "rec", oldData)
			mustVerify(t, s)

			// Old record survives a restart; garbage is reclaimed.
			s = reopen(t, s, path)
			mustGet(t, s, "rec", oldData)
			l := mustVerify(t, s)
			if l.Free < freeBefore {
				t.Fatalf("free pages shrank across failure: %d -> %d", freeBefore, l.Free)
			}

			// The store is still fully usable afterwards.
			mustPut(t, s, "rec", newData)
			mustGet(t, s, "rec", newData)
			s = reopen(t, s, path)
			mustGet(t, s, "rec", newData)
			mustVerify(t, s)
		})
	}
}

func TestCrashBeforeRootSwitchReclaimsGarbage(t *testing.T) {
	s, path := tempStore(t)
	mustPut(t, s, "rec", payload(1, 300))

	// Simulate a crash mid-update: the new 9-page chain was written and
	// synced, then the process died before the root switch, so the
	// rollback never ran. Emulate by raw-appending the orphaned chain.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := f.Stat()
	base := uint64(st.Size() / PageSize)
	buf := make([]byte, PageSize)
	for i := uint64(0); i < 9; i++ {
		var next uint64
		if i+1 < 9 {
			next = base + i + 1
		}
		encodePage(buf, next, payload(2, 2000)[int(i)*pageCap:minInt((int(i)+1)*pageCap, 2000)])
		if _, err := f.WriteAt(buf, int64(base+i)*PageSize); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustGet(t, s, "rec", payload(1, 300))
	l := mustVerify(t, s)
	if l.Free != 9 { // the orphaned 9-page new chain
		t.Fatalf("expected 9 reclaimed garbage pages, got %d", l.Free)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func writeRawPage(t *testing.T, path string, pid uint64, next uint64, data []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, PageSize)
	encodePage(buf, next, data)
	if _, err := f.WriteAt(buf, int64(pid)*PageSize); err != nil {
		t.Fatal(err)
	}
}

func rewriteDir(t *testing.T, path string, roots map[string]uint64) {
	t.Helper()
	payload, err := json.Marshal(struct {
		Roots map[string]uint64 `json:"roots"`
	}{roots})
	if err != nil {
		t.Fatal(err)
	}
	if err := ioutil.WriteFile(path+".dir", payload, 0644); err != nil {
		t.Fatal(err)
	}
}

func expectCorrupt(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected corruption error, got nil")
	}
	if _, ok := err.(*CorruptError); !ok {
		t.Fatalf("expected *CorruptError, got %T: %v", err, err)
	}
}

func TestCycleDetected(t *testing.T) {
	s, path := tempStore(t)
	mustPut(t, s, "a", payload(5, 1000)) // 5 pages
	chain, err := s.walk(s.roots["a"])
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Point the last page back at the first: a cycle.
	writeRawPage(t, path, chain[len(chain)-1], chain[0], nil)

	_, err = Open(path)
	expectCorrupt(t, err)
}

func TestSharedPageDetected(t *testing.T) {
	s, path := tempStore(t)
	mustPut(t, s, "a", payload(1, 1000))
	mustPut(t, s, "b", payload(2, 500))
	chainA, err := s.walk(s.roots["a"])
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Illegally point record b's root into the middle of a's chain.
	rewriteDir(t, path, map[string]uint64{"a": chainA[0], "b": chainA[2]})

	_, err = Open(path)
	expectCorrupt(t, err)
}

func TestDanglingRootDetected(t *testing.T) {
	s, path := tempStore(t)
	mustPut(t, s, "a", payload(1, 100))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	rewriteDir(t, path, map[string]uint64{"a": 9999})
	_, err := Open(path)
	expectCorrupt(t, err)
}

// TestIndependentLedgerAndDigest re-derives the page accounting and the
// content digest straight from the raw file bytes, without using any
// Store traversal code, and cross-checks them against Verify.
func TestIndependentLedgerAndDigest(t *testing.T) {
	s, path := tempStore(t)
	defer s.Close()

	records := map[string][]byte{
		"alpha": payload(1, 1000),
		"beta":  payload(2, 10),
		"gamma": payload(3, 2*pageCap), // exact page multiple
	}
	for k, v := range records {
		mustPut(t, s, k, v)
	}
	mustPut(t, s, "alpha", payload(4, 100)) // shrink
	mustPut(t, s, "beta", nil)              // delete
	records["alpha"] = payload(4, 100)
	delete(records, "beta")

	l := mustVerify(t, s)

	// --- independent traversal of the raw file ---
	raw, err := ioutil.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw)%PageSize != 0 {
		t.Fatalf("file size %d not page aligned", len(raw))
	}
	totalPages := uint64(len(raw) / PageSize)
	if string(raw[0:8]) != superblockMagic {
		t.Fatal("bad superblock magic in raw file")
	}

	dirRaw, err := ioutil.ReadFile(path + ".dir")
	if err != nil {
		t.Fatal(err)
	}
	var dir struct {
		Roots map[string]uint64 `json:"roots"`
	}
	if err := json.Unmarshal(dirRaw, &dir); err != nil {
		t.Fatal(err)
	}

	readNext := func(pid uint64) (uint64, []byte) {
		off := int(pid) * PageSize
		page := raw[off : off+PageSize]
		if binary.LittleEndian.Uint32(page[0:4]) != pageMagic {
			t.Fatalf("page %d: bad magic", pid)
		}
		n := binary.LittleEndian.Uint32(page[12:16])
		return binary.LittleEndian.Uint64(page[4:12]), page[16 : 16+n]
	}

	used := make(map[uint64]bool)
	contents := make(map[string][]byte)
	for name, root := range dir.Roots {
		seen := make(map[uint64]bool)
		for pid := root; pid != 0; {
			if seen[pid] {
				t.Fatalf("independent walk: cycle at page %d", pid)
			}
			if used[pid] {
				t.Fatalf("independent walk: page %d shared", pid)
			}
			seen[pid] = true
			used[pid] = true
			next, data := readNext(pid)
			contents[name] = append(contents[name], data...)
			pid = next
		}
	}

	// Content matches what was written.
	for name, want := range records {
		if !bytes.Equal(contents[name], want) {
			t.Fatalf("record %q: content mismatch", name)
		}
	}

	// Page ledger cross-check.
	if l.Pages != totalPages {
		t.Fatalf("ledger pages %d != file pages %d", l.Pages, totalPages)
	}
	if l.Used != uint64(len(used)) {
		t.Fatalf("ledger used %d != independent %d", l.Used, len(used))
	}
	if l.Free != totalPages-1-uint64(len(used)) {
		t.Fatalf("ledger free %d != independent %d", l.Free, totalPages-1-uint64(len(used)))
	}
	if uint64(len(s.free)) != l.Free {
		t.Fatalf("in-memory free set %d != ledger free %d", len(s.free), l.Free)
	}
	if l.Records != len(records) {
		t.Fatalf("ledger records %d != %d", l.Records, len(records))
	}

	// Digest cross-check.
	h := sha256.New()
	names := make([]string, 0, len(records))
	for name := range records {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(records[name])
		h.Write([]byte{0})
	}
	var wantDigest [32]byte
	copy(wantDigest[:], h.Sum(nil))
	if l.Digest != wantDigest {
		t.Fatal("digest mismatch between Verify and independent computation")
	}
}

func TestCollectReclaimsAfterFailedUpdate(t *testing.T) {
	s, path := tempStore(t)
	defer s.Close()
	mustPut(t, s, "a", payload(1, 500))

	// Orphaned pages appear behind the store's back (crash leftover):
	// raw-append a 4-page chain and forget it ever happened.
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := f.Stat()
	base := uint64(st.Size() / PageSize)
	buf := make([]byte, PageSize)
	for i := uint64(0); i < 4; i++ {
		var next uint64
		if i+1 < 4 {
			next = base + i + 1
		}
		encodePage(buf, next, payload(2, 900)[int(i)*pageCap:minInt((int(i)+1)*pageCap, 900)])
		if _, err := f.WriteAt(buf, int64(base+i)*PageSize); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	s.pages += 4
	s.free = s.free[:0]

	n, err := s.Collect()
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 { // 900 bytes -> 4 orphaned pages
		t.Fatalf("Collect reclaimed %d pages, want 4", n)
	}
	mustGet(t, s, "a", payload(1, 500))
	mustVerify(t, s)
}

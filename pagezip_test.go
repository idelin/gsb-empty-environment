package pagezip

import (
	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

var (
	dictV1 = []byte("user order product sku region currency ")
	dictV2 = []byte("user order product sku region currency payment coupon ")
)

func tempDir(t *testing.T) (string, func()) {
	dir, err := ioutil.TempDir("", "pagezip")
	if err != nil {
		t.Fatal(err)
	}
	return dir, func() { os.RemoveAll(dir) }
}

func openStore(t *testing.T, dir string) *Store {
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func mustPutDict(t *testing.T, s *Store, id string, content []byte) DictRef {
	ref, err := s.PutDict(id, content)
	if err != nil {
		t.Fatalf("PutDict: %v", err)
	}
	return ref
}

func mustWritePage(t *testing.T, s *Store, pageID string, ref DictRef, data []byte) {
	if err := s.WritePage(pageID, ref, data); err != nil {
		t.Fatalf("WritePage(%s): %v", pageID, err)
	}
}

func mustReadPage(t *testing.T, s *Store, pageID string) []byte {
	data, err := s.ReadPage(pageID)
	if err != nil {
		t.Fatalf("ReadPage(%s): %v", pageID, err)
	}
	return data
}

func reopen(t *testing.T, s *Store, dir string) *Store {
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return openStore(t, dir)
}

func TestWriteReadRoundTrip(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()
	s := openStore(t, dir)
	defer s.Close()

	ref := mustPutDict(t, s, "shop", dictV1)
	want := []byte("user order product sku region currency 0001 0002 0003")
	mustWritePage(t, s, "p1", ref, want)
	if got := mustReadPage(t, s, "p1"); !bytes.Equal(got, want) {
		t.Fatalf("round trip mismatch: got %q", got)
	}
	if n := s.RefCount(ref); n != 1 {
		t.Fatalf("RefCount = %d, want 1", n)
	}
}

func TestOldDictKeptUntilPagesRewritten(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()
	s := openStore(t, dir)
	defer s.Close()

	v1 := mustPutDict(t, s, "shop", dictV1)
	v2 := mustPutDict(t, s, "shop", dictV2)
	mustWritePage(t, s, "p1", v1, []byte("page one"))
	mustWritePage(t, s, "p2", v1, []byte("page two"))

	// Switch only p1 to v2; p2 still needs v1.
	mustWritePage(t, s, "p1", v2, []byte("page one v2"))

	if err := s.DeleteDict(v1); err != ErrDictInUse {
		t.Fatalf("DeleteDict(v1) = %v, want ErrDictInUse", err)
	}
	reclaimed, err := s.Reclaim()
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	for _, r := range reclaimed {
		if r == v1 {
			t.Fatalf("Reclaim deleted v1 while p2 still references it")
		}
	}
	if got := mustReadPage(t, s, "p2"); string(got) != "page two" {
		t.Fatalf("p2 decode after reclaim: %q", got)
	}

	// Rewrite p2; now v1 is reclaimable.
	mustWritePage(t, s, "p2", v2, []byte("page two v2"))
	reclaimed, err = s.Reclaim()
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if !containsRef(reclaimed, v1) {
		t.Fatalf("Reclaim did not free v1 after all pages rewritten: %v", reclaimed)
	}
	if containsRef(reclaimed, v2) {
		t.Fatalf("Reclaim freed live v2: %v", reclaimed)
	}
	if _, err := s.GetDict(v1); !IsDictMissing(err) {
		t.Fatalf("GetDict(v1) after reclaim = %v, want DictMissingError", err)
	}
}

func TestDictSwitchInterruptAfterPageCommit(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()
	s := openStore(t, dir)

	v1 := mustPutDict(t, s, "shop", dictV1)
	v2 := mustPutDict(t, s, "shop", dictV2)
	mustWritePage(t, s, "p1", v1, []byte("before switch"))

	// Simulate a switch interrupted after the page file commit but before
	// the ledger setref append: the page file already binds v2.
	dict, err := s.GetDict(v2)
	if err != nil {
		t.Fatalf("GetDict: %v", err)
	}
	payload, err := compressWithDict(dict, []byte("after switch"))
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	blob := encodePage(pageHeader{dictID: v2.ID, digest: v2.Digest, payload: payload})
	if err := s.commitPageFile("p1", blob); err != nil {
		t.Fatalf("commitPageFile: %v", err)
	}

	// "Crash": close without the ledger entry, then recover.
	s = reopen(t, s, dir)
	defer s.Close()

	ref, ok := s.PageRef("p1")
	if !ok || ref != v2 {
		t.Fatalf("recovered PageRef = %v, %v; want v2", ref, ok)
	}
	if n := s.RefCount(v2); n != 1 {
		t.Fatalf("RefCount(v2) = %d, want 1", n)
	}
	if n := s.RefCount(v1); n != 0 {
		t.Fatalf("RefCount(v1) = %d, want 0", n)
	}
	if got := mustReadPage(t, s, "p1"); string(got) != "after switch" {
		t.Fatalf("recovered page = %q", got)
	}
	reclaimed, err := s.Reclaim()
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if !containsRef(reclaimed, v1) {
		t.Fatalf("v1 should be reclaimable after committed switch: %v", reclaimed)
	}
}

func TestDictSwitchInterruptBeforePageCommit(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()
	s := openStore(t, dir)

	v1 := mustPutDict(t, s, "shop", dictV1)
	v2 := mustPutDict(t, s, "shop", dictV2)
	mustWritePage(t, s, "p1", v1, []byte("still old"))

	// Crash before the switch wrote anything: p1 must keep v1 alive.
	s = reopen(t, s, dir)
	defer s.Close()

	ref, ok := s.PageRef("p1")
	if !ok || ref != v1 {
		t.Fatalf("recovered PageRef = %v, %v; want v1", ref, ok)
	}
	if err := s.DeleteDict(v1); err != ErrDictInUse {
		t.Fatalf("DeleteDict(v1) = %v, want ErrDictInUse", err)
	}
	reclaimed, err := s.Reclaim()
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if containsRef(reclaimed, v1) {
		t.Fatalf("v1 reclaimed while old page still bound: %v", reclaimed)
	}
	if !containsRef(reclaimed, v2) {
		t.Fatalf("unreferenced v2 should be reclaimed: %v", reclaimed)
	}
	if got := mustReadPage(t, s, "p1"); string(got) != "still old" {
		t.Fatalf("p1 = %q", got)
	}
}

func TestSameDictIDDifferentContent(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()
	s := openStore(t, dir)
	defer s.Close()

	refA := mustPutDict(t, s, "cfg", []byte("schema alpha fields"))
	refB := mustPutDict(t, s, "cfg", []byte("schema beta fields extended"))
	if refA.Digest == refB.Digest {
		t.Fatal("same ID different content must yield different digests")
	}

	mustWritePage(t, s, "pa", refA, []byte("schema alpha fields record 1"))
	mustWritePage(t, s, "pb", refB, []byte("schema beta fields extended record 2"))

	if got := mustReadPage(t, s, "pa"); string(got) != "schema alpha fields record 1" {
		t.Fatalf("pa = %q", got)
	}
	if got := mustReadPage(t, s, "pb"); string(got) != "schema beta fields extended record 2" {
		t.Fatalf("pb = %q", got)
	}

	// Deleting refB must not disturb refA's version and vice versa.
	if err := s.DeleteDict(refA); err != ErrDictInUse {
		t.Fatalf("DeleteDict(refA) = %v, want ErrDictInUse", err)
	}
	mustWritePage(t, s, "pa", refB, []byte("schema beta fields extended record 1"))
	if err := s.DeleteDict(refA); err != nil {
		t.Fatalf("DeleteDict(refA) after rewrite: %v", err)
	}
	if got := mustReadPage(t, s, "pb"); string(got) != "schema beta fields extended record 2" {
		t.Fatalf("pb after deleting sibling version = %q", got)
	}
}

func TestMissingDictNeverGuessedFromLatest(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()
	s := openStore(t, dir)
	defer s.Close()

	v1 := mustPutDict(t, s, "shop", dictV1)
	v2 := mustPutDict(t, s, "shop", dictV2)
	mustWritePage(t, s, "p1", v1, []byte("bound to v1"))
	mustWritePage(t, s, "p2", v2, []byte("bound to v2"))

	// Remove the exact v1 file out of band; a newer dictionary with the
	// same ID still exists and must NOT be used as a substitute.
	if err := os.Remove(filepath.Join(dir, dictFileName(v1))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadPage("p1"); !IsDictMissing(err) {
		t.Fatalf("ReadPage(p1) = %v, want DictMissingError", err)
	}
	if got := mustReadPage(t, s, "p2"); string(got) != "bound to v2" {
		t.Fatalf("p2 = %q", got)
	}
}

func TestCorruptDictReported(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()
	s := openStore(t, dir)
	defer s.Close()

	v1 := mustPutDict(t, s, "shop", dictV1)
	mustWritePage(t, s, "p1", v1, []byte("some page data"))
	flipByteInFile(t, filepath.Join(dir, dictFileName(v1)), 3)

	if _, err := s.ReadPage("p1"); !IsDictCorrupt(err) {
		t.Fatalf("ReadPage = %v, want DictCorruptError", err)
	}
	if _, err := s.GetDict(v1); !IsDictCorrupt(err) {
		t.Fatalf("GetDict = %v, want DictCorruptError", err)
	}
}

func TestCorruptPageReported(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()
	s := openStore(t, dir)
	defer s.Close()

	v1 := mustPutDict(t, s, "shop", dictV1)
	mustWritePage(t, s, "p1", v1, []byte("payload that will be damaged"))
	pagePath := filepath.Join(dir, "p1.page")

	flipByteInFile(t, pagePath, 10)
	if _, err := s.ReadPage("p1"); !IsPageCorrupt(err) {
		t.Fatalf("ReadPage(flipped) = %v, want PageCorruptError", err)
	}

	// Truncation is also page corruption, not a dictionary problem.
	blob, err := ioutil.ReadFile(pagePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := ioutil.WriteFile(pagePath, blob[:len(blob)/2], 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadPage("p1"); !IsPageCorrupt(err) {
		t.Fatalf("ReadPage(truncated) = %v, want PageCorruptError", err)
	}
}

func TestIndependentLedgerReplay(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()
	s := openStore(t, dir)

	v1 := mustPutDict(t, s, "shop", dictV1)
	v2 := mustPutDict(t, s, "shop", dictV2)
	mustWritePage(t, s, "p1", v1, []byte("one"))
	mustWritePage(t, s, "p2", v1, []byte("two"))
	mustWritePage(t, s, "p1", v2, []byte("one switched"))
	mustWritePage(t, s, "p3", v2, []byte("three"))
	if err := s.DeletePage("p3"); err != nil {
		t.Fatalf("DeletePage: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Independently replay the ledger against the on-disk page files.
	lf, err := os.Open(filepath.Join(dir, ledgerName))
	if err != nil {
		t.Fatal(err)
	}
	headers, present, err := ScanPageHeaders(dir)
	if err != nil {
		t.Fatal(err)
	}
	pageRefs, counts, err := ReplayLedger(lf, headers, present)
	lf.Close()
	if err != nil {
		t.Fatalf("ReplayLedger: %v", err)
	}

	s = openStore(t, dir)
	defer s.Close()
	if !reflect.DeepEqual(pageRefs, s.pageRefs) {
		t.Fatalf("independent replay pageRefs %v != store %v", pageRefs, s.pageRefs)
	}
	if !reflect.DeepEqual(counts, s.refCount) {
		t.Fatalf("independent replay counts %v != store %v", counts, s.refCount)
	}
	if counts[v1] != 1 || counts[v2] != 1 {
		t.Fatalf("counts = %v, want one page per version", counts)
	}
}

func TestTornLedgerTailTolerated(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()
	s := openStore(t, dir)

	v1 := mustPutDict(t, s, "shop", dictV1)
	mustWritePage(t, s, "p1", v1, []byte("durable"))
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Append a half-written record, as a crashed append would leave.
	f, err := os.OpenFile(filepath.Join(dir, ledgerName), os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"op":"setref","page":"pz","dict":"shop","dig`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	s = openStore(t, dir)
	defer s.Close()
	if got := mustReadPage(t, s, "p1"); string(got) != "durable" {
		t.Fatalf("p1 = %q", got)
	}
	if n := s.RefCount(v1); n != 1 {
		t.Fatalf("RefCount(v1) = %d, want 1", n)
	}
}

func TestDeletePageReleasesReference(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()
	s := openStore(t, dir)
	defer s.Close()

	v1 := mustPutDict(t, s, "shop", dictV1)
	mustWritePage(t, s, "p1", v1, []byte("x"))
	if err := s.DeletePage("p1"); err != nil {
		t.Fatalf("DeletePage: %v", err)
	}
	if n := s.RefCount(v1); n != 0 {
		t.Fatalf("RefCount after DeletePage = %d, want 0", n)
	}
	if _, err := s.ReadPage("p1"); err != ErrPageNotFound {
		t.Fatalf("ReadPage after delete = %v, want ErrPageNotFound", err)
	}
	if err := s.DeleteDict(v1); err != nil {
		t.Fatalf("DeleteDict after DeletePage: %v", err)
	}
}

func containsRef(refs []DictRef, want DictRef) bool {
	for _, r := range refs {
		if r == want {
			return true
		}
	}
	return false
}

func flipByteInFile(t *testing.T, path string, off int) {
	blob, err := ioutil.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if off >= len(blob) {
		t.Fatalf("offset %d beyond file size %d", off, len(blob))
	}
	blob[off] ^= 0xFF
	if err := ioutil.WriteFile(path, blob, 0644); err != nil {
		t.Fatal(err)
	}
}

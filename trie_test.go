package triestore

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openTemp(t *testing.T, opts *Options) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir, opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s, dir
}

func mustPut(t *testing.T, s *Store, k, v string) Root {
	t.Helper()
	r, err := s.Put([]byte(k), []byte(v))
	if err != nil {
		t.Fatalf("Put(%q): %v", k, err)
	}
	return r
}

func mustGet(t *testing.T, s *Store, k string) (string, bool) {
	t.Helper()
	v, ok, err := s.Get([]byte(k))
	if err != nil {
		t.Fatalf("Get(%q): %v", k, err)
	}
	return string(v), ok
}

func countNodeFiles(t *testing.T, dir string) int {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(dir, "nodes"))
	if err != nil {
		t.Fatalf("ReadDir nodes: %v", err)
	}
	n := 0
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".node") {
			n++
		}
	}
	return n
}

func TestPutGetEmptyAndBinaryKeys(t *testing.T) {
	s, _ := openTemp(t, nil)

	// Empty key lives at the root node.
	if _, err := s.Put([]byte{}, []byte("root-value")); err != nil {
		t.Fatal(err)
	}
	// Binary keys with 0x00 / 0xff bytes.
	binKey := []byte{0x00, 0xff, 0x10, 0x00}
	if _, err := s.Put(binKey, []byte{0xde, 0xad, 0xbe, 0xef}); err != nil {
		t.Fatal(err)
	}
	mustPut(t, s, "hello", "world")

	v, ok, err := s.Get(nil)
	if err != nil || !ok || string(v) != "root-value" {
		t.Fatalf("empty key: v=%q ok=%v err=%v", v, ok, err)
	}
	bv, ok, err := s.Get(binKey)
	if err != nil || !ok || !bytes.Equal(bv, []byte{0xde, 0xad, 0xbe, 0xef}) {
		t.Fatalf("binary key: v=%x ok=%v err=%v", bv, ok, err)
	}
	if got, ok := mustGet(t, s, "hello"); !ok || got != "world" {
		t.Fatalf("hello: %q ok=%v", got, ok)
	}
	if _, ok := mustGet(t, s, "missing"); ok {
		t.Fatal("missing key reported found")
	}
}

func TestDeleteAbKeepsAbc(t *testing.T) {
	s, _ := openTemp(t, nil)
	mustPut(t, s, "ab", "1")
	mustPut(t, s, "abc", "2")
	mustPut(t, s, "abd", "3")

	found, err := s.Delete([]byte("ab"))
	if err != nil || !found {
		t.Fatalf("Delete(ab): found=%v err=%v", found, err)
	}
	if _, ok := mustGet(t, s, "ab"); ok {
		t.Fatal("ab should be gone")
	}
	if got, ok := mustGet(t, s, "abc"); !ok || got != "2" {
		t.Fatalf("abc must survive deleting ab: %q ok=%v", got, ok)
	}
	if got, ok := mustGet(t, s, "abd"); !ok || got != "3" {
		t.Fatalf("abd must survive deleting ab: %q ok=%v", got, ok)
	}

	// Deleting a missing key reports found=false and commits nothing.
	seqBefore := s.Seq()
	found, err = s.Delete([]byte("ab"))
	if err != nil || found {
		t.Fatalf("re-Delete(ab): found=%v err=%v", found, err)
	}
	if s.Seq() != seqBefore {
		t.Fatal("deleting a missing key must not commit a new root")
	}
}

func TestDeleteCompressionPrunesEmptyBranches(t *testing.T) {
	s, _ := openTemp(t, nil)
	mustPut(t, s, "abc", "1")
	mustPut(t, s, "xyz", "2")

	// Delete abc: the a->b->c chain has no other values, so it collapses.
	if _, err := s.Delete([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	keys, err := s.Keys(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !EqualKeys(keys, "xyz") {
		t.Fatalf("after pruning, keys=%q", keys)
	}

	// Delete the last key: the whole tree compresses back to empty.
	if _, err := s.Delete([]byte("xyz")); err != nil {
		t.Fatal(err)
	}
	if s.RootID() != emptyID {
		t.Fatal("root should be empty after deleting everything")
	}
	keys, _ = s.Keys(nil)
	if len(keys) != 0 {
		t.Fatalf("empty tree still has keys: %q", keys)
	}
}

func TestHistoricalRootsStayReadable(t *testing.T) {
	s, _ := openTemp(t, nil)
	r1 := mustPut(t, s, "k", "v1")
	r2 := mustPut(t, s, "k", "v2")
	r3 := mustPut(t, s, "k2", "x")
	if _, err := s.Delete([]byte("k2")); err != nil {
		t.Fatal(err)
	}

	// Current root sees the latest value.
	if got, ok := mustGet(t, s, "k"); !ok || got != "v2" {
		t.Fatalf("current k=%q ok=%v", got, ok)
	}
	// Historical roots still serve their old values.
	v, ok, err := s.GetAt(r1, []byte("k"))
	if err != nil || !ok || string(v) != "v1" {
		t.Fatalf("root@%d k=%q ok=%v err=%v", r1.Seq, v, ok, err)
	}
	v, ok, err = s.GetAt(r2, []byte("k"))
	if err != nil || !ok || string(v) != "v2" {
		t.Fatalf("root@%d k=%q ok=%v err=%v", r2.Seq, v, ok, err)
	}
	if _, ok, _ = s.GetAt(r3, []byte("k2")); !ok {
		t.Fatalf("root@%d should still contain k2", r3.Seq)
	}
	if _, ok := mustGet(t, s, "k2"); ok {
		t.Fatal("k2 should be deleted in current root")
	}
}

func TestPathCopyingWritesOnlyOnePath(t *testing.T) {
	s, dir := openTemp(t, nil)
	mustPut(t, s, "aaaa", "1")
	before := countNodeFiles(t, dir)

	// Insert a leaf that diverges only at the last byte. Path copying must
	// copy exactly the nodes on the path root->a->aa->aaa->aaab (5 nodes)
	// and share the entire "aaaa" subtree chain with the old root.
	mustPut(t, s, "aaab", "2")
	after := countNodeFiles(t, dir)
	if delta := after - before; delta != len("aaab")+1 {
		t.Fatalf("expected %d new node files, got %d", len("aaab")+1, delta)
	}

	// Rewriting the same leaf with the same value writes nothing new.
	before = after
	mustPut(t, s, "aaab", "2")
	if got := countNodeFiles(t, dir); got != before {
		t.Fatalf("idempotent put wrote %d new files", got-before)
	}

	// And the old root still resolves the old tree.
	roots, err := s.Roots()
	if err != nil {
		t.Fatal(err)
	}
	v, ok, err := s.GetAt(roots[0], []byte("aaaa"))
	if err != nil || !ok || string(v) != "1" {
		t.Fatalf("old root lost aaaa: %q ok=%v err=%v", v, ok, err)
	}
	if _, ok, _ = s.GetAt(roots[0], []byte("aaab")); ok {
		t.Fatal("old root should not see aaab")
	}
}

func TestPrefixScan(t *testing.T) {
	s, _ := openTemp(t, nil)
	for _, kv := range [][2]string{
		{"app", "1"}, {"apple", "2"}, {"application", "3"},
		{"banana", "4"}, {"band", "5"}, {"", "0"},
	} {
		mustPut(t, s, kv[0], kv[1])
	}

	keys, err := s.Keys([]byte("app"))
	if err != nil {
		t.Fatal(err)
	}
	if !EqualKeys(keys, "app", "apple", "application") {
		t.Fatalf("prefix app: %q", keys)
	}

	// Full scan is lexicographic; empty key sorts first.
	keys, _ = s.Keys(nil)
	if !EqualKeys(keys, "", "app", "apple", "application", "banana", "band") {
		t.Fatalf("full scan: %q", keys)
	}

	// Early stop after the first hit.
	var first []byte
	n := 0
	err = s.Scan([]byte("ban"), func(k, v []byte) bool {
		first = append([]byte(nil), k...)
		n++
		return false
	})
	if err != nil || n != 1 || string(first) != "banana" {
		t.Fatalf("early stop: first=%q n=%d err=%v", first, n, err)
	}

	// Prefix with no matches.
	keys, _ = s.Keys([]byte("zzz"))
	if len(keys) != 0 {
		t.Fatalf("unexpected keys: %q", keys)
	}
}

func TestCorruptNodeIsLocated(t *testing.T) {
	s, dir := openTemp(t, nil)
	mustPut(t, s, "alpha", "1")
	mustPut(t, s, "alpine", "2")
	mustPut(t, s, "beta", "3")

	// Corrupt one node file on disk (flip a payload byte, keep the CRC stale).
	ents, err := os.ReadDir(filepath.Join(dir, "nodes"))
	if err != nil {
		t.Fatal(err)
	}
	var victim string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".node") {
			victim = strings.TrimSuffix(e.Name(), ".node")
			break
		}
	}
	p := filepath.Join(dir, "nodes", victim+".node")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	data[10] ^= 0xff
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}

	// Verify must fail and name the corrupt node ID.
	err = s.Verify()
	if err == nil {
		t.Fatal("Verify should fail on a corrupted node")
	}
	if !strings.Contains(err.Error(), victim) {
		t.Fatalf("error should locate node %s, got: %v", victim, err)
	}
}

func TestCrashBetweenNodeWriteAndRootCommit(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustPut(t, s, "stable", "1")

	// Simulate a crash: nodes for the new write reach disk, but the process
	// "dies" before the root is committed.
	crash := errors.New("simulated crash")
	s.faults = &Faults{AfterNodeWrite: func(id string) error { return crash }}
	if _, err := s.Put([]byte("lost"), []byte("2")); !errors.Is(err, crash) {
		t.Fatalf("expected injected crash, got %v", err)
	}

	// Reopen = process restart. Recovery must find the last committed root.
	s2, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := mustGet(t, s2, "stable"); !ok || got != "1" {
		t.Fatalf("committed data lost: %q ok=%v", got, ok)
	}
	if _, ok := mustGet(t, s2, "lost"); ok {
		t.Fatal("uncommitted write must not survive recovery")
	}
	// Orphan node files from the crashed write are expected and harmless.
	if countNodeFiles(t, dir) == 0 {
		t.Fatal("sanity: nodes dir should not be empty")
	}
}

func TestCrashAtRootRenameKeepsPreviousRoot(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustPut(t, s, "a", "1")

	crash := errors.New("power cut before rename")
	s.faults = &Faults{BeforeRootRename: func(seq uint64) error { return crash }}
	if _, err := s.Put([]byte("b"), []byte("2")); !errors.Is(err, crash) {
		t.Fatalf("expected injected crash, got %v", err)
	}

	s2, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := mustGet(t, s2, "b"); ok {
		t.Fatal("root rename never happened; b must be invisible")
	}
	// The leftover temp root file must be ignored by recovery.
	ents, _ := os.ReadDir(filepath.Join(dir, "roots"))
	tmpSeen := false
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			tmpSeen = true
		}
	}
	if !tmpSeen {
		t.Fatal("expected a leftover .tmp root file from the crash")
	}
}

func TestRecoverySkipsTornRootFile(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustPut(t, s, "good", "1")

	// A torn root file with a higher sequence number (partial write).
	torn := filepath.Join(dir, "roots", "000002.root")
	if err := os.WriteFile(torn, []byte("TRR1\x01\x02\x03"), 0o644); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Seq() != 1 {
		t.Fatalf("recovery adopted torn root, seq=%d", s2.Seq())
	}
	if got, ok := mustGet(t, s2, "good"); !ok || got != "1" {
		t.Fatalf("fell back to last valid root: %q ok=%v", got, ok)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, &Options{Sync: true})
	if err != nil {
		t.Fatal(err)
	}
	mustPut(t, s, "persist", "me")
	mustPut(t, s, "and", "me too")

	s2, err := Open(dir, &Options{Sync: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := mustGet(t, s2, "persist"); !ok || got != "me" {
		t.Fatalf("after reopen: %q ok=%v", got, ok)
	}
	if err := s2.Verify(); err != nil {
		t.Fatalf("Verify after reopen: %v", err)
	}
	roots, err := s2.Roots()
	if err != nil || len(roots) != 2 {
		t.Fatalf("roots=%v err=%v", roots, err)
	}
}

func TestCorruptNodeDataInjection(t *testing.T) {
	dir := t.TempDir()
	// Inject media corruption on write: every node lands on disk corrupted.
	f := &Faults{CorruptNodeData: func(id string, data []byte) []byte {
		cp := append([]byte(nil), data...)
		cp[len(cp)/2] ^= 0x01
		return cp
	}}
	s, err := Open(dir, &Options{Faults: f})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put([]byte("doomed"), []byte("x")); err != nil {
		t.Fatal(err) // writes succeed; corruption is only detected on read
	}
	s.faults = nil
	err = s.Verify()
	if err == nil || !strings.Contains(err.Error(), "corrupt node") {
		t.Fatalf("expected located corruption, got %v", err)
	}
}

func TestScanAtHistoricalRoot(t *testing.T) {
	s, _ := openTemp(t, nil)
	r1 := mustPut(t, s, "a1", "1")
	mustPut(t, s, "a2", "2")

	var keys []string
	err := s.ScanAt(r1, []byte("a"), func(k, v []byte) bool {
		keys = append(keys, string(k))
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(keys) != "[a1]" {
		t.Fatalf("historical scan got %v", keys)
	}
}

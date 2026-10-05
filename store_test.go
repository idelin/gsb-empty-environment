package pagestore

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var (
	dictV1 = []byte("the quick brown fox jumps over the lazy dog")
	dictV2 = []byte("pack my box with five dozen liquor jugs")
)

func payloadOf(s string) []byte {
	return []byte(strings.Repeat(s+" ", 64))
}

func openStore(t *testing.T, dir string) (*Store, *RecoveryReport) {
	t.Helper()
	s, report, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s, report
}

func mustRegister(t *testing.T, s *Store, id uint64, content []byte) DictKey {
	t.Helper()
	key, err := s.RegisterDictionary(id, content)
	if err != nil {
		t.Fatalf("RegisterDictionary: %v", err)
	}
	return key
}

func mustWrite(t *testing.T, s *Store, pageID uint64, key DictKey, raw []byte) {
	t.Helper()
	if err := s.WritePage(pageID, key, raw); err != nil {
		t.Fatalf("WritePage(%d): %v", pageID, err)
	}
}

func mustRead(t *testing.T, s *Store, pageID uint64) []byte {
	t.Helper()
	raw, err := s.ReadPage(pageID)
	if err != nil {
		t.Fatalf("ReadPage(%d): %v", pageID, err)
	}
	return raw
}

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, _ := openStore(t, dir)
	k1 := mustRegister(t, s, 1, dictV1)

	p1, p2 := payloadOf("the quick brown fox"), payloadOf("lazy dog")
	mustWrite(t, s, 1, k1, p1)
	mustWrite(t, s, 2, k1, p2)

	if got := mustRead(t, s, 1); !reflect.DeepEqual(got, p1) {
		t.Fatalf("page 1 mismatch")
	}
	if got := mustRead(t, s, 2); !reflect.DeepEqual(got, p2) {
		t.Fatalf("page 2 mismatch")
	}
	if n := s.RefCount(k1); n != 2 {
		t.Fatalf("RefCount = %d, want 2", n)
	}
	s.Close()

	// State survives a restart via ledger replay.
	s2, report := openStore(t, dir)
	defer s2.Close()
	if len(report.IncompletePages) != 0 || len(report.CorruptDicts) != 0 || report.LedgerTruncatedBytes != 0 {
		t.Fatalf("unexpected recovery report: %+v", report)
	}
	if got := mustRead(t, s2, 1); !reflect.DeepEqual(got, p1) {
		t.Fatalf("page 1 mismatch after reopen")
	}
	if n := s2.RefCount(k1); n != 2 {
		t.Fatalf("RefCount after reopen = %d, want 2", n)
	}
}

func TestOldPagesPinOldDictionary(t *testing.T) {
	dir := t.TempDir()
	s, _ := openStore(t, dir)
	defer s.Close()
	k1 := mustRegister(t, s, 7, dictV1)
	k2 := mustRegister(t, s, 7, dictV2)

	oldPayload := payloadOf("the quick brown fox")
	mustWrite(t, s, 1, k1, oldPayload)               // stays on v1
	mustWrite(t, s, 2, k2, payloadOf("pack my box")) // new page on v2

	// Nothing is reclaimable: page 1 still pins the old version.
	collected, err := s.Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(collected) != 0 {
		t.Fatalf("Collect reclaimed %v while old page still pins v1", collected)
	}

	// Rewrite page 1 onto v2 (dictionary switch); now v1 is reclaimable.
	mustWrite(t, s, 1, k2, payloadOf("pack my box with five dozen liquor jugs"))
	collected, err = s.Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(collected) != 1 || collected[0] != k1 {
		t.Fatalf("Collect reclaimed %v, want [%v]", collected, k1)
	}
	if _, err := os.Stat(filepath.Join(dir, "dicts", k1.fileName())); !os.IsNotExist(err) {
		t.Fatalf("old dictionary file still present")
	}
	if got := mustRead(t, s, 2); !reflect.DeepEqual(got, payloadOf("pack my box")) {
		t.Fatalf("page 2 mismatch after collect")
	}
}

func TestInterruptedDictionarySwitch(t *testing.T) {
	dir := t.TempDir()
	s, _ := openStore(t, dir)
	k1 := mustRegister(t, s, 7, dictV1)
	k2 := mustRegister(t, s, 7, dictV2)
	committed := payloadOf("the quick brown fox")
	mustWrite(t, s, 1, k1, committed)

	// Crash mid-switch of page 2 to v2: the page file landed (step 1) but
	// the ledger BIND record was only partially appended (step 2).
	payload, err := compressWithDict(payloadOf("pack my box"), dictV2)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if err := os.WriteFile(s.pagePath(2), encodePage(2, k2, payload), 0o644); err != nil {
		t.Fatalf("plant orphan page: %v", err)
	}
	torn := encodeBindRecord(2, k2)[:7] // torn tail of an uncommitted record
	if _, err := s.ledger.Write(torn); err != nil {
		t.Fatalf("plant torn ledger tail: %v", err)
	}
	s.ledger.Sync()
	s.Close()

	s2, report := openStore(t, dir)
	defer s2.Close()

	// Recovery: orphan page reported, torn ledger tail truncated.
	if !reflect.DeepEqual(report.IncompletePages, []uint64{2}) {
		t.Fatalf("IncompletePages = %v, want [2]", report.IncompletePages)
	}
	if report.LedgerTruncatedBytes != len(torn) {
		t.Fatalf("LedgerTruncatedBytes = %d, want %d", report.LedgerTruncatedBytes, len(torn))
	}

	// The interrupted switch is invisible: page 2 has no committed binding.
	if _, err := s2.ReadPage(2); !errors.Is(err, ErrPageNotFound) {
		t.Fatalf("ReadPage(2) = %v, want ErrPageNotFound", err)
	}
	// The committed page still decodes with the old dictionary.
	if got := mustRead(t, s2, 1); !reflect.DeepEqual(got, committed) {
		t.Fatalf("page 1 mismatch after recovery")
	}

	// Reclamation: v2 was never committed to any page, so it is
	// reclaimable; v1 is still pinned by page 1 and must survive.
	collected, err := s2.Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !reflect.DeepEqual(collected, []DictKey{k2}) {
		t.Fatalf("Collect reclaimed %v, want [%v]", collected, k2)
	}
	if got := mustRead(t, s2, 1); !reflect.DeepEqual(got, committed) {
		t.Fatalf("page 1 mismatch after collect")
	}
}

func TestSameDictionaryIDDifferentContent(t *testing.T) {
	dir := t.TempDir()
	s, _ := openStore(t, dir)
	k1 := mustRegister(t, s, 7, dictV1)
	k2 := mustRegister(t, s, 7, dictV2)

	if k1.ID != k2.ID {
		t.Fatalf("expected shared dictionary ID")
	}
	if k1 == k2 {
		t.Fatalf("same ID with different content must yield distinct version keys")
	}

	p1, p2 := payloadOf("the quick brown fox"), payloadOf("pack my box")
	mustWrite(t, s, 1, k1, p1)
	mustWrite(t, s, 2, k2, p2)
	s.Close()

	s2, _ := openStore(t, dir)
	defer s2.Close()
	if got := mustRead(t, s2, 1); !reflect.DeepEqual(got, p1) {
		t.Fatalf("page 1 decoded with wrong dictionary version")
	}
	if got := mustRead(t, s2, 2); !reflect.DeepEqual(got, p2) {
		t.Fatalf("page 2 decoded with wrong dictionary version")
	}

	// Retire v1: rewrite page 1 onto v2 and collect. A page still pinned to
	// the retired version must report the version as missing, never fall
	// back to the "latest" dictionary with the same ID.
	mustWrite(t, s2, 1, k2, payloadOf("pack my box"))
	if _, err := s2.Collect(); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	// Plant a page committed to the retired version (file + ledger record,
	// as if written before the crash that lost the dictionary file).
	payload, err := compressWithDict(p1, dictV1)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if err := os.WriteFile(s2.pagePath(9), encodePage(9, k1, payload), 0o644); err != nil {
		t.Fatalf("plant page: %v", err)
	}
	if err := s2.appendLedger(encodeBindRecord(9, k1)); err != nil {
		t.Fatalf("plant binding: %v", err)
	}
	s2.bindings[9] = k1
	s2.refs[k1]++

	_, err = s2.ReadPage(9)
	if !errors.Is(err, ErrDictionaryMissing) {
		t.Fatalf("ReadPage(9) = %v, want ErrDictionaryMissing (no latest-dictionary guessing)", err)
	}
	// The live v2 page is unaffected.
	if got := mustRead(t, s2, 2); !reflect.DeepEqual(got, p2) {
		t.Fatalf("page 2 mismatch")
	}
}

func TestMissingCorruptDictionaryCorruptPageAreDistinct(t *testing.T) {
	dir := t.TempDir()
	s, _ := openStore(t, dir)
	kMissing := mustRegister(t, s, 1, dictV1)
	kCorrupt := mustRegister(t, s, 2, dictV2)
	kOK := mustRegister(t, s, 3, []byte("how vexingly quick daft zebras jump"))
	mustWrite(t, s, 1, kMissing, payloadOf("the quick brown fox"))
	mustWrite(t, s, 2, kCorrupt, payloadOf("pack my box"))
	mustWrite(t, s, 3, kOK, payloadOf("how vexingly quick daft zebras jump"))
	s.Close()

	// Lose one dictionary file entirely, tamper with another, and flip a
	// byte inside the third page's payload.
	if err := os.Remove(filepath.Join(dir, "dicts", kMissing.fileName())); err != nil {
		t.Fatalf("remove dict: %v", err)
	}
	corruptPath := filepath.Join(dir, "dicts", kCorrupt.fileName())
	content, err := os.ReadFile(corruptPath)
	if err != nil {
		t.Fatalf("read dict: %v", err)
	}
	content[0] ^= 0xFF
	if err := os.WriteFile(corruptPath, content, 0o644); err != nil {
		t.Fatalf("tamper dict: %v", err)
	}
	pagePath := filepath.Join(dir, "pages", "page_3.pg")
	pageData, err := os.ReadFile(pagePath)
	if err != nil {
		t.Fatalf("read page: %v", err)
	}
	pageData[pageHeaderSize] ^= 0xFF
	if err := os.WriteFile(pagePath, pageData, 0o644); err != nil {
		t.Fatalf("tamper page: %v", err)
	}

	s2, report := openStore(t, dir)
	defer s2.Close()
	if !reflect.DeepEqual(report.CorruptDicts, []DictKey{kCorrupt}) {
		t.Fatalf("CorruptDicts = %v, want [%v]", report.CorruptDicts, kCorrupt)
	}

	if _, err := s2.ReadPage(1); !errors.Is(err, ErrDictionaryMissing) {
		t.Fatalf("missing dictionary: got %v, want ErrDictionaryMissing", err)
	}
	if _, err := s2.ReadPage(2); !errors.Is(err, ErrDictionaryCorrupt) {
		t.Fatalf("corrupt dictionary: got %v, want ErrDictionaryCorrupt", err)
	}
	if _, err := s2.ReadPage(3); !errors.Is(err, ErrPageCorrupt) {
		t.Fatalf("corrupt page: got %v, want ErrPageCorrupt", err)
	}

	// The three classes are mutually exclusive.
	for _, tc := range []struct {
		err   error
		notIs error
	}{
		{ErrDictionaryMissing, ErrDictionaryCorrupt},
		{ErrDictionaryMissing, ErrPageCorrupt},
		{ErrDictionaryCorrupt, ErrDictionaryMissing},
		{ErrDictionaryCorrupt, ErrPageCorrupt},
		{ErrPageCorrupt, ErrDictionaryMissing},
		{ErrPageCorrupt, ErrDictionaryCorrupt},
	} {
		if errors.Is(tc.err, tc.notIs) {
			t.Fatalf("%v must not match %v", tc.err, tc.notIs)
		}
	}

	// A corrupt dictionary is not reclaimable while a page still pins it.
	collected, err := s2.Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, key := range collected {
		if key == kCorrupt {
			t.Fatalf("corrupt dictionary reclaimed while still referenced")
		}
	}
}

func TestIndependentLedgerReplay(t *testing.T) {
	dir := t.TempDir()
	s, _ := openStore(t, dir)
	k1 := mustRegister(t, s, 1, dictV1)
	k2 := mustRegister(t, s, 1, dictV2)
	mustWrite(t, s, 1, k1, payloadOf("the quick brown fox"))
	mustWrite(t, s, 2, k1, payloadOf("lazy dog"))
	mustWrite(t, s, 3, k2, payloadOf("pack my box"))
	mustWrite(t, s, 2, k2, payloadOf("five dozen liquor jugs")) // switch page 2 to v2
	if err := s.DeletePage(3); err != nil {
		t.Fatalf("DeletePage: %v", err)
	}
	s.Close()

	// Replay the ledger independently of any Store instance.
	state, validBytes, err := ReplayLedger(filepath.Join(dir, "refs.log"))
	if err != nil {
		t.Fatalf("ReplayLedger: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "refs.log"))
	if err != nil {
		t.Fatalf("stat ledger: %v", err)
	}
	if int64(validBytes) != info.Size() {
		t.Fatalf("validBytes = %d, ledger size = %d (unexpected torn tail)", validBytes, info.Size())
	}

	wantBindings := map[uint64]DictKey{1: k1, 2: k2}
	if !reflect.DeepEqual(state.Bindings, wantBindings) {
		t.Fatalf("Bindings = %v, want %v", state.Bindings, wantBindings)
	}
	wantRefs := map[DictKey]int{k1: 1, k2: 1}
	if !reflect.DeepEqual(state.Refs, wantRefs) {
		t.Fatalf("Refs = %v, want %v", state.Refs, wantRefs)
	}
}

func TestWritePageWithUnknownDictionary(t *testing.T) {
	dir := t.TempDir()
	s, _ := openStore(t, dir)
	defer s.Close()
	ghost := MakeDictKey(42, []byte("never registered"))
	if err := s.WritePage(1, ghost, payloadOf("boo")); !errors.Is(err, ErrDictionaryMissing) {
		t.Fatalf("WritePage with unregistered dictionary = %v, want ErrDictionaryMissing", err)
	}
}

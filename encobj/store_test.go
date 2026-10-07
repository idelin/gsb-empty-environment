package encobj

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	testKey1 = sha256.Sum256([]byte("encobj test key one"))
	testKey2 = sha256.Sum256([]byte("encobj test key two"))
	testKey3 = sha256.Sum256([]byte("encobj test key three"))
)

func openTestStore(t *testing.T, dir string, opts *Options) *Store {
	t.Helper()
	if opts == nil {
		opts = &Options{NonceReservationBlock: 4}
	}
	if opts.Keys == nil {
		opts.Keys = map[string][]byte{"k1": testKey1[:]}
		opts.ActiveKey = "k1"
	}
	st, err := Open(NewFSStorage(dir), opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return st
}

func mustCommit(t *testing.T, st *Store, obj string, op Operation) uint64 {
	t.Helper()
	v, err := st.Commit(obj, op)
	if err != nil {
		t.Fatalf("Commit(%s, %s): %v", obj, op.OpID, err)
	}
	return v
}

func mustSnapshot(t *testing.T, st *Store, obj string) *Snapshot {
	t.Helper()
	sn, err := st.Snapshot(obj)
	if err != nil {
		t.Fatalf("Snapshot(%s): %v", obj, err)
	}
	return sn
}

func TestCommitReadUpdateDelete(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir, nil)
	defer st.Close()

	v := mustCommit(t, st, "obj1", Operation{OpID: "op1", Set: map[string][]byte{
		"a": []byte("alpha"), "b": []byte("beta"),
	}})
	if v != 1 {
		t.Fatalf("version = %d, want 1", v)
	}
	sn := mustSnapshot(t, st, "obj1")
	if sn.Version() != 1 {
		t.Fatalf("snapshot version = %d", sn.Version())
	}
	if got, _ := sn.Get("a"); string(got) != "alpha" {
		t.Fatalf("a = %q", got)
	}
	sn.Close()

	v = mustCommit(t, st, "obj1", Operation{OpID: "op2", Set: map[string][]byte{"a": []byte("ALPHA")}, Del: []string{"b"}})
	if v != 2 {
		t.Fatalf("version = %d, want 2", v)
	}
	sn = mustSnapshot(t, st, "obj1")
	if got, _ := sn.Get("a"); string(got) != "ALPHA" {
		t.Fatalf("a = %q", got)
	}
	if _, ok := sn.Get("b"); ok {
		t.Fatal("b should be deleted")
	}
	sn.Close()

	if err := st.Delete("obj1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.Snapshot("obj1"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("Snapshot after delete: %v", err)
	}
	if err := st.Delete("obj1"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("second Delete: %v", err)
	}
}

func TestIdempotentReplayAndConflict(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir, nil)
	defer st.Close()

	op := Operation{OpID: "opX", Set: map[string][]byte{"a": []byte("1")}}
	v1 := mustCommit(t, st, "o", op)
	v2 := mustCommit(t, st, "o", op) // replay
	if v1 != v2 {
		t.Fatalf("replay returned %d, want %d", v2, v1)
	}
	// A real second commit bumps the version; replay must not.
	v3 := mustCommit(t, st, "o", Operation{OpID: "opY", Set: map[string][]byte{"b": []byte("2")}})
	if v3 != v1+1 {
		t.Fatalf("v3 = %d, want %d", v3, v1+1)
	}
	if v := mustCommit(t, st, "o", op); v != v1 {
		t.Fatalf("late replay = %d, want %d", v, v1)
	}
	// Same op ID, different content: conflict.
	_, err := st.Commit("o", Operation{OpID: "opX", Set: map[string][]byte{"a": []byte("different")}})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict err = %v", err)
	}
}

func TestUnchangedFieldSharesCiphertext(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir, nil)
	defer st.Close()

	mustCommit(t, st, "o", Operation{OpID: "op1", Set: map[string][]byte{
		"keep": []byte("stable"), "chg": []byte("v1"),
	}})
	o := st.obj("o")
	o.mu.Lock()
	var keepFileV1 string
	var keepRevV1 uint64
	for _, f := range o.m.Fields {
		if f.Name == "keep" {
			keepFileV1, keepRevV1 = f.File, f.Revision
		}
	}
	o.mu.Unlock()

	mustCommit(t, st, "o", Operation{OpID: "op2", Set: map[string][]byte{"chg": []byte("v2")}})
	o.mu.Lock()
	defer o.mu.Unlock()
	var keep, chg fieldRef
	for _, f := range o.m.Fields {
		switch f.Name {
		case "keep":
			keep = f
		case "chg":
			chg = f
		}
	}
	if keep.File != keepFileV1 || keep.Revision != keepRevV1 {
		t.Fatalf("unchanged field re-encrypted: %+v vs %s r%d", keep, keepFileV1, keepRevV1)
	}
	if chg.Revision != 2 {
		t.Fatalf("changed field revision = %d, want 2", chg.Revision)
	}
	// The superseded changed-field ciphertext must be reclaimed: exactly
	// the two live field files remain.
	names, _ := st.st.ReadFileNames()
	efCount := 0
	for _, n := range names {
		if strings.HasPrefix(n, objPrefix("o")+"f") {
			efCount++
		}
	}
	if efCount != 2 {
		t.Fatalf("field files on disk = %d, want 2", efCount)
	}
}

func TestKeyRegistryRules(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir, nil)
	defer st.Close()

	if err := st.RegisterKey("k1", testKey2[:]); !errors.Is(err, ErrKeyExists) {
		t.Fatalf("rebind k1: %v", err)
	}
	if err := st.RegisterKey("k1", testKey1[:]); err != nil {
		t.Fatalf("idempotent register: %v", err)
	}
	if err := st.RegisterKey("k2", testKey1[:]); !errors.Is(err, ErrKeyMaterialReused) {
		t.Fatalf("same material new ID: %v", err)
	}
	if err := st.SetActiveKey("nope"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("activate unknown: %v", err)
	}
	if err := st.RetireKey("k1"); !errors.Is(err, ErrKeyInUse) {
		t.Fatalf("retire active: %v", err)
	}
}

func TestNonceReservationRestartSkips(t *testing.T) {
	dir := t.TempDir()
	var usedMu sync.Mutex
	used := map[[12]byte]string{}

	st := openTestStore(t, dir, &Options{NonceReservationBlock: 4})
	st.hookEncrypt = func(keyID string, n [12]byte) {
		usedMu.Lock()
		if who, dup := used[n]; dup {
			t.Errorf("nonce reuse: %v (%s)", n, who)
		}
		used[n] = keyID
		usedMu.Unlock()
	}
	// 3 encryptions: 2 fields + 1 manifest. Reservation block 4 -> hi=4,
	// one value (3) left unused in the interval.
	mustCommit(t, st, "o", Operation{OpID: "op1", Set: map[string][]byte{
		"a": []byte("1"), "b": []byte("2"),
	}})
	st.Close()

	st2 := openTestStore(t, dir, &Options{NonceReservationBlock: 4})
	defer st2.Close()
	st2.hookEncrypt = func(keyID string, n [12]byte) {
		usedMu.Lock()
		if who, dup := used[n]; dup {
			t.Errorf("nonce reuse after restart: %v (%s)", n, who)
		}
		used[n] = keyID
		usedMu.Unlock()
	}
	mustCommit(t, st2, "o", Operation{OpID: "op2", Set: map[string][]byte{"c": []byte("3")}})

	usedMu.Lock()
	defer usedMu.Unlock()
	// After restart the allocator must skip the unused tail of the first
	// interval: nonces 0,1,2 used before restart, 3 wasted, 4,5 used after.
	want := map[uint64]bool{0: true, 1: true, 2: true, 4: true, 5: true}
	if len(used) != len(want) {
		t.Fatalf("used %d nonces, want %d (%v)", len(used), len(want), used)
	}
	for n := range used {
		v := uint64(n[8]) // tiny counters in this test
		if !want[v] {
			t.Fatalf("unexpected nonce counter %d (wasted value reused?)", v)
		}
	}
}

func TestNonceExhaustion(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir, &Options{NonceReservationBlock: 2, MaxNonce: 3})
	defer st.Close()
	// First commit needs 2 nonces (field + manifest). Second needs 2 more
	// but only 1 remains.
	mustCommit(t, st, "o", Operation{OpID: "op1", Set: map[string][]byte{"a": []byte("1")}})
	_, err := st.Commit("o", Operation{OpID: "op2", Set: map[string][]byte{"b": []byte("2")}})
	if !errors.Is(err, ErrNonceExhausted) {
		t.Fatalf("err = %v, want ErrNonceExhausted", err)
	}
}

func TestWriterLockExcludesSecondOpen(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir, nil)
	defer st.Close()
	_, err := Open(NewFSStorage(dir), nil)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("second Open err = %v, want ErrLocked", err)
	}
}

func TestDeleteKeepsPinnedFilesUntilSnapshotClose(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir, nil)
	defer st.Close()
	mustCommit(t, st, "o", Operation{OpID: "op1", Set: map[string][]byte{"a": []byte("1")}})

	sn := mustSnapshot(t, st, "o")
	if err := st.Delete("o"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Files pinned by the snapshot must still exist.
	names, _ := st.st.ReadFileNames()
	foundField := false
	for _, n := range names {
		if filepath.Ext(n) == ".ef" {
			foundField = true
		}
	}
	if !foundField {
		t.Fatal("pinned field file removed while snapshot open")
	}
	sn.Close()
	names, _ = st.st.ReadFileNames()
	for _, n := range names {
		if filepath.Ext(n) == ".ef" || filepath.Ext(n) == ".mf" {
			t.Fatalf("file %s survives after snapshot close on deleted object", n)
		}
	}
}

func TestNoPlaintextOnDisk(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir, nil)
	secret := []byte("TOP-SECRET-VALUE-xyzzy")
	mustCommit(t, st, "o", Operation{OpID: "op1", Set: map[string][]byte{"s": secret}})
	st.Close()
	// Keys are caller-provided and must never touch the directory.
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		b, _ := os.ReadFile(p)
		if bytes.Contains(b, secret) || bytes.Contains(b, testKey1[:]) {
			t.Errorf("plaintext or key material found in %s", p)
		}
		return nil
	})
}

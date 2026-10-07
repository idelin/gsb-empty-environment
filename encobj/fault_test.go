package encobj

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// crashStore arms a one-shot crash at the named persistence point. After
// the crash the store refuses all further work, modelling process death;
// everything fsynced before the point survives on disk.
func crashStore(st *Store, point string, fired *bool) {
	*fired = false
	st.hookPersist = func(p string) error {
		if p == point && !*fired {
			*fired = true
			st.mu.Lock()
			st.crashed = true
			st.mu.Unlock()
			return ErrCrashed
		}
		return nil
	}
}

// collectOnDiskNonces parses every sealed file in the directory and returns
// all nonces found, plus the reserved high-water from nonce.log.
func collectOnDiskNonces(t *testing.T, dir string) (nonces [][12]byte, reservedHi uint64) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		switch {
		case e.Name() == "nonce.log":
			for off := 0; off+nonceRecLen <= len(b); off += nonceRecLen {
				hi := binary.LittleEndian.Uint64(b[off+4:])
				if hi > reservedHi {
					reservedHi = hi
				}
			}
		case strings.HasSuffix(e.Name(), ".ef") || strings.HasSuffix(e.Name(), ".mf"):
			d := &decoder{buf: b}
			d.str() // magic
			d.str() // key ID
			nonce := d.bytes()
			if d.err == nil && len(nonce) == 12 {
				var n [12]byte
				copy(n[:], nonce)
				nonces = append(nonces, n)
			}
		}
	}
	return nonces, reservedHi
}

// TestCrashAtEveryPersistPoint injects process death before each durable
// step of the commit protocol, then replays recovery. After every crash the
// object must come back either fully at the old version or fully at the new
// one; idempotency records and the nonce set must stay consistent.
func TestCrashAtEveryPersistPoint(t *testing.T) {
	points := []string{
		"nonce.reserve",
		"field.write",
		"manifest.write",
		"current.write",
		"ops.append",
	}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			dir := t.TempDir()
			st := openTestStore(t, dir, &Options{
				NonceReservationBlock: 1, // force a reservation per encryption
				Keys:                  map[string][]byte{"k1": testKey1[:]},
				ActiveKey:             "k1",
			})
			v1 := mustCommit(t, st, "o", Operation{OpID: "base", Set: map[string][]byte{
				"a": []byte("A1"), "b": []byte("B1"),
			}})
			if v1 != 1 {
				t.Fatalf("v1 = %d", v1)
			}

			var fired bool
			crashStore(st, point, &fired)
			newOp := Operation{OpID: "op2", Set: map[string][]byte{
				"a": []byte("A2"), "c": []byte("C2"),
			}, Del: []string{"b"}}
			_, err := st.Commit("o", newOp)
			if !errors.Is(err, ErrCrashed) {
				t.Fatalf("commit err = %v, want ErrCrashed", err)
			}
			if !fired {
				t.Fatalf("crash point %q never fired", point)
			}
			st.Close() // releases the lock; a real crash would too

			// Replay recovery.
			st2 := openTestStore(t, dir, &Options{
				NonceReservationBlock: 1,
				Keys:                  map[string][]byte{"k1": testKey1[:]},
				ActiveKey:             "k1",
			})
			defer st2.Close()
			sn, err := st2.Snapshot("o")
			if err != nil {
				t.Fatalf("recovered snapshot: %v", err)
			}
			switch sn.Version() {
			case 1: // uncommitted: fully old
				if got, _ := sn.Get("a"); string(got) != "A1" {
					t.Fatalf("half-committed state: a=%q", got)
				}
			case 2: // fully committed
				if got, _ := sn.Get("a"); string(got) != "A2" {
					t.Fatalf("half-committed state: a=%q", got)
				}
				if _, ok := sn.Get("b"); ok {
					t.Fatal("half-committed state: b survives")
				}
			default:
				t.Fatalf("impossible version %d", sn.Version())
			}
			sn.Close()

			// Idempotency: retrying the crashed op must either commit it
			// now (as v2) or replay the already-committed v2 — never
			// conflict, never double-apply.
			v, err := st2.Commit("o", newOp)
			if err != nil {
				t.Fatalf("retry after crash: %v", err)
			}
			if v != 2 {
				t.Fatalf("retry version = %d, want 2", v)
			}
			if v := mustCommit(t, st2, "o", newOp); v != 2 {
				t.Fatalf("second replay = %d, want 2", v)
			}
			sn = mustSnapshot(t, st2, "o")
			if sn.Version() != 2 {
				t.Fatalf("final version = %d", sn.Version())
			}
			if got, _ := sn.Get("a"); string(got) != "A2" {
				t.Fatalf("final a = %q", got)
			}
			if _, ok := sn.Get("b"); ok {
				t.Fatal("final state: b survives")
			}
			if got, _ := sn.Get("c"); string(got) != "C2" {
				t.Fatalf("final c = %q", got)
			}
			sn.Close()

			// Nonce invariants across the crash boundary: every nonce on
			// disk is unique and below a persisted reservation bound.
			nonces, hi := collectOnDiskNonces(t, dir)
			seen := map[[12]byte]bool{}
			for _, n := range nonces {
				if seen[n] {
					t.Fatalf("nonce %v used twice on disk", n)
				}
				seen[n] = true
				v := binary.BigEndian.Uint64(n[4:])
				if v >= hi {
					t.Fatalf("nonce %d used beyond reserved high-water %d", v, hi)
				}
			}
		})
	}
}

// TestCrashDuringRotation ensures a crashed rotation leaves either the old
// or the new key state fully intact.
func TestCrashDuringRotation(t *testing.T) {
	for _, point := range []string{"rotate.field.write", "rotate.manifest.write", "rotate.current.write"} {
		t.Run(point, func(t *testing.T) {
			dir := t.TempDir()
			st := openTestStore(t, dir, nil)
			mustCommit(t, st, "o", Operation{OpID: "op1", Set: map[string][]byte{
				"a": []byte("A"), "b": []byte("B"),
			}})
			if err := st.RegisterKey("k2", testKey2[:]); err != nil {
				t.Fatal(err)
			}
			if err := st.SetActiveKey("k2"); err != nil {
				t.Fatal(err)
			}
			var fired bool
			crashStore(st, point, &fired)
			_, err := st.RotateObject("o")
			if !errors.Is(err, ErrCrashed) {
				t.Fatalf("rotate err = %v", err)
			}
			st.Close()

			st2 := openTestStore(t, dir, &Options{
				NonceReservationBlock: 4,
				Keys:                  map[string][]byte{"k1": testKey1[:], "k2": testKey2[:]},
				ActiveKey:             "k2",
			})
			defer st2.Close()
			sn := mustSnapshot(t, st2, "o")
			if sn.Version() != 1 {
				t.Fatalf("version = %d, want 1 (rotation never bumps)", sn.Version())
			}
			if got, _ := sn.Get("a"); string(got) != "A" {
				t.Fatalf("a = %q", got)
			}
			sn.Close()
			// Rotation can be retried to completion.
			if _, err := st2.RotateAll(); err != nil {
				t.Fatalf("retry RotateAll: %v", err)
			}
			sn = mustSnapshot(t, st2, "o")
			if got, _ := sn.Get("b"); string(got) != "B" {
				t.Fatalf("b = %q after re-rotation", got)
			}
			sn.Close()
		})
	}
}

func TestNonceLogTornTailTruncated(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir, nil)
	mustCommit(t, st, "o", Operation{OpID: "op1", Set: map[string][]byte{"a": []byte("1")}})
	st.Close()

	// Append a half-written reservation record.
	f, err := os.OpenFile(filepath.Join(dir, "nonce.log"), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0xde, 0xad, 0xbe}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	st2 := openTestStore(t, dir, nil)
	defer st2.Close()
	mustCommit(t, st2, "o", Operation{OpID: "op2", Set: map[string][]byte{"b": []byte("2")}})
	sn := mustSnapshot(t, st2, "o")
	if got, _ := sn.Get("b"); string(got) != "2" {
		t.Fatalf("b = %q", got)
	}
	sn.Close()
	// The log was rewritten without the torn bytes.
	b, _ := os.ReadFile(filepath.Join(dir, "nonce.log"))
	if len(b)%nonceRecLen != 0 {
		t.Fatalf("nonce.log length %d not record-aligned", len(b))
	}
}

func TestNonceLogMidCorruptionRejected(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir, &Options{NonceReservationBlock: 1, Keys: map[string][]byte{"k1": testKey1[:]}, ActiveKey: "k1"})
	// Force several reservation records.
	for i := 0; i < 3; i++ {
		mustCommit(t, st, "o", Operation{OpID: "op" + string(rune('a'+i)), Set: map[string][]byte{"a": []byte("1")}})
	}
	st.Close()

	p := filepath.Join(dir, "nonce.log")
	b, _ := os.ReadFile(p)
	if len(b) < 2*nonceRecLen {
		t.Fatalf("need >=2 records, have %d bytes", len(b))
	}
	b[nonceRecLen+5] ^= 0xff // corrupt the middle record
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(NewFSStorage(dir), &Options{Keys: map[string][]byte{"k1": testKey1[:]}, ActiveKey: "k1"})
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open err = %v, want ErrCorrupt", err)
	}
}

func TestOpsLogTornTailAndMidCorruption(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir, nil)
	mustCommit(t, st, "o", Operation{OpID: "op1", Set: map[string][]byte{"a": []byte("1")}})
	mustCommit(t, st, "o", Operation{OpID: "op2", Set: map[string][]byte{"a": []byte("2")}})
	st.Close()

	p := filepath.Join(dir, "ops.log")
	good, _ := os.ReadFile(p)

	// Torn tail: truncate garbage appended.
	f, _ := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0o600)
	f.Write([]byte{1, 2, 3, 4, 5})
	f.Close()
	st2 := openTestStore(t, dir, nil)
	if v := mustCommit(t, st2, "o", Operation{OpID: "op1", Set: map[string][]byte{"a": []byte("1")}}); v != 1 {
		t.Fatalf("replay after torn tail = %d, want 1", v)
	}
	st2.Close()

	// Mid corruption: flip a byte in the first record's crc.
	bad := append([]byte(nil), good...)
	bad[0] ^= 0xff
	if err := os.WriteFile(p, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(NewFSStorage(dir), &Options{Keys: map[string][]byte{"k1": testKey1[:]}, ActiveKey: "k1"})
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open err = %v, want ErrCorrupt", err)
	}
}

func TestStrayTmpFilesReclaimedOnRecovery(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir, nil)
	mustCommit(t, st, "o", Operation{OpID: "op1", Set: map[string][]byte{"a": []byte("1")}})
	st.Close()
	// Simulate staging files left by a crash inside WriteFileAtomic.
	if err := os.WriteFile(filepath.Join(dir, objPrefix("o")+"f0-r1-x.ef.tmp-deadbeef"), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	st2 := openTestStore(t, dir, nil)
	defer st2.Close()
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("staging file %s survived recovery", e.Name())
		}
	}
	sn := mustSnapshot(t, st2, "o")
	if got, _ := sn.Get("a"); string(got) != "1" {
		t.Fatalf("a = %q", got)
	}
	sn.Close()
}

func TestOpsLogRecordCRC(t *testing.T) {
	// Sanity: a record round-trips and crc covers the payload.
	rec := encodeOpRec(7, "op", [32]byte{1, 2, 3})
	if crc32.ChecksumIEEE(rec[4:]) != binary.LittleEndian.Uint32(rec[:4]) {
		t.Fatal("record crc mismatch")
	}
}

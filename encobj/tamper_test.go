package encobj

import (
	"errors"
	"strings"
	"testing"
)

// tamperFixture builds an object with fields a,b,c at version 2 and returns
// the store plus the on-disk names of its files.
func tamperFixture(t *testing.T) (string, *Store) {
	t.Helper()
	dir := t.TempDir()
	st := openTestStore(t, dir, nil)
	mustCommit(t, st, "o", Operation{OpID: "op1", Set: map[string][]byte{
		"a": []byte("va1"), "b": []byte("vb1"), "c": []byte("vc1"),
	}})
	mustCommit(t, st, "o", Operation{OpID: "op2", Set: map[string][]byte{
		"a": []byte("va2"),
	}})
	return dir, st
}

func objFiles(t *testing.T, st *Store, obj string) map[string]string {
	t.Helper()
	names, err := st.st.ReadFileNames()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, n := range names {
		if strings.HasPrefix(n, objPrefix(obj)) {
			_, file, _ := splitObjFile(n)
			out[file] = n
		}
	}
	return out
}

func writeRaw(t *testing.T, st *Store, name string, data []byte) {
	t.Helper()
	if err := st.st.WriteFileAtomic(name, data); err != nil {
		t.Fatal(err)
	}
}

func readRaw(t *testing.T, st *Store, name string) []byte {
	t.Helper()
	b, err := st.st.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// expectRejected asserts the read fails and delivers no snapshot at all.
func expectRejected(t *testing.T, st *Store, obj string) {
	t.Helper()
	sn, err := st.Snapshot(obj)
	if err == nil {
		sn.Close()
		t.Fatal("tampered object read succeeded")
	}
	if sn != nil {
		t.Fatal("partial snapshot escaped on failed read")
	}
}

func TestFieldCiphertextMovedToOtherField(t *testing.T) {
	_, st := tamperFixture(t)
	defer st.Close()
	files := objFiles(t, st, "o")
	var fa, fb string
	for f := range files {
		if strings.HasPrefix(f, "f0-") { // field a (id 0)
			fa = f
		}
		if strings.HasPrefix(f, "f1-") { // field b (id 1)
			fb = f
		}
	}
	// Overwrite b's file with a's valid ciphertext.
	writeRaw(t, st, objPrefix("o")+fb, readRaw(t, st, objPrefix("o")+fa))
	expectRejected(t, st, "o")
}

func TestFieldFileDeleted(t *testing.T) {
	_, st := tamperFixture(t)
	defer st.Close()
	files := objFiles(t, st, "o")
	for f := range files {
		if strings.HasPrefix(f, "f1-") {
			if err := st.st.RemoveFile(objPrefix("o") + f); err != nil {
				t.Fatal(err)
			}
		}
	}
	expectRejected(t, st, "o")
}

func TestManifestReplacedWithOld(t *testing.T) {
	_, st := tamperFixture(t)
	defer st.Close()
	files := objFiles(t, st, "o")
	var curManifest string
	for f := range files {
		if strings.HasPrefix(f, "m") {
			curManifest = f
		}
	}
	oldManifest := readRaw(t, st, objPrefix("o")+curManifest) // v2 manifest
	// Commit v3 so the v2 manifest becomes stale; it is reclaimed, so the
	// attacker re-injects it under the current manifest's name.
	mustCommit(t, st, "o", Operation{OpID: "op3", Set: map[string][]byte{"b": []byte("vb3")}})
	files = objFiles(t, st, "o")
	for f := range files {
		if strings.HasPrefix(f, "m") {
			writeRaw(t, st, objPrefix("o")+f, oldManifest)
		}
	}
	expectRejected(t, st, "o")
}

func TestCurrentRecordRolledBack(t *testing.T) {
	_, st := tamperFixture(t)
	defer st.Close()
	// Save the v2 current record and manifest.
	savedCur := readRaw(t, st, objPrefix("o")+"current")
	files := objFiles(t, st, "o")
	var m2 string
	for f := range files {
		if strings.HasPrefix(f, "m") {
			m2 = f
		}
	}
	savedManifest := readRaw(t, st, objPrefix("o")+m2)
	// Commit v3, then roll the index back to v2 and restore its manifest.
	mustCommit(t, st, "o", Operation{OpID: "op3", Set: map[string][]byte{"b": []byte("vb3")}})
	writeRaw(t, st, objPrefix("o")+m2, savedManifest)
	writeRaw(t, st, objPrefix("o")+"current", savedCur)
	// Rejected against the local committed-version index, not AEAD
	// freshness: the rolled-back manifest would authenticate fine.
	err := func() error {
		sn, err := st.Snapshot("o")
		if sn != nil {
			sn.Close()
		}
		return err
	}()
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("rollback err = %v, want ErrCorrupt", err)
	}
}

func TestLastFieldCorrupted(t *testing.T) {
	_, st := tamperFixture(t)
	defer st.Close()
	files := objFiles(t, st, "o")
	var last string
	for f := range files {
		if strings.HasPrefix(f, "f") && strings.HasSuffix(f, ".ef") && f > last {
			last = f
		}
	}
	body := readRaw(t, st, objPrefix("o")+last)
	body[len(body)-1] ^= 0x01
	writeRaw(t, st, objPrefix("o")+last, body)
	expectRejected(t, st, "o")
}

func TestOldFieldCiphertextReplacesNew(t *testing.T) {
	_, st := tamperFixture(t)
	defer st.Close()
	// v2 changed field a: its v1 ciphertext was reclaimed. Rebuild the
	// attack by saving v2, committing v3 changing a again, then
	// re-injecting the v2 ciphertext under the v3 file name.
	files := objFiles(t, st, "o")
	var aV2 string
	for f := range files {
		if strings.HasPrefix(f, "f0-") {
			aV2 = f
		}
	}
	savedA := readRaw(t, st, objPrefix("o")+aV2)
	mustCommit(t, st, "o", Operation{OpID: "op3", Set: map[string][]byte{"a": []byte("va3")}})
	files = objFiles(t, st, "o")
	for f := range files {
		if strings.HasPrefix(f, "f0-") {
			writeRaw(t, st, objPrefix("o")+f, savedA)
		}
	}
	expectRejected(t, st, "o")
}

func TestExtraFieldFileInjected(t *testing.T) {
	_, st := tamperFixture(t)
	defer st.Close()
	files := objFiles(t, st, "o")
	var fa string
	for f := range files {
		if strings.HasPrefix(f, "f0-") {
			fa = f
		}
	}
	// A stranger field file not committed by the manifest must be ignored
	// by reads and reclaimed by GC on the next commit — never delivered.
	writeRaw(t, st, objPrefix("o")+"f99-r1-deadbeef.ef", readRaw(t, st, objPrefix("o")+fa))
	sn := mustSnapshot(t, st, "o")
	if _, ok := sn.Get("stranger"); ok {
		t.Fatal("uncommitted field appeared in snapshot")
	}
	sn.Close()
	mustCommit(t, st, "o", Operation{OpID: "op3", Set: map[string][]byte{"d": []byte("vd")}})
	files = objFiles(t, st, "o")
	if _, ok := files["f99-r1-deadbeef.ef"]; ok {
		t.Fatal("stranger field file not reclaimed")
	}
}

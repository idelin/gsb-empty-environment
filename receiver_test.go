package snaprecv

import (
	"bytes"
	"crypto/sha256"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func tempDir(t *testing.T) string {
	t.Helper()
	d, err := ioutil.TempDir("", "snaprecv-test-")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// content deterministically derives snapshot bytes for a version.
func content(version uint64, size int) []byte {
	b := make([]byte, size)
	for i := range b {
		b[i] = byte(i*131 + int(version)*17)
	}
	return b
}

func open(t *testing.T, dir string) *Receiver {
	t.Helper()
	r, err := Open(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mustRead(t *testing.T, r *Receiver) ([]byte, uint64) {
	t.Helper()
	b, v, err := r.Read()
	if err != nil {
		t.Fatal(err)
	}
	return b, v
}

func TestOutOfOrderInstall(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	r := open(t, dir)

	want := content(1, 100)
	if _, _, err := r.Read(); err != ErrNoSnapshot {
		t.Fatalf("before install: got %v, want ErrNoSnapshot", err)
	}

	// 30-byte chunks plus a 10-byte tail, delivered shuffled.
	var chunks []Chunk
	for off := 0; off < 100; off += 30 {
		end := off + 30
		if end > 100 {
			end = 100
		}
		chunks = append(chunks, NewChunk(1, uint64(off), 100, want[off:end]))
	}
	rand.New(rand.NewSource(7)).Shuffle(len(chunks), func(i, j int) {
		chunks[i], chunks[j] = chunks[j], chunks[i]
	})
	for i, c := range chunks {
		if err := r.HandleChunk(c); err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
		if i < len(chunks)-1 {
			if _, _, err := r.Read(); err != ErrNoSnapshot {
				t.Fatalf("chunk %d: visible before complete: %v", i, err)
			}
		}
	}

	got, v := mustRead(t, r)
	if v != 1 || !bytes.Equal(got, want) {
		t.Fatalf("installed v%d len=%d, want v1 len=%d", v, len(got), len(want))
	}

	// Retransmitting an installed version's chunk is stale, not fatal.
	if err := r.HandleChunk(chunks[0]); err != ErrStaleVersion {
		t.Fatalf("retransmit: got %v, want ErrStaleVersion", err)
	}
}

func TestStaleVersionNeverOverwrites(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	r := open(t, dir)

	v2 := content(2, 60)
	for off := 0; off < 60; off += 60 {
		if err := r.HandleChunk(NewChunk(2, uint64(off), 60, v2[off:off+60])); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := mustRead(t, r); !bytes.Equal(got, v2) {
		t.Fatal("v2 not installed")
	}

	// Old-version responses must not touch staging or visible state.
	if err := r.HandleChunk(NewChunk(1, 0, 10, content(1, 10))); err != ErrStaleVersion {
		t.Fatalf("v1 chunk: got %v, want ErrStaleVersion", err)
	}
	if err := r.HandleChunk(NewChunk(2, 0, 60, content(9, 60))); err != ErrStaleVersion {
		t.Fatalf("v2 replay: got %v, want ErrStaleVersion", err)
	}
	if r.staged != 0 {
		t.Fatalf("stale chunk created staging for v%d", r.staged)
	}
	if got, v := mustRead(t, r); v != 2 || !bytes.Equal(got, v2) {
		t.Fatal("visible state changed by stale chunks")
	}
}

func TestHashMismatchRejected(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	r := open(t, dir)

	c := NewChunk(1, 0, 50, content(1, 50)[:50])
	c.Data[3] ^= 0xff // corrupt after hashing
	if err := r.HandleChunk(c); err != ErrHashMismatch {
		t.Fatalf("got %v, want ErrHashMismatch", err)
	}
	if len(r.intervals) != 0 {
		t.Fatal("corrupt chunk recorded as verified")
	}
	// A correct redelivery is accepted.
	if err := r.HandleChunk(NewChunk(1, 0, 50, content(1, 50)[:50])); err != nil {
		t.Fatal(err)
	}
}

func TestOverlapConflictAndRetransmit(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	r := open(t, dir)

	want := content(1, 100)
	if err := r.HandleChunk(NewChunk(1, 0, 100, want[:50])); err != nil {
		t.Fatal(err)
	}

	// Overlapping chunk whose shared bytes disagree -> conflict.
	badData := append([]byte(nil), want[25:75]...)
	bad := NewChunk(1, 25, 100, badData)
	bad.Data[10] ^= 0xff
	bad.Hash = sha256.Sum256(bad.Data)
	if err := r.HandleChunk(bad); err != ErrConflict {
		t.Fatalf("got %v, want ErrConflict", err)
	}

	// Identical retransmission of the verified range is fine.
	if err := r.HandleChunk(NewChunk(1, 10, 100, want[10:40])); err != nil {
		t.Fatalf("retransmit: %v", err)
	}

	// Agreeing overlap extends coverage and completes the snapshot.
	if err := r.HandleChunk(NewChunk(1, 40, 100, want[40:])); err != nil {
		t.Fatal(err)
	}
	got, v := mustRead(t, r)
	if v != 1 || !bytes.Equal(got, want) {
		t.Fatal("snapshot content mismatch after overlapping chunks")
	}
}

func TestTailChunkBounds(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	r := open(t, dir)

	want := content(1, 100)
	if err := r.HandleChunk(NewChunk(1, 0, 100, want[:90])); err != nil {
		t.Fatal(err)
	}
	// Tail overrunning the declared total size is invalid.
	over := NewChunk(1, 90, 100, append(append([]byte{}, want[90:]...), 0, 0, 0))
	if err := r.HandleChunk(over); err != ErrInvalidChunk {
		t.Fatalf("got %v, want ErrInvalidChunk", err)
	}
	// Exact tail completes the snapshot.
	if err := r.HandleChunk(NewChunk(1, 90, 100, want[90:])); err != nil {
		t.Fatal(err)
	}
	if got, _ := mustRead(t, r); !bytes.Equal(got, want) {
		t.Fatal("content mismatch")
	}
}

func TestTotalSizeDisagreement(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	r := open(t, dir)

	if err := r.HandleChunk(NewChunk(1, 0, 100, content(1, 100)[:50])); err != nil {
		t.Fatal(err)
	}
	// Same version, different declared total size -> conflict.
	if err := r.HandleChunk(NewChunk(1, 50, 200, content(1, 200)[50:100])); err != ErrConflict {
		t.Fatalf("got %v, want ErrConflict", err)
	}
}

func TestNewerVersionDiscardsStaging(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	r := open(t, dir)

	if err := r.HandleChunk(NewChunk(1, 0, 50, content(1, 50)[:25])); err != nil {
		t.Fatal(err)
	}
	// A newer version abandons the older staged one.
	v2 := content(2, 40)
	if err := r.HandleChunk(NewChunk(2, 0, 40, v2)); err != nil {
		t.Fatal(err)
	}
	if got, v := mustRead(t, r); v != 2 || !bytes.Equal(got, v2) {
		t.Fatal("v2 not installed")
	}
	// The abandoned v1 is now stale too.
	if err := r.HandleChunk(NewChunk(1, 25, 50, content(1, 50)[25:])); err != ErrStaleVersion {
		t.Fatalf("got %v, want ErrStaleVersion", err)
	}
}

func TestRecoveryReusesVerifiedChunks(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	r := open(t, dir)

	want := content(1, 100)
	if err := r.HandleChunk(NewChunk(1, 0, 100, want[:30])); err != nil {
		t.Fatal(err)
	}
	if err := r.HandleChunk(NewChunk(1, 60, 100, want[60:])); err != nil {
		t.Fatal(err)
	}

	// Simulate a crash: abandon the receiver, reopen on the same dir.
	r2 := open(t, dir)
	if r2.staged != 1 || r2.total != 100 {
		t.Fatalf("staged=%d total=%d, want 1/100", r2.staged, r2.total)
	}
	if len(r2.intervals) != 2 {
		t.Fatalf("intervals=%v, want 2 verified ranges", r2.intervals)
	}
	if _, _, err := r2.Read(); err != ErrNoSnapshot {
		t.Fatal("nothing should be visible yet")
	}

	// Only the missing middle chunk is needed; verified chunks are reused.
	if err := r2.HandleChunk(NewChunk(1, 30, 100, want[30:60])); err != nil {
		t.Fatal(err)
	}
	got, v := mustRead(t, r2)
	if v != 1 || !bytes.Equal(got, want) {
		t.Fatal("install after recovery has wrong content")
	}
}

func TestRecoveryDropsStaleStaging(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	r := open(t, dir)

	v1 := content(1, 20)
	if err := r.HandleChunk(NewChunk(1, 0, 20, v1)); err != nil {
		t.Fatal(err)
	}
	// Stage v2 partially, then "crash".
	if err := r.HandleChunk(NewChunk(2, 0, 30, content(2, 30)[:10])); err != nil {
		t.Fatal(err)
	}
	// Reopen and install v2 fully from a fresh sender retransmission.
	r2 := open(t, dir)
	v2 := content(2, 30)
	if err := r2.HandleChunk(NewChunk(2, 0, 30, v2)); err != nil {
		t.Fatal(err)
	}
	// Stage v3 partially, crash, then a *newer* v4 arrives after recovery.
	if err := r2.HandleChunk(NewChunk(3, 0, 30, content(3, 30)[:10])); err != nil {
		t.Fatal(err)
	}
	r3 := open(t, dir)
	if r3.staged != 3 {
		t.Fatalf("staged=%d, want 3", r3.staged)
	}
	if err := r3.HandleChunk(NewChunk(4, 0, 30, content(4, 30))); err != nil {
		t.Fatal(err)
	}
	// Leftover v3 staging must not resurrect.
	r4 := open(t, dir)
	if r4.staged != 0 {
		t.Fatalf("staged=%d, want 0", r4.staged)
	}
	if got, v := mustRead(t, r4); v != 4 || !bytes.Equal(got, content(4, 30)) {
		t.Fatal("visible state wrong")
	}
	// staging dir for superseded versions must be gone.
	if _, err := os.Stat(filepath.Join(dir, "staging")); !os.IsNotExist(err) {
		t.Fatal("stale staging dir left behind")
	}
}

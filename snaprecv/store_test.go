package snaprecv

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"testing"
)

// buildSnapshot makes a deterministic pseudo-random snapshot and its Meta.
func buildSnapshot(seed int64, size int, chunkSize int64, version uint64) ([]byte, Meta) {
	rng := rand.New(rand.NewSource(seed))
	data := make([]byte, size)
	rng.Read(data)
	meta := Meta{Version: version, Size: int64(size), ChunkSize: chunkSize}
	for off := int64(0); off < int64(size); off += chunkSize {
		end := off + chunkSize
		if end > int64(size) {
			end = int64(size)
		}
		sum := sha256.Sum256(data[off:end])
		meta.Chunks = append(meta.Chunks, hex.EncodeToString(sum[:]))
	}
	sum := sha256.Sum256(data)
	meta.Hash = hex.EncodeToString(sum[:])
	return data, meta
}

func chunkOf(data []byte, meta Meta, i int) []byte {
	off := int64(i) * meta.ChunkSize
	end := off + meta.ChunkLen(i)
	return data[off:end]
}

func mustOpen(t *testing.T, dir string, hook Hook) *Store {
	t.Helper()
	s, err := Open(dir, hook)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func readInstalled(t *testing.T, s *Store) []byte {
	t.Helper()
	rc, _, err := s.ReadInstalled()
	if err != nil {
		t.Fatalf("ReadInstalled: %v", err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return b
}

// deliverAll offers meta and writes every chunk in the given order.
func deliverAll(t *testing.T, s *Store, data []byte, meta Meta, order []int) {
	t.Helper()
	if _, err := s.Offer(meta); err != nil {
		t.Fatalf("Offer: %v", err)
	}
	for _, i := range order {
		if err := s.WriteChunk(meta.Version, i, chunkOf(data, meta, i)); err != nil {
			t.Fatalf("WriteChunk(%d): %v", i, err)
		}
	}
}

func TestOutOfOrderInstall(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir, nil)
	data, meta := buildSnapshot(1, 1000, 128, 1)

	// Reverse order: tail chunk first.
	order := []int{}
	for i := meta.NumChunks() - 1; i >= 0; i-- {
		order = append(order, i)
	}
	deliverAll(t, s, data, meta, order)

	if err := s.Validate(meta.Version); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := s.Install(meta.Version); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if got := readInstalled(t, s); !bytes.Equal(got, data) {
		t.Fatalf("installed bytes differ")
	}
	if v, ok := s.InstalledVersion(); !ok || v != 1 {
		t.Fatalf("installed version = %v,%v", v, ok)
	}
}

func TestStaleVersionCannotOverwrite(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir, nil)
	data2, meta2 := buildSnapshot(2, 500, 64, 2)
	deliverAll(t, s, data2, meta2, []int{7, 0, 1, 2, 3, 4, 5, 6})
	if err := s.Install(2); err != nil {
		t.Fatalf("Install v2: %v", err)
	}

	// A late v1 response must be rejected at every entry point.
	_, meta1 := buildSnapshot(1, 500, 64, 1)
	if _, err := s.Offer(meta1); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("Offer v1: %v", err)
	}
	if err := s.WriteChunk(1, 0, make([]byte, 64)); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("WriteChunk v1: %v", err)
	}
	if err := s.Install(1); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("Install v1: %v", err)
	}
	if got := readInstalled(t, s); !bytes.Equal(got, data2) {
		t.Fatalf("stale response overwrote visible state")
	}
}

func TestOverlappingConflictKeepsValidatedChunk(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir, nil)
	data, meta := buildSnapshot(3, 256, 64, 1)

	if _, err := s.Offer(meta); err != nil {
		t.Fatal(err)
	}
	// Validate chunk 0 with correct bytes.
	if err := s.WriteChunk(1, 0, chunkOf(data, meta, 0)); err != nil {
		t.Fatal(err)
	}
	// Conflicting delivery for the same offset: same length, wrong bytes.
	bad := make([]byte, 64)
	for i := range bad {
		bad[i] = 0xAA
	}
	if err := s.WriteChunk(1, 0, bad); !errors.Is(err, ErrChunkHashMismatch) {
		t.Fatalf("conflicting chunk accepted: %v", err)
	}
	// The previously validated chunk must be untouched.
	_, flags, _ := s.Staged(1)
	if !flags[0] {
		t.Fatalf("conflict clobbered validated chunk")
	}
	// Finish and install; content must equal the original snapshot.
	for i := 1; i < meta.NumChunks(); i++ {
		if err := s.WriteChunk(1, i, chunkOf(data, meta, i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Install(1); err != nil {
		t.Fatal(err)
	}
	if got := readInstalled(t, s); !bytes.Equal(got, data) {
		t.Fatalf("installed bytes differ after conflict")
	}
}

func TestTailChunkLengthEnforced(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir, nil)
	// 200 bytes with 64-byte chunks: tail chunk is 8 bytes.
	data, meta := buildSnapshot(4, 200, 64, 1)
	if _, err := s.Offer(meta); err != nil {
		t.Fatal(err)
	}
	tail := meta.NumChunks() - 1
	if got := meta.ChunkLen(tail); got != 8 {
		t.Fatalf("tail len = %d", got)
	}
	// Full-size tail must be rejected.
	if err := s.WriteChunk(1, tail, make([]byte, 64)); !errors.Is(err, ErrChunkLength) {
		t.Fatalf("oversized tail accepted: %v", err)
	}
	// Short tail must be rejected too.
	if err := s.WriteChunk(1, tail, make([]byte, 7)); !errors.Is(err, ErrChunkLength) {
		t.Fatalf("short tail accepted: %v", err)
	}
	if err := s.WriteChunk(1, tail, chunkOf(data, meta, tail)); err != nil {
		t.Fatalf("exact tail rejected: %v", err)
	}
}

func TestRecoveryReusesValidatedChunks(t *testing.T) {
	dir := t.TempDir()
	data, meta := buildSnapshot(5, 512, 64, 1)

	s := mustOpen(t, dir, nil)
	if _, err := s.Offer(meta); err != nil {
		t.Fatal(err)
	}
	// Validate half the chunks, then "crash" (drop the Store, re-open).
	for i := 0; i < 4; i++ {
		if err := s.WriteChunk(1, i, chunkOf(data, meta, i)); err != nil {
			t.Fatal(err)
		}
	}
	s = mustOpen(t, dir, nil)

	needed, err := s.Offer(meta)
	if err != nil {
		t.Fatal(err)
	}
	if len(needed) != 4 {
		t.Fatalf("needed = %v, want 4 missing chunks", needed)
	}
	for _, i := range needed {
		if i < 4 {
			t.Fatalf("chunk %d was validated before crash but reported needed", i)
		}
	}
	for _, i := range needed {
		if err := s.WriteChunk(1, i, chunkOf(data, meta, i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Install(1); err != nil {
		t.Fatal(err)
	}
	if got := readInstalled(t, s); !bytes.Equal(got, data) {
		t.Fatalf("installed bytes differ after recovery")
	}
}

// TestInstallInterruption crashes at every cut point on the install path
// and asserts the visible state is never torn: either the old snapshot or
// the new one, and a retry always converges.
func TestInstallInterruption(t *testing.T) {
	for _, cp := range []CutPoint{CutSnapshotBeforeRename, CutPointerBeforeRename, CutPointerAfterRename} {
		t.Run(cp.String(), func(t *testing.T) {
			dir := t.TempDir()
			old, oldMeta := buildSnapshot(6, 300, 64, 1)
			s := mustOpen(t, dir, nil)
			deliverAll(t, s, old, oldMeta, []int{0, 1, 2, 3, 4})
			if err := s.Install(1); err != nil {
				t.Fatal(err)
			}

			next, nextMeta := buildSnapshot(7, 300, 64, 2)
			deliverAll(t, s, next, nextMeta, []int{4, 3, 2, 1, 0})

			// Arm a one-shot crash at the cut point.
			armed := true
			s.hook = func(c CutPoint) error {
				if armed && c == cp {
					armed = false
					return errors.New("injected crash")
				}
				return nil
			}
			if err := s.Install(2); err == nil {
				t.Fatalf("expected injected crash")
			}

			// Re-open (process restart) and check the visible state.
			s = mustOpen(t, dir, nil)
			got := readInstalled(t, s)
			v, _ := s.InstalledVersion()
			switch v {
			case 1:
				if !bytes.Equal(got, old) {
					t.Fatalf("torn install: visible state is neither old nor new")
				}
			case 2:
				if !bytes.Equal(got, next) {
					t.Fatalf("torn install: visible state is neither old nor new")
				}
			default:
				t.Fatalf("unexpected installed version %d", v)
			}

			// Retry converges to v2; staged chunks survived the crash.
			// (If the crash hit after the pointer swap, v2 is already
			// installed and no retry is needed.)
			if v, _ := s.InstalledVersion(); v != 2 {
				needed, err := s.Offer(nextMeta)
				if err != nil {
					t.Fatal(err)
				}
				if len(needed) != 0 {
					t.Fatalf("validated chunks lost across crash: %v", needed)
				}
				if err := s.Install(2); err != nil {
					t.Fatal(err)
				}
			}
			if got := readInstalled(t, s); !bytes.Equal(got, next) {
				t.Fatalf("retry did not converge")
			}
		})
	}
}

// TestChunkWriteCrashMidway crashes between chunk temp-write and rename;
// the chunk must simply be reported missing again after recovery.
func TestChunkWriteCrashMidway(t *testing.T) {
	dir := t.TempDir()
	data, meta := buildSnapshot(8, 256, 64, 1)
	s := mustOpen(t, dir, nil)
	if _, err := s.Offer(meta); err != nil {
		t.Fatal(err)
	}
	armed := true
	s.hook = func(c CutPoint) error {
		if armed && c == CutChunkBeforeRename {
			armed = false
			return errors.New("injected crash")
		}
		return nil
	}
	if err := s.WriteChunk(1, 0, chunkOf(data, meta, 0)); err == nil {
		t.Fatalf("expected injected crash")
	}
	s = mustOpen(t, dir, nil)
	needed, err := s.Offer(meta)
	if err != nil {
		t.Fatal(err)
	}
	if len(needed) != meta.NumChunks() {
		t.Fatalf("needed = %v", needed)
	}
	// Complete delivery after recovery.
	for _, i := range needed {
		if err := s.WriteChunk(1, i, chunkOf(data, meta, i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Install(1); err != nil {
		t.Fatal(err)
	}
	if got := readInstalled(t, s); !bytes.Equal(got, data) {
		t.Fatalf("installed bytes differ")
	}
}

func TestConflictingManifestRejected(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir, nil)
	_, meta := buildSnapshot(9, 128, 64, 1)
	if _, err := s.Offer(meta); err != nil {
		t.Fatal(err)
	}
	other := meta
	other.Chunks = append([]string(nil), meta.Chunks...)
	other.Chunks[0] = meta.Chunks[1]
	other.Chunks[1] = meta.Chunks[0]
	if _, err := s.Offer(other); !errors.Is(err, ErrMetaConflict) {
		t.Fatalf("conflicting manifest accepted: %v", err)
	}
}

func TestValidateIncomplete(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir, nil)
	data, meta := buildSnapshot(10, 128, 64, 1)
	if _, err := s.Offer(meta); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteChunk(1, 0, chunkOf(data, meta, 0)); err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(1); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Validate: %v", err)
	}
	if err := s.Install(1); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Install: %v", err)
	}
	if _, _, err := s.ReadInstalled(); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("ReadInstalled: %v", err)
	}
}

func Example() {
	dir, _ := os.MkdirTemp("", "snaprecv")
	defer os.RemoveAll(dir)
	s, _ := Open(dir, nil)
	data, meta := buildSnapshot(0, 100, 32, 1)
	s.Offer(meta)
	for i := meta.NumChunks() - 1; i >= 0; i-- {
		s.WriteChunk(1, i, chunkOf(data, meta, i))
	}
	s.Validate(1)
	s.Install(1)
	rc, _, _ := s.ReadInstalled()
	b, _ := io.ReadAll(rc)
	rc.Close()
	fmt.Println(len(b))
	// Output: 100
}

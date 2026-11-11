package transcript

import (
	"errors"
	"testing"
)

// TestPhaseRetry proves phase rollback and duplicate tags are rejected
// without state changes, so the legitimate call can be retried.
func TestPhaseRetry(t *testing.T) {
	clean := New()
	if err := clean.WriteBytes(1, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := clean.BeginPhase(1); err != nil {
		t.Fatal(err)
	}
	if err := clean.WriteBytes(2, []byte("b")); err != nil {
		t.Fatal(err)
	}
	if err := clean.BeginPhase(3); err != nil {
		t.Fatal(err)
	}
	want, err := clean.Close()
	if err != nil {
		t.Fatal(err)
	}

	b := New()
	if err := b.WriteBytes(1, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := b.BeginPhase(1); err != nil {
		t.Fatal(err)
	}

	if err := b.BeginPhase(0); !errors.Is(err, ErrPhaseRollback) {
		t.Fatalf("rollback: got %v", err)
	}
	if b.Phase() != 1 {
		t.Fatalf("failed rollback changed phase to %d", b.Phase())
	}

	// The legitimate phase-1 write retries successfully.
	if err := b.WriteBytes(2, []byte("b")); err != nil {
		t.Fatal(err)
	}

	// Duplicate tags are rejected in the same phase and later phases.
	if err := b.WriteBytes(2, []byte("c")); !errors.Is(err, ErrDuplicateTag) {
		t.Fatalf("duplicate tag: got %v", err)
	}
	if err := b.BeginPhase(3); err != nil {
		t.Fatal(err)
	}
	if err := b.WriteBytes(2, []byte("c")); !errors.Is(err, ErrDuplicateTag) {
		t.Fatalf("cross-phase duplicate tag: got %v", err)
	}
	if err := b.WriteBytes(0, []byte("z")); !errors.Is(err, ErrBadTag) {
		t.Fatalf("zero tag: got %v", err)
	}

	got, err := b.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got.Sum != want.Sum {
		t.Fatalf("rejected retries altered the transcript:\n got %x\nwant %x", got.Sum, want.Sum)
	}
}

// TestCancellation covers mid-chunk cancellation and guarantees no
// signable digest can ever leave a cancelled builder.
func TestCancellation(t *testing.T) {
	b := New()
	if err := b.WriteBytes(1, []byte("committed")); err != nil {
		t.Fatal(err)
	}
	fw, err := b.BeginField(2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("partial")); err != nil {
		t.Fatal(err)
	}
	b.Cancel()

	checks := []struct {
		name string
		call func() error
	}{
		{"digest", func() error { _, err := b.Digest(); return err }},
		{"close", func() error { _, err := b.Close(); return err }},
		{"write", func() error { return b.WriteBytes(3, []byte("x")) }},
		{"phase", func() error { return b.BeginPhase(1) }},
		{"chunk", func() error { _, err := fw.Write([]byte("z")); return err }},
		{"field close", func() error { return fw.Close() }},
	}
	for _, tc := range checks {
		if err := tc.call(); !errors.Is(err, ErrCancelled) {
			t.Fatalf("%s after cancel: got %v", tc.name, err)
		}
	}
	b.Cancel() // idempotent
}

// TestDigestGating ensures no digest is exportable while the transcript
// is still open, and that Close and Digest agree.
func TestDigestGating(t *testing.T) {
	b := New()
	if _, err := b.Digest(); !errors.Is(err, ErrNotClosed) {
		t.Fatalf("digest before any write: got %v", err)
	}
	if err := b.WriteUint(5, 99); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Digest(); !errors.Is(err, ErrNotClosed) {
		t.Fatalf("digest while open: got %v", err)
	}

	fw, err := b.BeginField(6, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Close(); !errors.Is(err, ErrOpenField) {
		t.Fatalf("close with open field: got %v", err)
	}
	if _, err := fw.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	if err := fw.Close(); err != nil {
		t.Fatal(err)
	}

	closed, err := b.Close()
	if err != nil {
		t.Fatal(err)
	}
	again, err := b.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if again != closed {
		t.Fatal("Digest after Close disagrees with Close result")
	}
	if err := b.WriteBytes(7, []byte("z")); !errors.Is(err, ErrClosed) {
		t.Fatalf("write after close: got %v", err)
	}
	if _, err := b.Close(); !errors.Is(err, ErrClosed) {
		t.Fatalf("double close: got %v", err)
	}
}

package netsec

import (
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSignatureRegionMisalignmentRejected(t *testing.T) {
	ctx := context.Background()
	keys := newTestKeys(t)
	policy := testPolicy(keys, NewMemoryCounter(), 0)
	payload := []byte("firmware-image-v1")

	t.Run("payload byte altered", func(t *testing.T) {
		capsule := signedCapsule(keys, payload, 1, 1, nil)
		capsule[OuterHeaderSize] ^= 0xFF
		_, err := policy.Admit(ctx, capsule)
		if !errors.Is(err, ErrSignature) {
			t.Fatalf("err = %v, want ErrSignature", err)
		}
	})

	t.Run("counter altered without re-signing", func(t *testing.T) {
		capsule := signedCapsule(keys, payload, 1, 1, func(raw *rawCapsule) {
			raw.Counter = 2
		})
		_, err := policy.Admit(ctx, capsule)
		if !errors.Is(err, ErrSignature) {
			t.Fatalf("err = %v, want ErrSignature", err)
		}
	})

	t.Run("signature length field wrong", func(t *testing.T) {
		capsule := signedCapsule(keys, payload, 1, 1, nil)
		sigLenOff := OuterHeaderSize + len(payload) + 12
		binary.BigEndian.PutUint32(capsule[sigLenOff:], ed25519.SignatureSize-1)
		_, err := policy.Admit(ctx, capsule)
		if !errors.Is(err, ErrAuthLength) {
			t.Fatalf("err = %v, want ErrAuthLength", err)
		}
	})

	t.Run("wrong signing key", func(t *testing.T) {
		other := newTestKeys(t)
		otherPolicy := testPolicy(other, NewMemoryCounter(), 0)
		capsule := signedCapsule(keys, payload, 1, 1, nil)
		_, err := otherPolicy.Admit(ctx, capsule)
		if !errors.Is(err, ErrSignature) {
			t.Fatalf("err = %v, want ErrSignature", err)
		}
	})

	t.Run("signature over payload without counter fails", func(t *testing.T) {
		sig := ed25519.Sign(keys.private, payload)
		capsule := signedCapsule(keys, payload, 1, 1, func(raw *rawCapsule) {
			raw.Sig = sig
		})
		_, err := policy.Admit(ctx, capsule)
		if !errors.Is(err, ErrSignature) {
			t.Fatalf("err = %v, want ErrSignature", err)
		}
	})

	t.Run("magic checked", func(t *testing.T) {
		capsule := signedCapsule(keys, payload, 1, 1, func(raw *rawCapsule) {
			raw.Magic = 0xDEADBEEF
		})
		_, err := policy.Admit(ctx, capsule)
		if !errors.Is(err, ErrBadMagic) {
			t.Fatalf("err = %v, want ErrBadMagic", err)
		}
	})
}

func TestCounterReplayRejected(t *testing.T) {
	ctx := context.Background()
	keys := newTestKeys(t)
	store := NewMemoryCounter()
	policy := testPolicy(keys, store, 0)
	payload := []byte("firmware")

	first := signedCapsule(keys, payload, 5, 1, nil)
	admitted, err := policy.Admit(ctx, first)
	if err != nil {
		t.Fatalf("first admit: %v", err)
	}
	if err := policy.Commit(ctx, admitted); err != nil {
		t.Fatalf("first commit: %v", err)
	}

	t.Run("equal counter replayed", func(t *testing.T) {
		_, err := policy.Admit(ctx, signedCapsule(keys, payload, 5, 1, nil))
		if !errors.Is(err, ErrCounterReplay) {
			t.Fatalf("err = %v, want ErrCounterReplay", err)
		}
	})

	t.Run("stale counter replayed", func(t *testing.T) {
		_, err := policy.Admit(ctx, signedCapsule(keys, payload, 4, 1, nil))
		if !errors.Is(err, ErrCounterReplay) {
			t.Fatalf("err = %v, want ErrCounterReplay", err)
		}
	})

	t.Run("commit enforces monotonicity directly", func(t *testing.T) {
		if err := store.Commit(ctx, 3); !errors.Is(err, ErrCounterReplay) {
			t.Fatalf("err = %v, want ErrCounterReplay", err)
		}
	})

	t.Run("higher counter accepted and committed", func(t *testing.T) {
		admitted, err := policy.Admit(ctx, signedCapsule(keys, payload, 6, 1, nil))
		if err != nil {
			t.Fatalf("admit higher counter: %v", err)
		}
		if err := policy.Commit(ctx, admitted); err != nil {
			t.Fatalf("commit higher counter: %v", err)
		}
		if got, _ := store.Current(ctx); got != 6 {
			t.Fatalf("stored counter = %d, want 6", got)
		}
	})
}

func TestVersionDowngradeRejected(t *testing.T) {
	ctx := context.Background()
	keys := newTestKeys(t)
	policy := testPolicy(keys, NewMemoryCounter(), 0x00020000)
	payload := []byte("firmware")

	t.Run("below minimum", func(t *testing.T) {
		capsule := signedCapsule(keys, payload, 1, 0x00010009, nil)
		_, err := policy.Admit(ctx, capsule)
		if !errors.Is(err, ErrVersionUnsupported) {
			t.Fatalf("err = %v, want ErrVersionUnsupported", err)
		}
	})

	t.Run("equal to minimum accepted", func(t *testing.T) {
		capsule := signedCapsule(keys, payload, 1, 0x00020000, nil)
		if _, err := policy.Admit(ctx, capsule); err != nil {
			t.Fatalf("admit at minimum: %v", err)
		}
	})

	t.Run("above minimum accepted", func(t *testing.T) {
		capsule := signedCapsule(keys, payload, 1, 0x00030000, nil)
		if _, err := policy.Admit(ctx, capsule); err != nil {
			t.Fatalf("admit above minimum: %v", err)
		}
	})
}

func TestPowerLossBeforeCommitRetriable(t *testing.T) {
	ctx := context.Background()
	keys := newTestKeys(t)
	path := filepath.Join(t.TempDir(), "counter.bin")

	store, err := NewFileCounter(path)
	if err != nil {
		t.Fatalf("new file counter: %v", err)
	}
	policy := testPolicy(keys, store, 0x00010000)
	payload := []byte("firmware-image")
	counter := uint64(7)
	capsule := signedCapsule(keys, payload, counter, 0x00010001, nil)

	// First boot: capsule passes, flash "succeeds", but power is lost before
	// Commit runs, so no counter file must exist.
	_, err = policy.Admit(ctx, capsule)
	if err != nil {
		t.Fatalf("first admit: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("counter file created before commit: stat err = %v", err)
	}

	// Simulated reboot: reload the durable store from disk.
	reopened, err := NewFileCounter(path)
	if err != nil {
		t.Fatalf("reopen after power loss: %v", err)
	}
	if got, _ := reopened.Current(ctx); got != 0 {
		t.Fatalf("counter after power loss = %d, want 0", got)
	}
	policy.Counter = reopened

	// Same capsule must still be admissible because the counter never advanced.
	retried, err := policy.Admit(ctx, capsule)
	if err != nil {
		t.Fatalf("retry after power loss: %v", err)
	}
	if string(retried.Payload) != string(payload) {
		t.Fatalf("retried payload mismatch")
	}
	if err := policy.Commit(ctx, retried); err != nil {
		t.Fatalf("commit after retry: %v", err)
	}

	// Another reboot: committed value survives and replay is now rejected.
	committed, err := NewFileCounter(path)
	if err != nil {
		t.Fatalf("reopen after commit: %v", err)
	}
	if got, _ := committed.Current(ctx); got != counter {
		t.Fatalf("committed counter = %d, want %d", got, counter)
	}
	policy.Counter = committed
	if _, err := policy.Admit(ctx, capsule); !errors.Is(err, ErrCounterReplay) {
		t.Fatalf("post-commit replay err = %v, want ErrCounterReplay", err)
	}
}

func TestFileCounterCorruptionAndTornFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "counter.bin")

	store, err := NewFileCounter(path)
	if err != nil {
		t.Fatalf("new file counter: %v", err)
	}
	if err := store.Commit(ctx, 42); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// A torn write leaves fewer than 8 bytes; construction must refuse to
	// silently reset the counter to zero.
	if err := os.WriteFile(path, []byte{1, 2, 3}, 0o600); err != nil {
		t.Fatalf("write torn file: %v", err)
	}
	if _, err := NewFileCounter(path); !errors.Is(err, ErrCounterCorrupt) {
		t.Fatalf("torn file err = %v, want ErrCounterCorrupt", err)
	}
	if _, err := store.Current(ctx); !errors.Is(err, ErrCounterCorrupt) {
		t.Fatalf("current err = %v, want ErrCounterCorrupt", err)
	}

	// No temp file may be left behind after an atomic commit.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if match, _ := filepath.Match(".counter-*", entry.Name()); match {
			t.Fatalf("leftover temp counter file: %s", entry.Name())
		}
	}
}

func TestInvalidPolicyRejected(t *testing.T) {
	ctx := context.Background()
	keys := newTestKeys(t)
	payload := []byte("firmware")
	capsule := signedCapsule(keys, payload, 1, 1, nil)

	if _, err := (Policy{}).Admit(ctx, capsule); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("empty policy err = %v, want ErrInvalidPolicy", err)
	}
	if err := (Policy{}).Commit(ctx, &AdmittedCapsule{Counter: 1}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("empty policy commit err = %v, want ErrInvalidPolicy", err)
	}
}

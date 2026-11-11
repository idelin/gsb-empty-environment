package proofcache

import (
	"errors"
	"testing"
)

var testRule = RuleVersion{Name: "taint", Version: "v1.2.0"}

func digestFor(tag string) Digest {
	h := NewHasher()
	h.WriteString("test", tag)
	return h.Sum()
}

func fpFor(t *testing.T, irTag string, deps []DepClaim) Fingerprint {
	t.Helper()
	fp, err := NewFingerprint(digestFor(irTag), deps, testRule)
	if err != nil {
		t.Fatalf("NewFingerprint: %v", err)
	}
	return fp
}

func draftFor(t *testing.T, label Label, irTag string, contractTag string, deps []DepClaim, generation uint64) Draft {
	t.Helper()
	return Draft{
		Label:       label,
		Fingerprint: fpFor(t, irTag, deps),
		Contract:    digestFor(contractTag),
		Proof:       Proof("proof-" + label),
		Generation:  generation,
	}
}

func mustCommit(t *testing.T, b *Batch) []*Entry {
	t.Helper()
	published, err := b.Commit()
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return published
}

func assertErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want error %v, got %v", target, err)
	}
}

func assertFenced(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrStale) && !errors.Is(err, ErrBatchClosed) {
		t.Fatalf("operation must be fenced as stale or closed, got %v", err)
	}
}

func assertLookup(t *testing.T, c *Cache, label Label, fp Fingerprint) *Entry {
	t.Helper()
	entry, err := c.Lookup(label, fp)
	if err != nil {
		t.Fatalf("Lookup(%q): %v", label, err)
	}
	return entry
}

func assertLookupMisses(t *testing.T, c *Cache, label Label, fp Fingerprint) {
	t.Helper()
	if _, err := c.Lookup(label, fp); err == nil {
		t.Fatalf("Lookup(%q) unexpectedly hit", label)
	}
}

func newTestCache() *Cache {
	c := New(testRule)
	c.newBatchID = func() (string, error) {
		return "batch-fixed", nil
	}
	return c
}

// gen returns the fencing generation a batch captured for member.
func gen(b *Batch, member Label) uint64 {
	return b.Generation(member)
}

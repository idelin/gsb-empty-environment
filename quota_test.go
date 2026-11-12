package netsec

import (
	"errors"
	"testing"
)

func TestQuotaReserveAndRelease(t *testing.T) {
	q := NewQuota(100)

	g1, err := q.reserve(60)
	if err != nil {
		t.Fatalf("reserve 60: %v", err)
	}
	if q.Used() != 60 || q.Available() != 40 {
		t.Fatalf("unexpected state used=%d available=%d", q.Used(), q.Available())
	}

	if _, err := q.reserve(41); !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("expected ErrQuotaExhausted, got %v", err)
	}
	// A failed reservation leaves state untouched.
	if q.Used() != 60 {
		t.Fatalf("failed reservation mutated state, used=%d", q.Used())
	}

	g2, err := q.reserve(40)
	if err != nil {
		t.Fatalf("reserve 40: %v", err)
	}
	if q.Used() != 100 {
		t.Fatalf("expected used 100, got %d", q.Used())
	}

	// Releases are idempotent.
	if released := g1.Release(); released != 60 {
		t.Fatalf("expected 60 released, got %d", released)
	}
	if g1.Release() != 0 {
		t.Fatal("second Release must be a no-op")
	}
	if q.Used() != 40 {
		t.Fatalf("expected used 40 after release, got %d", q.Used())
	}

	// A committed grant cannot be released later.
	g2.Commit()
	g2.Release()
	if q.Used() != 40 {
		t.Fatalf("committed grant must stay counted, used=%d", q.Used())
	}
}

func TestQuotaZeroValueGrantIsSafe(t *testing.T) {
	var nilGrant *Grant
	nilGrant.Release() // must not panic

	empty := &Grant{}
	empty.Commit()
	if empty.Release() != 0 {
		t.Fatal("zero-value grant release should report 0")
	}
}

func TestQuotaRejectsNegativeAndNonPositiveLimit(t *testing.T) {
	q := NewQuota(0)
	if _, err := q.reserve(0); err != nil {
		t.Fatalf("zero-byte reservation should succeed, got %v", err)
	}
	if _, err := q.reserve(1); !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("expected zero-limit quota to reject bytes, got %v", err)
	}

	if _, err := q.reserve(-1); err == nil {
		t.Fatal("negative reservation must error")
	}
}

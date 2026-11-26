package crlstate

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

var testBase = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// tm returns a time offset by hours from the scenario origin.
func tm(hours int) time.Time { return testBase.Add(time.Duration(hours) * time.Hour) }

func mkManifest(part, issuer, root string, epoch uint64, lo, hi uint64, from, until int,
	reasons ReasonMask, crl uint64, isDelta bool, baseDelta uint64, entries ...Entry) *Manifest {
	return &Manifest{
		PartitionID: part,
		IssuerID:    IssuerID(issuer),
		RootID:      RootID(root),
		Epoch:       epoch,
		SerialLo:    SerialFromUint64(lo),
		SerialHi:    SerialFromUint64(hi),
		Reasons:     reasons,
		ValidFrom:   tm(from),
		ValidUntil:  tm(until),
		CRLNumber:   crl,
		IsDelta:     isDelta,
		BaseDelta:   baseDelta,
		Entries:     entries,
	}
}

func mkFull(part, issuer, root string, epoch uint64, lo, hi uint64, from, until int,
	reasons ReasonMask, crl uint64, entries ...Entry) *Manifest {
	return mkManifest(part, issuer, root, epoch, lo, hi, from, until, reasons, crl, false, 0, entries...)
}

func mkDelta(part, issuer, root string, epoch uint64, lo, hi uint64, from, until int,
	reasons ReasonMask, crl, base uint64, entries ...Entry) *Manifest {
	return mkManifest(part, issuer, root, epoch, lo, hi, from, until, reasons, crl, true, base, entries...)
}

func ent(serial uint64, at int, reason ReasonCode, holdID string) Entry {
	return Entry{Serial: SerialFromUint64(serial), RevokedAt: tm(at), Reason: reason, HoldID: holdID}
}

func cert(serial uint64, start, end int) Certificate {
	return Certificate{
		IssuerID: "issuer",
		Serial:   SerialFromUint64(serial),
		Start:    tm(start),
		End:      tm(end),
	}
}

func mustAdd(t *testing.T, agg *Aggregator, m *Manifest) {
	t.Helper()
	if err := agg.AddManifest(m); err != nil {
		t.Fatalf("AddManifest: %v", err)
	}
}

func wantStatus(t *testing.T, got Result, want Status) {
	t.Helper()
	if got.Status != want {
		t.Fatalf("status = %s, want %s (result %+v)", got.Status, want, got)
	}
}

func TestCoverageRevocationAndHold(t *testing.T) {
	t.Run("complete partition", func(t *testing.T) {
		agg := NewAggregator(nil)
		mustAdd(t, agg, mkFull("p", "issuer", "r1", 1, 0, 100, 0, 100, AllStatusReasons, 1))
		wantStatus(t, agg.Check(cert(10, 10, 90)), StatusGood)
	})

	t.Run("hard revocation", func(t *testing.T) {
		agg := NewAggregator(nil)
		mustAdd(t, agg, mkFull("p", "issuer", "r1", 1, 0, 100, 0, 100, AllStatusReasons, 1,
			ent(10, 20, ReasonKeyCompromise, "")))
		res := agg.Check(cert(10, 10, 90))
		wantStatus(t, res, StatusRevoked)
		if res.Reason != ReasonKeyCompromise || !res.RevokedAt.Equal(tm(20)) {
			t.Fatalf("unexpected revocation detail: %+v", res)
		}
	})

	t.Run("revocation after lifetime is ignored", func(t *testing.T) {
		agg := NewAggregator(nil)
		mustAdd(t, agg, mkFull("p", "issuer", "r1", 1, 0, 100, 0, 100, AllStatusReasons, 1,
			ent(10, 95, ReasonKeyCompromise, "")))
		wantStatus(t, agg.Check(cert(10, 10, 90)), StatusGood)
	})

	t.Run("released certificate hold is non-blocking", func(t *testing.T) {
		agg := NewAggregator(nil)
		mustAdd(t, agg, mkFull("p", "issuer", "r1", 1, 0, 50, 0, 50, AllStatusReasons, 1,
			ent(10, 20, ReasonCertificateHold, "held-1")))
		mustAdd(t, agg, mkDelta("p", "issuer", "r1", 1, 0, 50, 50, 100, AllStatusReasons, 2, 1,
			ent(10, 60, ReasonRemoveFromCRL, "held-1")))
		wantStatus(t, agg.Check(cert(10, 10, 90)), StatusGood)
	})

	t.Run("hold released under a different credential blocks", func(t *testing.T) {
		agg := NewAggregator(nil)
		mustAdd(t, agg, mkFull("p", "issuer", "r1", 1, 0, 50, 0, 50, AllStatusReasons, 1,
			ent(10, 20, ReasonCertificateHold, "held-1")))
		mustAdd(t, agg, mkDelta("p", "issuer", "r1", 1, 0, 50, 50, 100, AllStatusReasons, 2, 1,
			ent(10, 60, ReasonRemoveFromCRL, "held-2")))
		wantStatus(t, agg.Check(cert(10, 10, 90)), StatusRevoked)
	})

	t.Run("wrong-baseline delta cannot release a hold", func(t *testing.T) {
		agg := NewAggregator(nil)
		mustAdd(t, agg, mkFull("p", "issuer", "r1", 1, 0, 50, 0, 50, AllStatusReasons, 1,
			ent(10, 20, ReasonCertificateHold, "held-1")))
		// CRL 4 claims a dependency on CRL 3, which was never supplied for
		// this partition. Its removeFromCRL entry is not compatible state.
		mustAdd(t, agg, mkDelta("p", "issuer", "r1", 1, 0, 50, 50, 100, AllStatusReasons, 4, 3,
			ent(10, 60, ReasonRemoveFromCRL, "held-1")))
		wantStatus(t, agg.Check(cert(10, 10, 90)), StatusRevoked)
	})
}

func TestDeltaContinuityAndEpochIsolation(t *testing.T) {
	t.Run("matching delta", func(t *testing.T) {
		agg := NewAggregator(nil)
		mustAdd(t, agg, mkFull("p", "issuer", "r1", 1, 0, 100, 0, 50, AllStatusReasons, 1))
		mustAdd(t, agg, mkDelta("p", "issuer", "r1", 1, 0, 100, 50, 100, AllStatusReasons, 2, 1))
		wantStatus(t, agg.Check(cert(10, 10, 90)), StatusGood)
	})

	t.Run("cumulative delta generations share the full base dependency", func(t *testing.T) {
		agg := NewAggregator(nil)
		mustAdd(t, agg, mkFull("p", "issuer", "r1", 1, 0, 100, 0, 50, AllStatusReasons, 1))
		mustAdd(t, agg, mkDelta("p", "issuer", "r1", 1, 0, 100, 50, 75, AllStatusReasons, 2, 1))
		mustAdd(t, agg, mkDelta("p", "issuer", "r1", 1, 0, 100, 75, 100, AllStatusReasons, 3, 1))
		wantStatus(t, agg.Check(cert(10, 10, 90)), StatusGood)
	})

	t.Run("missing baseline dependency", func(t *testing.T) {
		agg := NewAggregator(nil)
		mustAdd(t, agg, mkFull("p", "issuer", "r1", 1, 0, 100, 0, 50, AllStatusReasons, 1))
		mustAdd(t, agg, mkDelta("p", "issuer", "r1", 1, 0, 100, 50, 100, AllStatusReasons, 4, 3))
		res := agg.Check(cert(10, 10, 90))
		wantStatus(t, res, StatusUnknown)
		if len(res.Missing) == 0 || !res.Missing[0].Start.Equal(tm(50)) {
			t.Fatalf("expected gap at 50, got %+v", res.Missing)
		}
	})

	t.Run("delta time gap is not stitched", func(t *testing.T) {
		agg := NewAggregator(nil)
		mustAdd(t, agg, mkFull("p", "issuer", "r1", 1, 0, 100, 0, 50, AllStatusReasons, 1))
		mustAdd(t, agg, mkDelta("p", "issuer", "r1", 1, 0, 100, 60, 100, AllStatusReasons, 2, 1))
		wantStatus(t, agg.Check(cert(10, 10, 90)), StatusUnknown)
	})

	t.Run("delta cannot cross into a newer epoch", func(t *testing.T) {
		agg := NewAggregator(nil)
		mustAdd(t, agg, mkFull("p", "issuer", "r1", 1, 0, 100, 0, 50, AllStatusReasons, 1))
		mustAdd(t, agg, mkDelta("p", "issuer", "r1", 2, 0, 100, 50, 100, AllStatusReasons, 2, 1))
		wantStatus(t, agg.Check(cert(10, 10, 90)), StatusUnknown)
	})

	t.Run("non-adjacent epoch halves are not stitched", func(t *testing.T) {
		agg := NewAggregator(nil)
		mustAdd(t, agg, mkFull("p", "issuer", "r1", 1, 0, 100, 0, 50, AllStatusReasons, 1))
		mustAdd(t, agg, mkFull("p", "issuer", "r1", 2, 0, 100, 60, 100, AllStatusReasons, 1))
		wantStatus(t, agg.Check(cert(10, 10, 90)), StatusUnknown)
	})

	t.Run("expired higher epoch does not resurrect a lower epoch", func(t *testing.T) {
		agg := NewAggregator(nil)
		mustAdd(t, agg, mkFull("p", "issuer", "r1", 1, 0, 100, 0, 100, AllStatusReasons, 1))
		mustAdd(t, agg, mkFull("p", "issuer", "r1", 2, 0, 100, 50, 60, AllStatusReasons, 2))
		wantStatus(t, agg.Check(cert(10, 10, 90)), StatusUnknown)
		wantStatus(t, agg.CheckAt(cert(10, 10, 90), tm(55)), StatusGood)
		wantStatus(t, agg.CheckAt(cert(10, 10, 90), tm(70)), StatusUnknown)
	})

	t.Run("hard revocation survives a later epoch", func(t *testing.T) {
		agg := NewAggregator(nil)
		mustAdd(t, agg, mkFull("p", "issuer", "r1", 1, 0, 100, 0, 50, AllStatusReasons, 1,
			ent(10, 20, ReasonKeyCompromise, "")))
		mustAdd(t, agg, mkFull("p", "issuer", "r1", 2, 0, 100, 50, 100, AllStatusReasons, 1))
		wantStatus(t, agg.Check(cert(10, 10, 90)), StatusRevoked)
	})
}

func TestReasonAndRootScopedCoverage(t *testing.T) {
	t.Run("reason bits may be split across same-root partitions", func(t *testing.T) {
		agg := NewAggregator(nil)
		half := AllStatusReasons &^ MaskCertificateHold
		mustAdd(t, agg, mkFull("p1", "issuer", "r1", 1, 0, 100, 0, 100, half, 1))
		wantStatus(t, agg.Check(cert(10, 10, 90)), StatusUnknown)
		mustAdd(t, agg, mkFull("p2", "issuer", "r1", 1, 0, 100, 0, 100, MaskCertificateHold, 2))
		wantStatus(t, agg.Check(cert(10, 10, 90)), StatusGood)
	})

	t.Run("different roots are not overlaid", func(t *testing.T) {
		agg := NewAggregator(nil)
		keyOnly := MaskKeyCompromise
		holdOnly := MaskCertificateHold
		mustAdd(t, agg, mkFull("p1", "issuer", "r1", 1, 0, 100, 0, 100, keyOnly, 1))
		mustAdd(t, agg, mkFull("p2", "issuer", "r2", 1, 0, 100, 0, 100, holdOnly, 1))
		wantStatus(t, agg.Check(cert(10, 10, 90)), StatusUnknown)
	})

	t.Run("one complete root path is sufficient", func(t *testing.T) {
		agg := NewAggregator(nil)
		mustAdd(t, agg, mkFull("p1", "issuer", "r1", 1, 0, 100, 0, 100, AllStatusReasons, 1))
		mustAdd(t, agg, mkFull("p2", "issuer", "r2", 1, 0, 100, 0, 100, MaskCertificateHold, 2))
		wantStatus(t, agg.Check(cert(10, 10, 90)), StatusGood)
	})
}

func TestSplitMergeMigration(t *testing.T) {
	setup := func(t *testing.T, sameRoot, withMigration bool) *Aggregator {
		t.Helper()
		agg := NewAggregator(nil)
		mustAdd(t, agg, mkFull("old", "issuer", "r1", 1, 0, 100, 0, 100, AllStatusReasons, 1))
		newRoot := "r2"
		if sameRoot {
			newRoot = "r1"
		}
		mustAdd(t, agg, mkFull("new-a", "issuer", newRoot, 2, 0, 50, 100, 200, AllStatusReasons, 1))
		mustAdd(t, agg, mkFull("new-b", "issuer", newRoot, 2, 50, 100, 100, 200, AllStatusReasons, 1))
		if withMigration {
			mig := &Migration{
				Kind:      MigrationSplit,
				IssuerID:  "issuer",
				FromEpoch: 1,
				ToEpoch:   2,
				OldRoot:   "r1",
				NewRoot:   RootID(newRoot),
				Old:       []PartitionSpan{{PartitionID: "old", SerialLo: SerialFromUint64(0), SerialHi: SerialFromUint64(100), Reasons: AllStatusReasons}},
				New: []PartitionSpan{
					{PartitionID: "new-a", SerialLo: SerialFromUint64(0), SerialHi: SerialFromUint64(50), Reasons: AllStatusReasons},
					{PartitionID: "new-b", SerialLo: SerialFromUint64(50), SerialHi: SerialFromUint64(100), Reasons: AllStatusReasons},
				},
				EffectiveAt: tm(100),
			}
			if err := agg.AddMigration(mig); err != nil {
				t.Fatalf("AddMigration: %v", err)
			}
		}
		return agg
	}

	t.Run("split without declaration is unknown", func(t *testing.T) {
		agg := setup(t, true, false)
		wantStatus(t, agg.Check(cert(10, 10, 190)), StatusUnknown)
	})
	t.Run("split with same-root declaration", func(t *testing.T) {
		agg := setup(t, true, true)
		wantStatus(t, agg.Check(cert(10, 10, 190)), StatusGood)
		wantStatus(t, agg.Check(cert(70, 10, 190)), StatusGood)
	})
	t.Run("cross-root split requires dual-signed declaration", func(t *testing.T) {
		agg := setup(t, false, false)
		wantStatus(t, agg.Check(cert(10, 10, 190)), StatusUnknown)
	})
	t.Run("cross-root split with declaration", func(t *testing.T) {
		agg := setup(t, false, true)
		wantStatus(t, agg.Check(cert(10, 10, 190)), StatusGood)
	})
	t.Run("revocation in a migrated partition still wins", func(t *testing.T) {
		agg := NewAggregator(nil)
		mustAdd(t, agg, mkFull("old", "issuer", "r1", 1, 0, 100, 0, 100, AllStatusReasons, 1))
		mustAdd(t, agg, mkFull("new-a", "issuer", "r2", 2, 0, 50, 100, 200, AllStatusReasons, 1,
			ent(10, 110, ReasonKeyCompromise, "")))
		mustAdd(t, agg, mkFull("new-b", "issuer", "r2", 2, 50, 100, 100, 200, AllStatusReasons, 1))
		mig := &Migration{
			Kind: "split", IssuerID: "issuer", FromEpoch: 1, ToEpoch: 2, OldRoot: "r1", NewRoot: "r2",
			Old: []PartitionSpan{{PartitionID: "old", SerialLo: SerialFromUint64(0), SerialHi: SerialFromUint64(100), Reasons: AllStatusReasons}},
			New: []PartitionSpan{
				{PartitionID: "new-a", SerialLo: SerialFromUint64(0), SerialHi: SerialFromUint64(50), Reasons: AllStatusReasons},
				{PartitionID: "new-b", SerialLo: SerialFromUint64(50), SerialHi: SerialFromUint64(100), Reasons: AllStatusReasons},
			},
			EffectiveAt: tm(100),
		}
		if err := agg.AddMigration(mig); err != nil {
			t.Fatalf("AddMigration: %v", err)
		}
		wantStatus(t, agg.Check(cert(10, 10, 190)), StatusRevoked)
	})
}

func TestMergeMigrationBoundaries(t *testing.T) {
	agg := NewAggregator(nil)
	mustAdd(t, agg, mkFull("a", "issuer", "r1", 1, 0, 50, 0, 100, AllStatusReasons, 1))
	mustAdd(t, agg, mkFull("b", "issuer", "r1", 1, 50, 100, 0, 100, AllStatusReasons, 1))
	mustAdd(t, agg, mkFull("merged", "issuer", "r2", 2, 0, 100, 100, 200, AllStatusReasons, 1))
	mig := &Migration{
		Kind: "merge", IssuerID: "issuer", FromEpoch: 1, ToEpoch: 2, OldRoot: "r1", NewRoot: "r2",
		Old: []PartitionSpan{
			{PartitionID: "a", SerialLo: SerialFromUint64(0), SerialHi: SerialFromUint64(50), Reasons: AllStatusReasons},
			{PartitionID: "b", SerialLo: SerialFromUint64(50), SerialHi: SerialFromUint64(100), Reasons: AllStatusReasons},
		},
		New:         []PartitionSpan{{PartitionID: "merged", SerialLo: SerialFromUint64(0), SerialHi: SerialFromUint64(100), Reasons: AllStatusReasons}},
		EffectiveAt: tm(100),
	}
	if err := agg.AddMigration(mig); err != nil {
		t.Fatalf("AddMigration: %v", err)
	}
	wantStatus(t, agg.Check(cert(10, 10, 190)), StatusGood)
	wantStatus(t, agg.Check(cert(70, 10, 190)), StatusGood)
}

func TestSameEpochSplitStillRequiresMigration(t *testing.T) {
	build := func(withMigration bool) *Aggregator {
		agg := NewAggregator(nil)
		mustAdd(t, agg, mkFull("old", "issuer", "r1", 1, 0, 100, 0, 50, AllStatusReasons, 1))
		mustAdd(t, agg, mkFull("a", "issuer", "r1", 1, 0, 50, 50, 100, AllStatusReasons, 2))
		mustAdd(t, agg, mkFull("b", "issuer", "r1", 1, 50, 100, 50, 100, AllStatusReasons, 3))
		if withMigration {
			mig := &Migration{
				Kind: "split", IssuerID: "issuer", FromEpoch: 1, ToEpoch: 1, OldRoot: "r1", NewRoot: "r1",
				Old: []PartitionSpan{{PartitionID: "old", SerialLo: SerialFromUint64(0), SerialHi: SerialFromUint64(100), Reasons: AllStatusReasons}},
				New: []PartitionSpan{
					{PartitionID: "a", SerialLo: SerialFromUint64(0), SerialHi: SerialFromUint64(50), Reasons: AllStatusReasons},
					{PartitionID: "b", SerialLo: SerialFromUint64(50), SerialHi: SerialFromUint64(100), Reasons: AllStatusReasons},
				},
				EffectiveAt: tm(50),
			}
			if err := agg.AddMigration(mig); err != nil {
				t.Fatalf("AddMigration: %v", err)
			}
		}
		return agg
	}
	wantStatus(t, build(false).Check(cert(10, 10, 90)), StatusUnknown)
	wantStatus(t, build(true).Check(cert(10, 10, 90)), StatusGood)
}

func TestMigrationBoundaryValidation(t *testing.T) {
	oldSpans := []PartitionSpan{{
		PartitionID: "old", SerialLo: SerialFromUint64(0), SerialHi: SerialFromUint64(100), Reasons: AllStatusReasons,
	}}
	base := Migration{
		Kind: "split", IssuerID: "issuer", FromEpoch: 1, ToEpoch: 2, OldRoot: "r1", NewRoot: "r2",
		Old: oldSpans, EffectiveAt: tm(100),
	}

	t.Run("serial gap", func(t *testing.T) {
		m := base
		m.New = []PartitionSpan{
			{PartitionID: "a", SerialLo: SerialFromUint64(0), SerialHi: SerialFromUint64(40), Reasons: AllStatusReasons},
			{PartitionID: "b", SerialLo: SerialFromUint64(60), SerialHi: SerialFromUint64(100), Reasons: AllStatusReasons},
		}
		if err := ValidateMigration(&m); err == nil {
			t.Fatal("expected boundary gap rejection")
		}
	})

	t.Run("serial overlap", func(t *testing.T) {
		m := base
		m.New = []PartitionSpan{
			{PartitionID: "a", SerialLo: SerialFromUint64(0), SerialHi: SerialFromUint64(60), Reasons: AllStatusReasons},
			{PartitionID: "b", SerialLo: SerialFromUint64(50), SerialHi: SerialFromUint64(100), Reasons: AllStatusReasons},
		}
		if err := ValidateMigration(&m); err == nil {
			t.Fatal("expected overlapping boundary rejection")
		}
	})

	t.Run("reason gap", func(t *testing.T) {
		m := base
		m.New = []PartitionSpan{{
			PartitionID: "a", SerialLo: SerialFromUint64(0), SerialHi: SerialFromUint64(100),
			Reasons: AllStatusReasons &^ MaskCertificateHold,
		}}
		if err := ValidateMigration(&m); err == nil {
			t.Fatal("expected reason boundary gap rejection")
		}
	})
}

func TestMigrationDualSignature(t *testing.T) {
	pub1, priv1, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub2, priv2, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	roots := NewEd25519Roots()
	if err := roots.AddRoot("r1", pub1); err != nil {
		t.Fatal(err)
	}
	if err := roots.AddRoot("r2", pub2); err != nil {
		t.Fatal(err)
	}
	agg := NewAggregator(roots)

	m1 := mkFull("old", "issuer", "r1", 1, 0, 100, 0, 100, AllStatusReasons, 1)
	if err := SignManifest(m1, "r1", priv1); err != nil {
		t.Fatal(err)
	}
	m2 := mkFull("new", "issuer", "r2", 2, 0, 100, 100, 200, AllStatusReasons, 1)
	if err := SignManifest(m2, "r2", priv2); err != nil {
		t.Fatal(err)
	}
	mustAdd(t, agg, m1)
	mustAdd(t, agg, m2)

	mig := &Migration{
		Kind: "merge", IssuerID: "issuer", FromEpoch: 1, ToEpoch: 2, OldRoot: "r1", NewRoot: "r2",
		Old:         []PartitionSpan{{PartitionID: "old", SerialLo: SerialFromUint64(0), SerialHi: SerialFromUint64(100), Reasons: AllStatusReasons}},
		New:         []PartitionSpan{{PartitionID: "new", SerialLo: SerialFromUint64(0), SerialHi: SerialFromUint64(100), Reasons: AllStatusReasons}},
		EffectiveAt: tm(100),
	}
	if err := agg.AddMigration(mig); err == nil || !errors.Is(err, ErrBadSignature) {
		t.Fatalf("unsigned migration should fail authentication, got %v", err)
	}
	if err := SignMigration(mig, priv1, priv2); err != nil {
		t.Fatal(err)
	}
	if err := agg.AddMigration(mig); err != nil {
		t.Fatalf("dual-signed migration rejected: %v", err)
	}
	wantStatus(t, agg.Check(cert(10, 10, 190)), StatusGood)
}

func TestForkLocksIssuerWithDualEvidence(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	roots := NewEd25519Roots()
	if err := roots.AddRoot("r1", pub); err != nil {
		t.Fatal(err)
	}
	agg := NewAggregator(roots)
	m1 := mkFull("p", "issuer", "r1", 1, 0, 100, 0, 100, AllStatusReasons, 1)
	if err := SignManifest(m1, "r1", priv); err != nil {
		t.Fatal(err)
	}
	m2 := mkFull("p", "issuer", "r1", 1, 0, 90, 0, 100, AllStatusReasons, 1,
		ent(50, 30, ReasonKeyCompromise, ""))
	if err := SignManifest(m2, "r1", priv); err != nil {
		t.Fatal(err)
	}
	mustAdd(t, agg, m1)
	err = agg.AddManifest(m2)
	if err == nil {
		t.Fatal("expected fork error")
	}
	if !errors.Is(err, ErrIssuerLocked) {
		t.Fatalf("fork error should unwrap to ErrIssuerLocked, got %v", err)
	}
	var fe *ForkError
	if !errors.As(err, &fe) {
		t.Fatalf("expected ForkError, got %T", err)
	}
	if fe.Evidence.First.Digest == ([32]byte{}) || fe.Evidence.Second.Digest == ([32]byte{}) {
		t.Fatal("fork evidence must contain both signing digests")
	}
	if len(fe.Evidence.First.Signature) == 0 || len(fe.Evidence.Second.Signature) == 0 {
		t.Fatal("fork evidence must contain both signatures")
	}
	if !agg.Locked("issuer") {
		t.Fatal("issuer should be locked")
	}
	wantStatus(t, agg.Check(cert(10, 10, 90)), StatusLocked)
}

func TestDisjointSameEpochDoesNotFork(t *testing.T) {
	agg := NewAggregator(nil)
	mustAdd(t, agg, mkFull("p1", "issuer", "r1", 1, 0, 50, 0, 100, AllStatusReasons, 1))
	mustAdd(t, agg, mkFull("p2", "issuer", "r1", 1, 50, 100, 0, 100, AllStatusReasons, 2))
	if agg.Locked("issuer") {
		t.Fatal("disjoint partitions must not lock the issuer")
	}
}

func TestEpochRegressionAndDelegation(t *testing.T) {
	t.Run("epoch regression rejected", func(t *testing.T) {
		agg := NewAggregator(nil)
		mustAdd(t, agg, mkFull("p", "issuer", "r1", 2, 0, 100, 0, 100, AllStatusReasons, 1))
		err := agg.AddManifest(mkFull("p", "issuer", "r1", 1, 0, 50, 0, 100, AllStatusReasons, 1))
		if !errors.Is(err, ErrEpochRegression) {
			t.Fatalf("expected epoch regression, got %v", err)
		}
	})

	t.Run("valid indirect delegation", func(t *testing.T) {
		m := mkFull("p", "issuer", "r1", 1, 0, 100, 0, 100, AllStatusReasons, 1)
		m.SignerID = "delegated-crl"
		m.Indirect = []Delegation{{
			Parent: "issuer", Child: "delegated-crl", Root: "r1",
			SerialLo: SerialFromUint64(0), SerialHi: SerialFromUint64(100),
			Reasons: AllStatusReasons, ValidFrom: tm(0), ValidUntil: tm(100),
		}}
		if err := ValidateManifest(m); err != nil {
			t.Fatalf("valid chain rejected: %v", err)
		}
	})

	t.Run("delegation scope must contain partition", func(t *testing.T) {
		m := mkFull("p", "issuer", "r1", 1, 0, 100, 0, 100, AllStatusReasons, 1)
		m.SignerID = "delegated-crl"
		m.Indirect = []Delegation{{
			Parent: "issuer", Child: "delegated-crl", Root: "r1",
			SerialLo: SerialFromUint64(0), SerialHi: SerialFromUint64(50),
			Reasons: AllStatusReasons, ValidFrom: tm(0), ValidUntil: tm(100),
		}}
		if err := ValidateManifest(m); err == nil || !strings.Contains(err.Error(), "serial scope") {
			t.Fatalf("expected serial scope error, got %v", err)
		}
	})

	t.Run("delegation root must match manifest root", func(t *testing.T) {
		m := mkFull("p", "issuer", "r1", 1, 0, 100, 0, 100, AllStatusReasons, 1)
		m.SignerID = "delegated-crl"
		m.Indirect = []Delegation{{
			Parent: "issuer", Child: "delegated-crl", Root: "r2",
			SerialLo: SerialFromUint64(0), SerialHi: SerialFromUint64(100),
			Reasons: AllStatusReasons, ValidFrom: tm(0), ValidUntil: tm(100),
		}}
		if err := ValidateManifest(m); err == nil {
			t.Fatal("expected root mismatch error")
		}
	})
}

func TestCacheIncrementalInvalidation(t *testing.T) {
	agg := NewAggregator(nil)
	mustAdd(t, agg, mkFull("p1", "issuer", "r1", 1, 0, 50, 0, 100, AllStatusReasons, 1))
	mustAdd(t, agg, mkFull("p2", "issuer", "r1", 1, 50, 100, 0, 100, AllStatusReasons, 1))
	cache := NewCache(agg)
	defer cache.Close()

	a := cert(10, 10, 40)
	b := cert(80, 60, 90)
	wantStatus(t, cache.Check(a), StatusGood)
	wantStatus(t, cache.Check(b), StatusGood)
	if cache.Len() != 2 {
		t.Fatalf("cache entries = %d, want 2", cache.Len())
	}
	wantStatus(t, cache.Check(a), StatusGood)
	if got := cache.Stats().Hits; got != 1 {
		t.Fatalf("hits = %d, want 1", got)
	}

	mustAdd(t, agg, mkDelta("p1", "issuer", "r1", 1, 0, 50, 30, 60, AllStatusReasons, 2, 1))
	if got := cache.Stats().Invalidated; got != 1 {
		t.Fatalf("invalidated = %d, want 1 (serial-disjoint entry must survive)", got)
	}
	if cache.Len() != 1 {
		t.Fatalf("cache entries = %d, want 1", cache.Len())
	}
	wantStatus(t, cache.Check(a), StatusGood)
	wantStatus(t, cache.Check(b), StatusGood)

	mustAdd(t, agg, mkDelta("p2", "issuer", "r1", 1, 50, 100, 40, 100, AllStatusReasons, 2, 1))
	if got := cache.Stats().Invalidated; got != 2 {
		t.Fatalf("invalidated = %d, want 2", got)
	}
	wantStatus(t, cache.Check(b), StatusGood)
}

func TestCacheReasonScopeInvalidation(t *testing.T) {
	agg := NewAggregator(nil)
	mustAdd(t, agg, mkFull("p", "issuer", "r1", 1, 0, 100, 0, 100, AllStatusReasons, 1))
	cache := NewCache(agg)
	defer cache.Close()

	req := cert(10, 10, 40)
	req.Reasons = MaskKeyCompromise
	wantStatus(t, cache.Check(req), StatusGood)
	baseInvalidated := cache.Stats().Invalidated

	// Same serial/time domain but a disjoint reason scope cannot affect a
	// keyCompromise-only query.
	mustAdd(t, agg, mkFull("q", "issuer", "r1", 1, 0, 100, 0, 100, MaskCertificateHold, 2))
	if got := cache.Stats().Invalidated; got != baseInvalidated {
		t.Fatalf("disjoint reason scope invalidated %d entries", got-baseInvalidated)
	}
	if cache.Len() != 1 {
		t.Fatalf("cache entries = %d, want 1", cache.Len())
	}
	wantStatus(t, cache.Check(req), StatusGood)

	// An overlapping reason scope does invalidate it.
	mustAdd(t, agg, mkFull("r", "issuer", "r1", 1, 0, 100, 20, 30, MaskKeyCompromise, 3))
	if got := cache.Stats().Invalidated; got != baseInvalidated+1 {
		t.Fatalf("overlapping reason scope invalidated = %d, want %d", got, baseInvalidated+1)
	}
}

func TestCacheLockInvalidatesIssuer(t *testing.T) {
	agg := NewAggregator(nil)
	mustAdd(t, agg, mkFull("p1", "issuer", "r1", 1, 0, 50, 0, 100, AllStatusReasons, 1))
	cache := NewCache(agg)
	defer cache.Close()
	wantStatus(t, cache.Check(cert(10, 10, 40)), StatusGood)
	if cache.Len() != 1 {
		t.Fatal("expected a cached entry")
	}
	// Same partition and generation with conflicting content triggers lock.
	_ = agg.AddManifest(mkFull("p1", "issuer", "r1", 1, 0, 50, 0, 100, AllStatusReasons, 1,
		ent(10, 20, ReasonKeyCompromise, "")))
	if cache.Len() != 0 {
		t.Fatalf("lock should invalidate all issuer entries, have %d", cache.Len())
	}
	wantStatus(t, cache.Check(cert(10, 10, 40)), StatusLocked)
}

func TestValidateSentinels(t *testing.T) {
	agg := NewAggregator(nil)
	mustAdd(t, agg, mkFull("p", "issuer", "r1", 1, 0, 100, 0, 100, AllStatusReasons, 1,
		ent(11, 20, ReasonKeyCompromise, "")))
	if err := agg.Validate(cert(10, 10, 90)); err != nil {
		t.Fatalf("good validation returned %v", err)
	}
	if err := agg.Validate(cert(11, 10, 90)); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked validation = %v, want ErrRevoked", err)
	}
	unknown := cert(12, 10, 90)
	unknown.IssuerID = "other-issuer"
	if err := agg.Validate(unknown); !errors.Is(err, ErrStatusUnknown) {
		t.Fatalf("unknown validation = %v, want ErrStatusUnknown", err)
	}
}

func TestCacheEpochSupersessionInvalidation(t *testing.T) {
	agg := NewAggregator(nil)
	mustAdd(t, agg, mkFull("p", "issuer", "r1", 1, 0, 100, 0, 100, AllStatusReasons, 1))
	cache := NewCache(agg)
	defer cache.Close()

	later := cert(10, 70, 90)
	wantStatus(t, cache.Check(later), StatusGood)
	if cache.Len() != 1 {
		t.Fatal("expected cached result")
	}
	// Epoch 2 starts at 50 and expires at 60. It must invalidate the later
	// cached lifetime because the lower epoch may not resume after epoch 2.
	mustAdd(t, agg, mkFull("p", "issuer", "r1", 2, 0, 100, 50, 60, AllStatusReasons, 2))
	if cache.Len() != 0 {
		t.Fatalf("epoch supersession should invalidate later entries, have %d", cache.Len())
	}
	wantStatus(t, cache.Check(later), StatusUnknown)
}

func TestCacheTreapInvalidatesExactlyIntersectingEntries(t *testing.T) {
	agg := NewAggregator(nil)
	for i := 0; i < 100; i++ {
		lo := uint64(i * 10)
		mustAdd(t, agg, mkFull(fmt.Sprintf("p%d", i), "issuer", "r1", 1, lo, lo+10, 0, 1000,
			AllStatusReasons, uint64(i+1)))
	}
	cache := NewCache(agg)
	defer cache.Close()
	for i := 0; i < 100; i++ {
		serial := uint64(i*10 + 5)
		wantStatus(t, cache.Check(cert(serial, 10, 20)), StatusGood)
	}
	if cache.Len() != 100 {
		t.Fatalf("cache entries = %d, want 100", cache.Len())
	}

	// Serial intersection selects exactly the first 50 entries.
	mustAdd(t, agg, mkFull("q", "issuer", "r1", 1, 0, 500, 15, 16, AllStatusReasons, 500))
	if got := cache.Stats().Invalidated; got != 50 {
		t.Fatalf("invalidated = %d, want 50", got)
	}
	if cache.Len() != 50 {
		t.Fatalf("cache entries = %d, want 50", cache.Len())
	}

	// Serial intersection selects the final 10 entries.
	mustAdd(t, agg, mkFull("q2", "issuer", "r1", 1, 900, 1000, 15, 16, AllStatusReasons, 501))
	if got := cache.Stats().Invalidated; got != 60 {
		t.Fatalf("invalidated = %d, want 60", got)
	}
	if cache.Len() != 40 {
		t.Fatalf("cache entries = %d, want 40", cache.Len())
	}

	// Same epoch but a time window entirely before every cached lifetime:
	// no entry is affected.
	mustAdd(t, agg, mkFull("q3", "issuer", "r1", 1, 0, 1000, 0, 5, AllStatusReasons, 502))
	if got := cache.Stats().Invalidated; got != 60 {
		t.Fatalf("non-overlapping time invalidated = %d, want 60", got-60)
	}
	if cache.Len() != 40 {
		t.Fatalf("cache entries = %d, want 40", cache.Len())
	}
}

func TestCacheInvalidatesWhenDeltaBridgesIntoLaterSibling(t *testing.T) {
	agg := NewAggregator(nil)
	mustAdd(t, agg, mkFull("p", "issuer", "r1", 1, 0, 100, 0, 50, AllStatusReasons, 1))
	// Later cumulative delta exists but its prefix has a hole.
	mustAdd(t, agg, mkDelta("p", "issuer", "r1", 1, 0, 100, 75, 100, AllStatusReasons, 3, 1))
	cache := NewCache(agg)
	defer cache.Close()

	later := cert(10, 76, 90)
	wantStatus(t, cache.Check(later), StatusUnknown)
	if cache.Len() != 1 {
		t.Fatal("expected cached unknown result")
	}
	// This delta's own window ends at 75, but it bridges into CRL 3 at 75,
	// so the cached [76,90) lifetime must be invalidated.
	mustAdd(t, agg, mkDelta("p", "issuer", "r1", 1, 0, 100, 50, 75, AllStatusReasons, 2, 1))
	if cache.Len() != 0 {
		t.Fatalf("bridging delta should invalidate later entry, have %d", cache.Len())
	}
	wantStatus(t, cache.Check(later), StatusGood)
}

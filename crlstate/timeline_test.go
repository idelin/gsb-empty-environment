package crlstate

import (
	"testing"
	"time"
)

type refSegment struct {
	from time.Time
	to   time.Time
	want Status
}

type timelineScenario struct {
	name       string
	manifests  []*Manifest
	migrations []*Migration
	cert       Certificate
	segments   []refSegment
}

func (s timelineScenario) truth(at time.Time) Status {
	for _, seg := range s.segments {
		if !at.Before(seg.from) && at.Before(seg.to) {
			return seg.want
		}
	}
	return StatusUnknown
}

// TestTimelineReferenceTruth checks every scenario at hour boundaries and
// half-hour points against a hand-authored reference timeline that is
// independent of the production coverage evaluator.
func TestTimelineReferenceTruth(t *testing.T) {
	all := AllStatusReasons
	splitMigration := &Migration{
		Kind: "split", IssuerID: "issuer", FromEpoch: 1, ToEpoch: 2, OldRoot: "r1", NewRoot: "r2",
		Old: []PartitionSpan{{PartitionID: "old", SerialLo: SerialFromUint64(0), SerialHi: SerialFromUint64(100), Reasons: all}},
		New: []PartitionSpan{
			{PartitionID: "new-a", SerialLo: SerialFromUint64(0), SerialHi: SerialFromUint64(50), Reasons: all},
			{PartitionID: "new-b", SerialLo: SerialFromUint64(50), SerialHi: SerialFromUint64(100), Reasons: all},
		},
		EffectiveAt: tm(100),
	}

	scenarios := []timelineScenario{
		{
			name:      "complete",
			manifests: []*Manifest{mkFull("p", "issuer", "r1", 1, 0, 100, 0, 100, all, 1)},
			cert:      cert(10, 10, 90),
			segments:  []refSegment{{tm(10), tm(90), StatusGood}},
		},
		{
			name:      "time gap",
			manifests: []*Manifest{mkFull("p", "issuer", "r1", 1, 0, 100, 0, 50, all, 1)},
			cert:      cert(10, 10, 90),
			segments:  []refSegment{{tm(10), tm(50), StatusGood}, {tm(50), tm(90), StatusUnknown}},
		},
		{
			name: "delta completes gap",
			manifests: []*Manifest{
				mkFull("p", "issuer", "r1", 1, 0, 100, 0, 50, all, 1),
				mkDelta("p", "issuer", "r1", 1, 0, 100, 50, 100, all, 2, 1),
			},
			cert:     cert(10, 10, 90),
			segments: []refSegment{{tm(10), tm(90), StatusGood}},
		},
		{
			name: "revocation instant",
			manifests: []*Manifest{mkFull("p", "issuer", "r1", 1, 0, 100, 0, 100, all, 1,
				ent(10, 50, ReasonKeyCompromise, ""))},
			cert:     cert(10, 10, 90),
			segments: []refSegment{{tm(10), tm(50), StatusGood}, {tm(50), tm(90), StatusRevoked}},
		},
		{
			name: "released hold is non-blocking across its history",
			manifests: []*Manifest{
				mkFull("p", "issuer", "r1", 1, 0, 50, 0, 50, all, 1,
					ent(10, 30, ReasonCertificateHold, "h")),
				mkDelta("p", "issuer", "r1", 1, 0, 50, 50, 100, all, 2, 1,
					ent(10, 60, ReasonRemoveFromCRL, "h")),
			},
			cert:     cert(10, 10, 90),
			segments: []refSegment{{tm(10), tm(90), StatusGood}},
		},
		{
			name: "split migration",
			manifests: []*Manifest{
				mkFull("old", "issuer", "r1", 1, 0, 100, 0, 100, all, 1),
				mkFull("new-a", "issuer", "r2", 2, 0, 50, 100, 200, all, 1),
				mkFull("new-b", "issuer", "r2", 2, 50, 100, 100, 200, all, 1),
			},
			migrations: []*Migration{splitMigration},
			cert:       cert(10, 10, 190),
			segments:   []refSegment{{tm(10), tm(190), StatusGood}},
		},
		{
			name: "revocation in new partition",
			manifests: []*Manifest{
				mkFull("old", "issuer", "r1", 1, 0, 100, 0, 100, all, 1),
				mkFull("new-a", "issuer", "r2", 2, 0, 50, 100, 200, all, 1,
					ent(10, 130, ReasonCACompromise, "")),
				mkFull("new-b", "issuer", "r2", 2, 50, 100, 100, 200, all, 1),
			},
			migrations: []*Migration{splitMigration},
			cert:       cert(10, 10, 190),
			segments:   []refSegment{{tm(10), tm(130), StatusGood}, {tm(130), tm(190), StatusRevoked}},
		},
		{
			name: "epoch gap",
			manifests: []*Manifest{
				mkFull("p", "issuer", "r1", 1, 0, 100, 0, 50, all, 1),
				mkFull("p", "issuer", "r1", 2, 0, 100, 60, 100, all, 1),
			},
			cert: cert(10, 10, 90),
			segments: []refSegment{
				{tm(10), tm(50), StatusGood},
				{tm(50), tm(60), StatusUnknown},
				{tm(60), tm(90), StatusGood},
			},
		},
		{
			name: "cross-root overlay",
			manifests: []*Manifest{
				mkFull("p1", "issuer", "r1", 1, 0, 100, 0, 100, MaskKeyCompromise, 1),
				mkFull("p2", "issuer", "r2", 1, 0, 100, 0, 100, MaskCertificateHold, 1),
			},
			cert:     cert(10, 10, 90),
			segments: []refSegment{{tm(10), tm(90), StatusUnknown}},
		},
		{
			name: "missing baseline dependency",
			manifests: []*Manifest{
				mkFull("p", "issuer", "r1", 1, 0, 100, 0, 50, all, 1),
				mkDelta("p", "issuer", "r1", 1, 0, 100, 50, 100, all, 4, 3),
			},
			cert:     cert(10, 10, 90),
			segments: []refSegment{{tm(10), tm(50), StatusGood}, {tm(50), tm(90), StatusUnknown}},
		},
		{
			name: "reason gap",
			manifests: []*Manifest{
				mkFull("p", "issuer", "r1", 1, 0, 100, 0, 100, AllStatusReasons&^MaskCertificateHold, 1),
			},
			cert:     cert(10, 10, 90),
			segments: []refSegment{{tm(10), tm(90), StatusUnknown}},
		},
		{
			name: "unreleased hold",
			manifests: []*Manifest{
				mkFull("p", "issuer", "r1", 1, 0, 100, 0, 100, all, 1,
					ent(10, 40, ReasonCertificateHold, "h")),
			},
			cert:     cert(10, 10, 90),
			segments: []refSegment{{tm(10), tm(40), StatusGood}, {tm(40), tm(90), StatusRevoked}},
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			agg := NewAggregator(nil)
			for _, m := range sc.manifests {
				if err := agg.AddManifest(m); err != nil {
					t.Fatalf("AddManifest(%s): %v", m.PartitionID, err)
				}
			}
			for _, mig := range sc.migrations {
				if err := agg.AddMigration(mig); err != nil {
					t.Fatalf("AddMigration: %v", err)
				}
			}
			points := timelinePoints(sc.cert.Start, sc.cert.End)
			for _, at := range points {
				got := agg.CheckAt(sc.cert, at)
				want := sc.truth(at)
				if got.Status != want {
					t.Fatalf("point %s: status = %s, want %s (result %+v)", at, got.Status, want, got)
				}
			}
		})
	}
}

func timelinePoints(start, end time.Time) []time.Time {
	var out []time.Time
	for at := start; at.Before(end); at = at.Add(30 * time.Minute) {
		out = append(out, at)
	}
	return out
}

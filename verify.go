package timemap

import (
	"fmt"
	"sort"
)

// Check is one named verification result.
type Check struct {
	Name   string
	OK     bool
	Detail string
}

// Verify runs the structural checks on a built result: monotonicity of the
// shared mapping, exact round-trip of every key point (events and protected
// boundaries), non-overlap between compressed and protected segments, and
// preservation of event order and simultaneity.
func Verify(res *Result, spec Spec) []Check {
	var checks []Check
	m := res.Mapping

	pts := m.Points()
	mono := true
	for i := 1; i < len(pts); i++ {
		if pts[i-1].X.Cmp(pts[i].X) >= 0 || pts[i-1].Y.Cmp(pts[i].Y) >= 0 {
			mono = false
		}
	}
	checks = append(checks, Check{
		Name:   "mapping strictly increasing",
		OK:     mono,
		Detail: fmt.Sprintf("%d breakpoints", len(pts)),
	})

	keyPoints := map[string]Rat{}
	for _, t := range spec.Events {
		keyPoints[t.String()] = t
	}
	for _, iv := range spec.Protected {
		keyPoints[iv.Start.String()] = iv.Start
		keyPoints[iv.End.String()] = iv.End
	}
	roundTrip := true
	worst := ""
	for _, t := range keyPoints {
		back := m.Inverse(m.Forward(t))
		if !back.Equal(t) {
			roundTrip = false
			worst = fmt.Sprintf("t=%s round-trips to %s", t, back)
			break
		}
	}
	detail := fmt.Sprintf("%d key points invert exactly", len(keyPoints))
	if !roundTrip {
		detail = worst
	}
	checks = append(checks, Check{Name: "key-point round-trip", OK: roundTrip, Detail: detail})

	overlap := true
	overlapDetail := "no compressed segment overlaps a protected segment"
	for _, seg := range res.Segments {
		if seg.Kind != SegIdleCompressed {
			continue
		}
		for _, iv := range spec.Protected {
			if seg.OriginalInterval().Overlaps(iv) {
				overlap = false
				overlapDetail = fmt.Sprintf("compressed [%s,%s) overlaps protected %q [%s,%s)",
					seg.Start, seg.End, iv.ID, iv.Start, iv.End)
			}
		}
	}
	checks = append(checks, Check{Name: "compressed/protected disjoint", OK: overlap, Detail: overlapDetail})

	events := append([]Rat(nil), spec.Events...)
	sort.Slice(events, func(i, j int) bool { return events[i].Cmp(events[j]) < 0 })
	orderOK, simOK := true, true
	for i := 1; i < len(events); i++ {
		a, b := m.Forward(events[i-1]), m.Forward(events[i])
		switch events[i-1].Cmp(events[i]) {
		case 0:
			if !a.Equal(b) {
				simOK = false
			}
		default:
			if a.Cmp(b) >= 0 {
				orderOK = false
			}
		}
	}
	checks = append(checks, Check{
		Name:   "event order preserved",
		OK:     orderOK,
		Detail: fmt.Sprintf("%d events strictly increase after mapping", len(events)),
	})
	checks = append(checks, Check{
		Name:   "simultaneity preserved",
		OK:     simOK,
		Detail: "events at equal times map to equal times",
	})
	return checks
}

// SyncCheck verifies one cross-track sync point: the shared mapping must send
// every track to the same compressed time, and that time must land inside each
// track's own frame bucket for the frame index the track computes.
type SyncCheck struct {
	Time        Rat
	Compressed  Rat
	OK          bool
	PerTrack    []TrackSync
	Description string
}

// TrackSync records one track's sampling of a sync point.
type TrackSync struct {
	TrackID   string
	Frame     string
	BucketLo  Rat
	BucketHi  Rat
	Contained bool
}

// CheckSyncPoint evaluates a sync point across tracks.
func CheckSyncPoint(m *Mapping, tracks []Track, t Rat) SyncCheck {
	compressed := m.Forward(t)
	sc := SyncCheck{Time: t, Compressed: compressed, OK: true}
	for _, tr := range tracks {
		n, err := tr.FrameIndex(m, compressed)
		ts := TrackSync{TrackID: tr.ID}
		if err != nil {
			sc.OK = false
			ts.Frame = "error: " + err.Error()
			sc.PerTrack = append(sc.PerTrack, ts)
			continue
		}
		lo, hi, err := tr.FrameBucket(m, n)
		if err != nil {
			sc.OK = false
			ts.Frame = "error: " + err.Error()
			sc.PerTrack = append(sc.PerTrack, ts)
			continue
		}
		ts.Frame = n.String()
		ts.BucketLo, ts.BucketHi = lo, hi
		ts.Contained = lo.Cmp(compressed) <= 0 && compressed.Cmp(hi) < 0
		if !ts.Contained {
			sc.OK = false
		}
		sc.PerTrack = append(sc.PerTrack, ts)
	}
	sc.Description = fmt.Sprintf("t=%s maps to %s across %d tracks", t, compressed, len(tracks))
	return sc
}

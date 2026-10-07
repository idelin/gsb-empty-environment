package timemap

import (
	"errors"
	"fmt"
	"math/big"
)

// Track is a synchronized media track with a rational start offset and a
// rational sampling interval on the original timeline.
type Track struct {
	ID       string
	Start    Rat // rational start offset (original timeline)
	Interval Rat // sampling interval, e.g. 1/30 for 30 fps
}

// MappedStart returns the track's start on the compressed timeline.
func (t Track) MappedStart(m *Mapping) Rat { return m.Forward(t.Start) }

// FrameIndex computes the frame number for a compressed-timeline time using
// the unified round-half-even rule shared by all tracks.
func (t Track) FrameIndex(m *Mapping, compressedTime Rat) (*big.Int, error) {
	if t.Interval.Sign() <= 0 {
		return nil, fmt.Errorf("track %q: interval must be positive", t.ID)
	}
	phase := compressedTime.Sub(t.MappedStart(m)).Div(t.Interval)
	return RoundHalfEven(phase), nil
}

// FrameBucket returns the half-open compressed-time interval [lo, hi) whose
// times round to frame n under round-half-even.
func (t Track) FrameBucket(m *Mapping, n *big.Int) (lo, hi Rat, err error) {
	if t.Interval.Sign() <= 0 {
		return Rat{}, Rat{}, fmt.Errorf("track %q: interval must be positive", t.ID)
	}
	half := t.Interval.Div(RatFromInt(2))
	center := t.MappedStart(m).Add(t.Interval.Mul(RatFromBig(new(big.Rat).SetInt(n))))
	return center.Sub(half), center.Add(half), nil
}

// ReverseFrame locates frame n on the original timeline: it inverts the shared
// mapping at both bucket edges and returns the half-open original interval
// [lo, hi) that lands in frame n.
func (t Track) ReverseFrame(m *Mapping, n *big.Int) (lo, hi Rat, err error) {
	clo, chi, err := t.FrameBucket(m, n)
	if err != nil {
		return Rat{}, Rat{}, err
	}
	return m.Inverse(clo), m.Inverse(chi), nil
}

// OffsetEvidence documents how one track sits on the compressed timeline.
type OffsetEvidence struct {
	TrackID       string
	OriginalStart Rat
	MappedStart   Rat
	Interval      Rat
	Shift         Rat // MappedStart - OriginalStart: shift introduced by compression
}

// Evidence collects per-track offset evidence for a shared mapping.
func Evidence(tracks []Track, m *Mapping) ([]OffsetEvidence, error) {
	if m == nil {
		return nil, errors.New("nil mapping")
	}
	out := make([]OffsetEvidence, 0, len(tracks))
	for _, t := range tracks {
		if t.Interval.Sign() <= 0 {
			return nil, fmt.Errorf("track %q: interval must be positive", t.ID)
		}
		ms := t.MappedStart(m)
		out = append(out, OffsetEvidence{
			TrackID:       t.ID,
			OriginalStart: t.Start,
			MappedStart:   ms,
			Interval:      t.Interval,
			Shift:         ms.Sub(t.Start),
		})
	}
	return out, nil
}

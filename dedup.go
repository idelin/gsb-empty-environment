package main

import (
	"fmt"
	"sort"
)

// ErrGapTableFull is returned when the sparse gap table reaches its bound.
// The receiver is expected to trigger repair (retransmission of the missing
// contiguous prefix) before accepting more out-of-order messages.
var ErrGapTableFull = fmt.Errorf("dedup: gap table full")

// SourceWindow is the bounded dedup history for one message source.
//
// Memory layout per source:
//   - watermark: highest contiguous sequence number seen. Everything
//     <= watermark is implicitly "seen" and costs O(1).
//   - gaps: sparse table of out-of-order arrivals above the watermark.
//     Bounded by maxGaps; entries are absorbed into the watermark as the
//     missing prefix is repaired.
//   - floor: compression horizon. Everything <= floor has been confirmed
//     by all valid subscribers, so it will never be retransmitted and the
//     metadata for it may be forgotten. floor never exceeds watermark.
type SourceWindow struct {
	watermark uint64
	gaps      map[uint64]struct{}
	floor     uint64
	maxGaps   int
}

func NewSourceWindow(maxGaps int) *SourceWindow {
	return &SourceWindow{gaps: make(map[uint64]struct{}), maxGaps: maxGaps}
}

func (w *SourceWindow) Watermark() uint64 { return w.watermark }
func (w *SourceWindow) Floor() uint64     { return w.floor }
func (w *SourceWindow) GapLen() int       { return len(w.gaps) }

// Gaps returns the sparse gap entries in ascending order (for inspection).
func (w *SourceWindow) Gaps() []uint64 {
	out := make([]uint64, 0, len(w.gaps))
	for seq := range w.gaps {
		out = append(out, seq)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Seen reports whether seq is already accounted for by the history:
// below the contiguous watermark, below the compressed floor, or present
// in the sparse gap table.
func (w *SourceWindow) Seen(seq uint64) bool {
	if seq <= w.watermark || seq <= w.floor {
		return true
	}
	_, ok := w.gaps[seq]
	return ok
}

// Add records seq. It reports false when seq is a duplicate.
// When the arrival closes the contiguous prefix, the watermark advances
// and any newly contiguous gap entries are absorbed into it.
func (w *SourceWindow) Add(seq uint64) (advanced bool, err error) {
	if w.Seen(seq) {
		return false, nil
	}
	if seq > w.watermark+1 {
		if len(w.gaps) >= w.maxGaps {
			return false, ErrGapTableFull
		}
		w.gaps[seq] = struct{}{}
		return false, nil
	}
	w.watermark = seq
	advanced = true
	for {
		next := w.watermark + 1
		if _, ok := w.gaps[next]; !ok {
			return advanced, nil
		}
		delete(w.gaps, next)
		w.watermark = next
	}
}

// Compress forgets dedup metadata at or below floor and raises the
// compression horizon. It must only be called with a floor confirmed by
// every valid subscriber; the cluster enforces this. floor may not exceed
// the watermark: raising the horizon past unreceived messages would make
// later catch-up retransmissions look like duplicates and lose them.
func (w *SourceWindow) Compress(floor uint64) error {
	if floor > w.watermark {
		return fmt.Errorf("dedup: compress floor %d exceeds watermark %d", floor, w.watermark)
	}
	if floor <= w.floor {
		return nil
	}
	// Defensive: gap entries always sit above the watermark (hence above
	// floor), but drop any that the horizon has caught up with.
	for seq := range w.gaps {
		if seq <= floor {
			delete(w.gaps, seq)
		}
	}
	w.floor = floor
	return nil
}

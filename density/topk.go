package density

import (
	"container/heap"
	"time"
)

// Hotspot is one ranked result of a TopK query: a grid tile with its
// aggregated weight over the queried spatio-temporal window.
type Hotspot struct {
	Lat    float64 // tile center latitude
	Lng    float64 // tile center longitude
	Weight float64 // aggregated weight in the window
	CellX  int32   // tile x index
	CellY  int32   // tile y index
}

// hotspotHeap is a min-heap by Weight, keeping the K heaviest tiles on top.
type hotspotHeap []Hotspot

func (h hotspotHeap) Len() int            { return len(h) }
func (h hotspotHeap) Less(i, j int) bool  { return h[i].Weight < h[j].Weight }
func (h hotspotHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *hotspotHeap) Push(v interface{}) { *h = append(*h, v.(Hotspot)) }
func (h *hotspotHeap) Pop() interface{} {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}

// TopK aggregates all cells whose hourly bucket intersects [from, to) and
// returns the K tiles with the highest total weight, sorted descending.
//
// The scan is O(C + T log K) where C is the number of stored cells and T the
// number of distinct tiles in the window; the heap holds at most K entries,
// so query memory is bounded regardless of stream volume.
func (c *Cube) TopK(from, to time.Time, k int) []Hotspot {
	if k <= 0 || !from.Before(to) {
		return nil
	}
	lo := hourOf(from)
	hi := hourOf(to.Add(-1)) // last bucket intersecting [from, to)

	// Aggregate weights per tile across all hours in the window.
	agg := make(map[uint64]float64)
	for key, w := range c.cells {
		hour, _, _ := unpack(key)
		if hour < lo || hour > hi {
			continue
		}
		tileKey := key & (1<<hourShift - 1) // strip the hour bits
		agg[tileKey] += w
	}

	h := &hotspotHeap{}
	for tileKey, w := range agg {
		if h.Len() < k {
			heap.Push(h, Hotspot{Weight: w, CellX: unpackX(tileKey), CellY: unpackY(tileKey)})
		} else if w > (*h)[0].Weight {
			(*h)[0] = Hotspot{Weight: w, CellX: unpackX(tileKey), CellY: unpackY(tileKey)}
			heap.Fix(h, 0)
		}
	}

	// Pop ascending, then reverse for descending order.
	out := make([]Hotspot, h.Len())
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = heap.Pop(h).(Hotspot)
	}
	for i := range out {
		out[i].Lat, out[i].Lng = c.grid.Center(out[i].CellX, out[i].CellY)
	}
	return out
}

func unpackX(tileKey uint64) int32 {
	return int32(int64(tileKey>>coordBits&coordMask) - coordBias)
}

func unpackY(tileKey uint64) int32 {
	return int32(int64(tileKey&coordMask) - coordBias)
}

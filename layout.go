package netsec

import (
	"fmt"
	"math"
	"sort"
)

// SimulateLayout extends every supplied out-of-bounds write across adjacent
// slots.  It returns only impacts that reach a control slot or another object
// classified safe.  Control slots must already have concrete offsets in the
// input layout; this function never places them.
func SimulateLayout(layout Layout, classSource any, writes []UnsafeWrite) []LayoutImpact {
	if len(layout.Slots) == 0 || len(writes) == 0 {
		return nil
	}
	classes := normalizeClasses(classSource)
	slots := append([]LayoutSlot(nil), layout.Slots...)
	sort.SliceStable(slots, func(i, j int) bool {
		if slots[i].start() != slots[j].start() {
			return slots[i].start() < slots[j].start()
		}
		return slots[i].id() < slots[j].id()
	})
	byObject := map[ObjectID]int{}
	for i, slot := range slots {
		if id := slot.objectID(); id != "" {
			if _, exists := byObject[id]; !exists {
				byObject[id] = i
			}
		}
	}

	var impacts []LayoutImpact
	seen := map[string]bool{}
	for _, write := range writes {
		start, end, ok := writeInterval(write, slots, byObject)
		if !ok || end <= start {
			continue
		}
		impact := LayoutImpact{Source: write.Object, Write: write}
		touched := map[string]bool{}
		for _, slot := range slots {
			if slot.size() == 0 {
				continue
			}
			ss, ok := unsignedToInt64(slot.start())
			if !ok || ss == math.MaxInt64 {
				continue
			}
			se, overflow := addInt64(ss, int64(minUint64(slot.size(), math.MaxInt64)))
			if overflow {
				se = math.MaxInt64
			}
			overlap := overlapBytes(start, end, ss, se)
			if overlap == 0 {
				continue
			}
			id := slot.id()
			if id != "" {
				touched[id] = true
				impact.Touched = append(impact.Touched, id)
			}
			impact.Bytes += overlap
			if slot.isControl() {
				impact.Control = true
				continue
			}
			other := slot.objectID()
			if other == "" || other == write.Object {
				continue
			}
			if classes[other] == ClassSafe || slot.Kind == SlotSafe {
				impact.SafeObjects = append(impact.SafeObjects, other)
			}
		}
		if !impact.Serious() {
			continue
		}
		impact.Touched = uniqueStrings(impact.Touched)
		impact.SafeObjects = uniqueObjectIDs(impact.SafeObjects)
		key := impactKey(impact)
		if seen[key] {
			continue
		}
		seen[key] = true
		impacts = append(impacts, impact)
	}
	sort.Slice(impacts, func(i, j int) bool {
		if impacts[i].Source != impacts[j].Source {
			return impacts[i].Source < impacts[j].Source
		}
		ai := impacts[i].Write.normalizedOffset()
		aj := impacts[j].Write.normalizedOffset()
		return ai < aj
	})
	return impacts
}

// Simulate uses the Layout attached to a Graph and a reference report.
func Simulate(g any, report *Report, writes []UnsafeWrite) []LayoutImpact {
	graph, ok := coerceGraph(g)
	if !ok || graph.Layout == nil || report == nil {
		return nil
	}
	return SimulateLayout(*graph.Layout, report, writes)
}

func normalizeClasses(src any) map[ObjectID]StackClass {
	switch v := src.(type) {
	case nil:
		return nil
	case map[ObjectID]StackClass:
		return v
	case map[string]StackClass:
		out := make(map[ObjectID]StackClass, len(v))
		for id, class := range v {
			out[ObjectID(id)] = class
		}
		return out
	case *Report:
		if v == nil {
			return nil
		}
		return v.Classes
	case Report:
		return v.Classes
	default:
		return nil
	}
}

// Simulate is the Report-oriented form of SimulateLayout.
func (r *Report) Simulate(layout Layout, writes []UnsafeWrite) []LayoutImpact {
	if r == nil {
		return nil
	}
	return SimulateLayout(layout, r, writes)
}

func writeInterval(write UnsafeWrite, slots []LayoutSlot, byObject map[ObjectID]int) (int64, int64, bool) {
	size := write.Size
	if size == 0 {
		if write.OffsetMax >= write.OffsetMin {
			size = uint64(write.OffsetMax-write.OffsetMin) + 1
		} else {
			size = 1
		}
	}
	if size > math.MaxInt64 {
		size = math.MaxInt64
	}
	var start int64
	if write.Absolute {
		var ok bool
		start, ok = unsignedToInt64(write.Address)
		if !ok {
			start = math.MaxInt64
		}
	} else {
		idx, ok := byObject[write.Object]
		if !ok {
			return 0, 0, false
		}
		slotStart, ok := unsignedToInt64(slots[idx].start())
		if !ok {
			return 0, 0, false
		}
		start = addSaturated(slotStart, write.normalizedOffset())
	}
	end := addSaturated(start, int64(size))
	if end < start {
		end = math.MaxInt64
	}
	return start, end, true
}

func overlapBytes(aStart, aEnd, bStart, bEnd int64) uint64 {
	start := aStart
	if bStart > start {
		start = bStart
	}
	end := aEnd
	if bEnd < end {
		end = bEnd
	}
	if end <= start {
		return 0
	}
	// The sum of overlaps is only used as a size heuristic; saturate rather
	// than wrapping.
	return uint64(end - start)
}

func unsignedToInt64(v uint64) (int64, bool) {
	if v > math.MaxInt64 {
		return 0, false
	}
	return int64(v), true
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func uniqueStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func uniqueObjectIDs(in []ObjectID) []ObjectID {
	if len(in) == 0 {
		return nil
	}
	seen := map[ObjectID]bool{}
	out := make([]ObjectID, 0, len(in))
	for _, id := range in {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func impactKey(i LayoutImpact) string {
	key := fmt.Sprintf("%s|%s|%d|%d", i.Source, i.Write.Source, i.Write.normalizedOffset(), i.Write.Size)
	for _, id := range i.SafeObjects {
		key += "|" + string(id)
	}
	if i.Control {
		key += "|control"
	}
	return key
}

// BuildLayout assigns offsets to managed local objects in Order (input order
// when Order ties).  Control metadata is intentionally excluded; callers must
// append externally-addressed control slots themselves.
func BuildLayout(objects []Object, base uint64) Layout {
	ordered := append([]Object(nil), objects...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Order < ordered[j].Order
	})
	layout := Layout{}
	offset := base
	for _, obj := range ordered {
		if obj.isControl() || obj.normalizedID() == "" {
			continue
		}
		align := obj.normalizedAlign()
		if align > 1 {
			rem := offset % align
			if rem != 0 {
				offset += align - rem
			}
		}
		layout.Slots = append(layout.Slots, LayoutSlot{
			Object: obj.normalizedID(),
			Offset: offset,
			Size:   obj.sizeBytes(),
			Align:  align,
			Kind:   SlotLocal,
		})
		offset += obj.sizeBytes()
	}
	return layout
}

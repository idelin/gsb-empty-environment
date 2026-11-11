package netsec

import (
	"errors"
	"fmt"
	"sort"
	"sync/atomic"
)

// idSpace is the number of distinct IDs in a uint32 identifier space.
const idSpace = uint64(1) << 32

// Sentinel errors returned by the package. All validation failures wrap one
// of these values, so callers can use errors.Is.
var (
	// ErrZeroLength is returned when an entry has Length == 0.
	ErrZeroLength = errors.New("netsec: mapping entry has zero length")

	// ErrIntervalOverflow is returned when an entry's container or parent
	// interval extends past the end of the uint32 ID space.
	ErrIntervalOverflow = errors.New("netsec: mapping interval overflows the ID space")

	// ErrOverlappingEntries is returned when two entries in the same layer
	// overlap, either in container coordinates or in parent coordinates.
	ErrOverlappingEntries = errors.New("netsec: overlapping mapping entries")

	// ErrEmptyInterval is returned when a query interval has length zero.
	ErrEmptyInterval = errors.New("netsec: query interval has zero length")

	// ErrNilLayer is returned when a nil layer is published on a chain.
	ErrNilLayer = errors.New("netsec: nil layer published")

	// ErrNoVersion is returned when a query runs against a nil Version
	// (for example a chain that has never been published).
	ErrNoVersion = errors.New("netsec: no published mapping version")
)

// Entry is one translation triple inside a Layer: the half-open container
// interval [ContainerStart, ContainerStart+Length) maps bijectively onto the
// parent interval [ParentStart, ParentStart+Length) with a constant offset.
type Entry struct {
	ContainerStart uint32
	ParentStart    uint32
	Length         uint32
}

func containerEnd(e Entry) uint64 { return uint64(e.ContainerStart) + uint64(e.Length) }
func parentEnd(e Entry) uint64    { return uint64(e.ParentStart) + uint64(e.Length) }

// Layer is an immutable, validated set of mapping entries. Within a layer the
// container intervals are pairwise disjoint and the parent intervals are
// pairwise disjoint; neither kind of interval may overflow the uint32 ID
// space. The zero value is not usable; create layers with NewLayer.
type Layer struct {
	entries []Entry
}

// NewLayer validates entries and returns an immutable Layer. The input slice
// is copied and may be reused or mutated by the caller afterwards.
func NewLayer(entries ...Entry) (*Layer, error) {
	cp := make([]Entry, len(entries))
	copy(cp, entries)

	for i, e := range cp {
		if e.Length == 0 {
			return nil, fmt.Errorf("%w: entry %d", ErrZeroLength, i)
		}
		if containerEnd(e) > idSpace {
			return nil, fmt.Errorf("%w: entry %d container interval [%d,+%d)",
				ErrIntervalOverflow, i, e.ContainerStart, e.Length)
		}
		if parentEnd(e) > idSpace {
			return nil, fmt.Errorf("%w: entry %d parent interval [%d,+%d)",
				ErrIntervalOverflow, i, e.ParentStart, e.Length)
		}
	}

	sort.Slice(cp, func(i, j int) bool { return cp[i].ContainerStart < cp[j].ContainerStart })
	for i := 1; i < len(cp); i++ {
		if containerEnd(cp[i-1]) > uint64(cp[i].ContainerStart) {
			return nil, fmt.Errorf("%w: container intervals of entries %d and %d",
				ErrOverlappingEntries, i-1, i)
		}
	}

	byParent := make([]Entry, len(cp))
	copy(byParent, cp)
	sort.Slice(byParent, func(i, j int) bool { return byParent[i].ParentStart < byParent[j].ParentStart })
	for i := 1; i < len(byParent); i++ {
		if parentEnd(byParent[i-1]) > uint64(byParent[i].ParentStart) {
			return nil, fmt.Errorf("%w: parent intervals of entries %d and %d",
				ErrOverlappingEntries, i-1, i)
		}
	}

	return &Layer{entries: cp}, nil
}

// Entries returns a copy of the layer's entries, sorted by ContainerStart.
func (l *Layer) Entries() []Entry {
	cp := make([]Entry, len(l.entries))
	copy(cp, l.entries)
	return cp
}

// piece is a half-open interval tracked while walking the layer chain.
// lo/length use the coordinate system of the layer currently being applied;
// cLo is the start of the same interval in the original container space, so
// holes can always be reported with container coordinates. length is uint64
// because intermediate arithmetic reaches up to 2^32.
type piece struct {
	lo     uint32
	length uint64
	cLo    uint64
	hole   bool
}

// apply splits every mapped piece at this layer's entry boundaries, emitting
// translated pieces for covered sub-intervals and hole pieces for uncovered
// ones. Existing holes pass through unchanged.
func (l *Layer) apply(pieces []piece) []piece {
	out := make([]piece, 0, len(pieces))
	es := l.entries

	for _, p := range pieces {
		if p.hole {
			// A hole established at an inner layer stays a hole for every
			// outer layer and must never be looked up or translated again.
			out = append(out, p)
			continue
		}

		cur := uint64(p.lo)
		remaining := p.length
		consumed := uint64(0)

		for remaining > 0 {
			// First entry whose container interval could contain cur.
			idx := sort.Search(len(es), func(i int) bool { return containerEnd(es[i]) > cur })

			var take uint64
			if idx < len(es) && uint64(es[idx].ContainerStart) <= cur {
				e := es[idx]
				take = min(remaining, containerEnd(e)-cur)
				out = append(out, piece{
					lo:     e.ParentStart + uint32(cur-uint64(e.ContainerStart)),
					length: take,
					cLo:    p.cLo + consumed,
				})
			} else {
				gapEnd := idSpace
				if idx < len(es) {
					gapEnd = uint64(es[idx].ContainerStart)
				}
				take = min(remaining, gapEnd-cur)
				out = append(out, piece{
					lo:     uint32(cur),
					length: take,
					cLo:    p.cLo + consumed,
					hole:   true,
				})
			}

			cur += take
			remaining -= take
			consumed += take
		}
	}

	return out
}

// Shard is a fully mapped container sub-interval and its host location.
type Shard struct {
	ContainerStart uint32
	HostStart      uint32
	Length         uint32
}

// Hole is a container sub-interval that no layer entry covers. It must never
// be reported as mapped, not even to host ID 0.
type Hole struct {
	ContainerStart uint32
	Length         uint32
}

// MapResult is the exact partition of a queried container interval: every
// queried ID belongs to exactly one Shard or one Hole.
type MapResult struct {
	Shards []Shard
	Holes  []Hole
}

// Version is an immutable snapshot of a full layer chain. Versions remain
// usable forever, even after newer versions are published, so a security
// monitor can pin its queries to one consistent view of the world.
type Version struct {
	seq    uint64
	layers []*Layer
}

// Sequence returns the monotonically increasing publication number, starting
// at 1 for the first successful Publish.
func (v *Version) Sequence() uint64 { return v.seq }

// Layers returns a copy of the layer chain, ordered from the innermost
// (container) layer to the outermost (host) layer.
func (v *Version) Layers() []*Layer {
	cp := make([]*Layer, len(v.layers))
	copy(cp, v.layers)
	return cp
}

// MapInterval composes the whole layer chain over the half-open container
// interval [start, start+length) and returns its precise host shards and
// unmapped holes. All arithmetic is performed in 64 bits so intervals ending
// at math.MaxUint32 cannot wrap.
func (v *Version) MapInterval(containerStart uint32, length uint32) (*MapResult, error) {
	if v == nil {
		return nil, ErrNoVersion
	}
	if length == 0 {
		return nil, fmt.Errorf("%w: start %d", ErrEmptyInterval, containerStart)
	}

	pieces := []piece{{
		lo:     containerStart,
		length: uint64(length),
		cLo:    uint64(containerStart),
	}}
	for _, layer := range v.layers {
		pieces = layer.apply(pieces)
	}

	res := &MapResult{}
	for _, p := range pieces {
		// With zero published layers every ID is unmapped: a piece is only a
		// host shard if it stayed mapped through the whole chain.
		if p.hole || len(v.layers) == 0 {
			res.Holes = append(res.Holes, Hole{
				ContainerStart: uint32(p.cLo),
				Length:         uint32(p.length),
			})
		} else {
			res.Shards = append(res.Shards, Shard{
				ContainerStart: uint32(p.cLo),
				HostStart:      p.lo,
				Length:         uint32(p.length),
			})
		}
	}

	sort.Slice(res.Shards, func(i, j int) bool {
		return res.Shards[i].ContainerStart < res.Shards[j].ContainerStart
	})
	sort.Slice(res.Holes, func(i, j int) bool {
		return res.Holes[i].ContainerStart < res.Holes[j].ContainerStart
	})
	res.Shards = mergeShards(res.Shards)
	res.Holes = mergeHoles(res.Holes)
	return res, nil
}

func mergeShards(shards []Shard) []Shard {
	if len(shards) <= 1 {
		return shards
	}
	merged := shards[:1]
	for _, s := range shards[1:] {
		prev := &merged[len(merged)-1]
		containerAdjacent := uint64(prev.ContainerStart)+uint64(prev.Length) == uint64(s.ContainerStart)
		hostAdjacent := uint64(prev.HostStart)+uint64(prev.Length) == uint64(s.HostStart)
		if containerAdjacent && hostAdjacent {
			prev.Length += s.Length
		} else {
			merged = append(merged, s)
		}
	}
	return merged
}

func mergeHoles(holes []Hole) []Hole {
	if len(holes) <= 1 {
		return holes
	}
	merged := holes[:1]
	for _, h := range holes[1:] {
		prev := &merged[len(merged)-1]
		if uint64(prev.ContainerStart)+uint64(prev.Length) == uint64(h.ContainerStart) {
			prev.Length += h.Length
		} else {
			merged = append(merged, h)
		}
	}
	return merged
}

// MapID maps a single container ID. The boolean result is mandatory: a false
// value means the ID falls in an unmapped hole and hostID must be ignored, so
// a hole can never be silently interpreted as host ID 0.
func (v *Version) MapID(containerID uint32) (hostID uint32, mapped bool, err error) {
	res, err := v.MapInterval(containerID, 1)
	if err != nil {
		return 0, false, err
	}
	if len(res.Shards) == 0 {
		return 0, false, nil
	}
	return res.Shards[0].HostStart, true, nil
}

// Chain publishes immutable mapping versions atomically. The zero value is
// ready to use.
type Chain struct {
	current atomic.Pointer[Version]
}

// NewChain creates an empty chain whose Current method returns nil until the
// first Publish.
func NewChain() *Chain { return &Chain{} }

// Publish atomically installs a new immutable version built from the given
// layer chain, ordered from the innermost (container) layer to the outermost
// (host) layer. The returned Version is immediately the chain's Current.
func (c *Chain) Publish(layers ...*Layer) (*Version, error) {
	for i, layer := range layers {
		if layer == nil {
			return nil, fmt.Errorf("%w: layer index %d", ErrNilLayer, i)
		}
	}

	next := &Version{layers: make([]*Layer, len(layers))}
	copy(next.layers, layers)

	for {
		old := c.current.Load()
		if old == nil {
			next.seq = 1
		} else {
			next.seq = old.seq + 1
		}
		if c.current.CompareAndSwap(old, next) {
			return next, nil
		}
	}
}

// Current returns the most recently published version, or nil if the chain
// has never been published.
func (c *Chain) Current() *Version { return c.current.Load() }

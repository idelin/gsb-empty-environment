package netsec

import "fmt"

const (
	// AddressSpace is the number of bytes in the modelled address space.
	AddressSpace = 4096
	// LineSize is the cache line size in bytes.
	LineSize = 16
	// NumSets is the number of cache sets.
	NumSets = 16
	// Ways is the associativity of every cache set.
	Ways = 2
)

// Phase identifies the part of an experiment in which an access occurred.
type Phase uint8

const (
	// PhaseUnspecified is used for standalone Cache.Access calls.
	PhaseUnspecified Phase = iota
	// PhasePrime is the attacker's priming phase.
	PhasePrime
	// PhaseVictim is the victim's load phase.
	PhaseVictim
	// PhaseProbe is the attacker's probing phase.
	PhaseProbe
)

func (p Phase) String() string {
	switch p {
	case PhasePrime:
		return "prime"
	case PhaseVictim:
		return "victim"
	case PhaseProbe:
		return "probe"
	default:
		return "unspecified"
	}
}

// ValidateAddress checks that addr is a byte address in the model.
func ValidateAddress(addr int) error {
	if addr < 0 || addr >= AddressSpace {
		return fmt.Errorf("netsec: address %d outside [0,%d)", addr, AddressSpace)
	}
	return nil
}

// SetOf returns the set selected by addr.
func SetOf(addr int) int {
	return (addr / LineSize) % NumSets
}

// TagOf returns the tag selected by addr.
func TagOf(addr int) int {
	return (addr / LineSize) / NumSets
}

// WayState is one cache way in slot order.
type WayState struct {
	Valid    bool
	Tag      int
	LastUsed uint64
}

// SetState is a point-in-time view of one set. Ways are in fixed slot order;
// LastUsed and LRU can be used to recover recency order.
type SetState struct {
	Set  int
	Ways [Ways]WayState
}

// LRU returns the valid tags in least-recently-used to most-recently-used
// order.
func (s SetState) LRU() []int {
	tags := make([]int, 0, Ways)
	for _, w := range s.Ways {
		if !w.Valid {
			continue
		}
		inserted := false
		for i, tag := range tags {
			other := s.tagState(tag)
			if w.LastUsed < other.LastUsed {
				tags = append(tags, 0)
				copy(tags[i+1:], tags[i:])
				tags[i] = w.Tag
				inserted = true
				break
			}
		}
		if !inserted {
			tags = append(tags, w.Tag)
		}
	}
	return tags
}

func (s SetState) tagState(tag int) WayState {
	for _, w := range s.Ways {
		if w.Valid && w.Tag == tag {
			return w
		}
	}
	return WayState{}
}

// MRU returns the most-recently-used valid tag, or -1 for an empty set.
func (s SetState) MRU() int {
	lru := s.LRU()
	if len(lru) == 0 {
		return -1
	}
	return lru[len(lru)-1]
}

// AccessResult records one cache access and the set state immediately before
// and after it.
type AccessResult struct {
	Phase       Phase
	Index       int
	Address     int
	Line        int
	Set         int
	Tag         int
	Hit         bool
	Evicted     bool
	EvictedTag  int
	Before      SetState
	After       SetState
	AccessClock uint64
}

// Cache is a 16-set, 2-way set-associative cache with true LRU replacement.
// The zero value is an empty cache and is ready for use.
type Cache struct {
	sets  [NumSets]set
	clock uint64
}

type set struct {
	ways [Ways]line
}

type line struct {
	valid    bool
	tag      int
	lastUsed uint64
}

// NewCache returns an empty cache.
func NewCache() *Cache {
	return &Cache{}
}

// Reset empties the cache and resets its access clock.
func (c *Cache) Reset() {
	*c = Cache{}
}

// Snapshot returns the complete cache state.
func (c *Cache) Snapshot() [NumSets]SetState {
	var out [NumSets]SetState
	for i := range c.sets {
		out[i] = c.snapshotSet(i)
	}
	return out
}

// SetState returns the current state of one set.
func (c *Cache) SetState(setIndex int) (SetState, error) {
	if setIndex < 0 || setIndex >= NumSets {
		return SetState{}, fmt.Errorf("netsec: set %d outside [0,%d)", setIndex, NumSets)
	}
	return c.snapshotSet(setIndex), nil
}

// Access performs one access. Any address in the address space is valid;
// addresses in the same line are treated as the same cache line.
func (c *Cache) Access(addr int) (AccessResult, error) {
	return c.access(addr, PhaseUnspecified, 0)
}

func (c *Cache) access(addr int, phase Phase, index int) (AccessResult, error) {
	if err := ValidateAddress(addr); err != nil {
		return AccessResult{}, err
	}

	c.clock++
	lineIndex := addr / LineSize
	setIndex := lineIndex % NumSets
	tag := lineIndex / NumSets
	s := &c.sets[setIndex]

	before := c.snapshotSet(setIndex)
	result := AccessResult{
		Phase:       phase,
		Index:       index,
		Address:     addr,
		Line:        lineIndex,
		Set:         setIndex,
		Tag:         tag,
		EvictedTag:  -1,
		Before:      before,
		AccessClock: c.clock,
	}

	for i := range s.ways {
		if s.ways[i].valid && s.ways[i].tag == tag {
			s.ways[i].lastUsed = c.clock
			result.Hit = true
			result.After = c.snapshotSet(setIndex)
			return result, nil
		}
	}

	slot := -1
	for i := range s.ways {
		if !s.ways[i].valid {
			slot = i
			break
		}
	}
	if slot < 0 {
		slot = 0
		for i := 1; i < Ways; i++ {
			if s.ways[i].lastUsed < s.ways[slot].lastUsed {
				slot = i
			}
		}
		result.Evicted = true
		result.EvictedTag = s.ways[slot].tag
	}

	s.ways[slot] = line{valid: true, tag: tag, lastUsed: c.clock}
	result.After = c.snapshotSet(setIndex)
	return result, nil
}

func (c *Cache) snapshotSet(setIndex int) SetState {
	out := SetState{Set: setIndex}
	for i, w := range c.sets[setIndex].ways {
		out.Ways[i] = WayState{Valid: w.valid, Tag: w.tag, LastUsed: w.lastUsed}
	}
	return out
}

package gdict

import (
	"errors"
	"fmt"
	"sort"
)

// Bound is the comparison operator of an old range-index cut point.
type Bound uint8

const (
	// LT means the old scan includes rows with old code < OldCode.
	LT Bound = iota
	// LE means the old scan includes rows with old code <= OldCode.
	LE
)

func (b Bound) String() string {
	if b == LT {
		return "LT"
	}
	if b == LE {
		return "LE"
	}
	return fmt.Sprintf("Bound(%d)", uint8(b))
}

// Entry is one entry of a partition-local dictionary.
type Entry struct {
	OldCode uint64 `json:"old_code"`
	Key     Key    `json:"key"`
}

// Cut is a still-live old range-index cut point. The associated index scans
// either {row : old code < OldCode} (LT) or {row : old code <= OldCode} (LE).
type Cut struct {
	Name    string `json:"name,omitempty"`
	OldCode uint64 `json:"old_code"`
	Bound   Bound  `json:"bound"`
}

// Partition is one local dictionary together with the collation version used
// by its still-live range indexes.
type Partition struct {
	Name    string  `json:"name"`
	Version string  `json:"version"`
	Entries []Entry `json:"entries"`
	Cuts    []Cut   `json:"cuts,omitempty"`
}

// Alias declares keys that denote one logical dictionary value. Members are
// required to be equivalent in every supplied collation; a strict ordering
// between two members is an input conflict rather than a usable distinction.
type Alias struct {
	Name    string `json:"name,omitempty"`
	Members []Key  `json:"members"`
}

// Preorder is the total preorder of one collation version. Keys with equal
// ranks are equivalent for that version. Strictly smaller ranks sort first.
// Every key mentioned anywhere in Config must have a rank in every Preorder.
type Preorder struct {
	Version string        `json:"version"`
	Ranks   map[Key]int64 `json:"ranks"`
}

// NewPreorder builds a preorder from ordered equivalence classes. Classes are
// listed from smallest to largest; keys inside one class are equivalent.
// Each key may occur only once.
func NewPreorder(version string, classes [][]Key) (Preorder, error) {
	if version == "" {
		return Preorder{}, fmt.Errorf("gdict: preorder needs a version name")
	}
	ranks := make(map[Key]int64)
	for i, class := range classes {
		for _, k := range class {
			if _, ok := ranks[k]; ok {
				return Preorder{}, fmt.Errorf("gdict: key %s occurs in more than one preorder class", k)
			}
			ranks[k] = int64(i)
		}
	}
	return Preorder{Version: version, Ranks: ranks}, nil
}

// Interval is an inclusive interval of physical global codes.
type Interval struct {
	Lo uint64 `json:"lo"`
	Hi uint64 `json:"hi"`
}

// Fixed is an existing foreign-key code. It is occupied and cannot move.
type Fixed struct {
	Code uint64 `json:"code"`
	Key  Key    `json:"key"`
}

// CodeSpace describes the global physical code space [0, Limit), reserved
// holes, and fixed foreign-key codes.
type CodeSpace struct {
	Limit uint64     `json:"limit"`
	Holes []Interval `json:"holes,omitempty"`
	Fixed []Fixed    `json:"fixed,omitempty"`
}

// Lookup is a still-live exact key lookup. Its old semantics are the SQL
// collation semantics: all rows whose key is equivalent to Key under Version
// (aliases included) are returned.
type Lookup struct {
	Name      string `json:"name,omitempty"`
	Partition string `json:"partition"`
	Version   string `json:"version,omitempty"`
	Key       Key    `json:"key"`
}

// Config is the complete upgrade input.
type Config struct {
	Partitions []Partition `json:"partitions"`
	Aliases    []Alias     `json:"aliases,omitempty"`
	Preorders  []Preorder  `json:"preorders"`
	Space      CodeSpace   `json:"space"`
	Lookups    []Lookup    `json:"lookups,omitempty"`

	// MaxStates bounds the exact search. Zero selects DefaultMaxStates.
	MaxStates int `json:"max_states,omitempty"`
}

// MaxGlobalCode is the largest exclusive upper bound accepted by this
// package. The physical code domain is sparse; no code in it is ever
// enumerated.
const MaxGlobalCode uint64 = 1 << 50

// DefaultMaxStates is the default exact-search budget.
const DefaultMaxStates = 2_000_000

var (
	// ErrSearchLimit reports that the exact search budget was exhausted.
	// Solve never returns a suboptimal plan disguised as an optimal one.
	ErrSearchLimit = errors.New("gdict: exact search budget exhausted")
)

func (c CodeSpace) validate() ([]Interval, []Fixed, error) {
	if c.Limit == 0 {
		return nil, nil, errors.New("gdict: global code space limit must be positive")
	}
	if c.Limit > MaxGlobalCode {
		return nil, nil, fmt.Errorf("gdict: global code space limit %d exceeds 2^50", c.Limit)
	}
	holes := append([]Interval(nil), c.Holes...)
	sort.Slice(holes, func(i, j int) bool {
		if holes[i].Lo != holes[j].Lo {
			return holes[i].Lo < holes[j].Lo
		}
		return holes[i].Hi < holes[j].Hi
	})
	merged := holes[:0]
	for _, h := range holes {
		if h.Lo > h.Hi {
			return nil, nil, fmt.Errorf("gdict: inverted hole [%d,%d]", h.Lo, h.Hi)
		}
		if h.Hi >= c.Limit {
			return nil, nil, fmt.Errorf("gdict: hole [%d,%d] outside [0,%d)", h.Lo, h.Hi, c.Limit)
		}
		if len(merged) > 0 && h.Lo <= merged[len(merged)-1].Hi+1 {
			if h.Hi > merged[len(merged)-1].Hi {
				merged[len(merged)-1].Hi = h.Hi
			}
			continue
		}
		merged = append(merged, h)
	}
	fixed := append([]Fixed(nil), c.Fixed...)
	sort.Slice(fixed, func(i, j int) bool {
		if fixed[i].Code != fixed[j].Code {
			return fixed[i].Code < fixed[j].Code
		}
		return fixed[i].Key.Less(fixed[j].Key)
	})
	for _, f := range fixed {
		if f.Code >= c.Limit {
			return nil, nil, &IncompatibleError{Cert: Certificate{
				Kind:    "fixed-code-out-of-range",
				Message: fmt.Sprintf("fixed code %d is outside the global code space [0,%d)", f.Code, c.Limit),
				Fixed:   []Fixed{f},
				Observations: []Observation{
					{Kind: "code-space", Detail: fmt.Sprintf("global code space is [0,%d)", c.Limit)},
					{Kind: "fixed-code", LeftKey: f.Key, Detail: fmt.Sprintf("foreign key %s is pinned at %d", f.Key, f.Code)},
				},
				Requirement: "existing foreign-key codes cannot move",
			}}
		}
		if h, ok := containingHole(merged, f.Code); ok {
			return nil, nil, &IncompatibleError{Cert: Certificate{
				Kind:    "fixed-code-in-hole",
				Message: fmt.Sprintf("fixed code %d for %s lies inside reserved hole [%d,%d]", f.Code, f.Key, h.Lo, h.Hi),
				Fixed:   []Fixed{f},
				Observations: []Observation{
					{
						Kind:         "reserved-hole",
						OldCode:      h.Lo,
						RightOldCode: h.Hi,
						LeftKey:      f.Key,
						RightKey:     f.Key,
						Detail:       fmt.Sprintf("reserved hole [%d,%d]", h.Lo, h.Hi),
					},
					{Kind: "fixed-code", LeftKey: f.Key, Detail: fmt.Sprintf("foreign key %s is pinned at %d", f.Key, f.Code)},
				},
				Requirement: "an occupied fixed code may not fall in a reserved hole",
			}}
		}
	}
	return merged, fixed, nil
}

func inIntervals(code uint64, intervals []Interval) bool {
	i := sort.Search(len(intervals), func(i int) bool { return intervals[i].Hi >= code })
	return i < len(intervals) && intervals[i].Lo <= code && code <= intervals[i].Hi
}

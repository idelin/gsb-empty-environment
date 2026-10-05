package main

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
)

// Item is a weighted item to be placed.
type Item struct {
	ID     string
	Weight *big.Rat
}

// Input is the parsed, validated problem instance.
// All quantities are exact rationals.
type Input struct {
	Capacity  *big.Rat   // max total weight per bin
	Positions []*big.Rat // discrete slot coordinates inside every bin, sorted, deduped
	Lo, Hi    *big.Rat   // safe interval for the center of gravity of each bin
	Center    *big.Rat   // (Lo+Hi)/2, reference point for eccentricity
	Items     []Item
	MaxNodes  int // search node budget (0 => default)
}

// ratJSON decodes a rational given either as a JSON string ("3/2", "1.5", "7")
// or as a JSON number. Decoding via big.Rat keeps the value exact.
type ratJSON struct {
	r *big.Rat
}

func (rj *ratJSON) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		r, ok := new(big.Rat).SetString(s)
		if !ok {
			return fmt.Errorf("invalid rational %q", s)
		}
		rj.r = r
		return nil
	}
	var num json.Number
	if err := json.Unmarshal(data, &num); err != nil {
		return fmt.Errorf("expected rational as string or number, got %s", string(data))
	}
	r, ok := new(big.Rat).SetString(num.String())
	if !ok {
		return fmt.Errorf("invalid rational %q", num.String())
	}
	rj.r = r
	return nil
}

type rawItem struct {
	ID     string  `json:"id"`
	Weight ratJSON `json:"weight"`
}

type rawInput struct {
	Capacity     ratJSON   `json:"capacity"`
	Positions    []ratJSON `json:"positions"`
	SafeInterval []ratJSON `json:"safe_interval"`
	Items        []rawItem `json:"items"`
	MaxNodes     int       `json:"max_nodes"`
}

const (
	maxItems     = 18 // per problem statement
	maxPositions = 64 // bitmask width used by the search
)

// ParseInput parses and validates a problem instance.
func ParseInput(data []byte) (*Input, error) {
	var ri rawInput
	if err := json.Unmarshal(data, &ri); err != nil {
		return nil, fmt.Errorf("invalid JSON: %v", err)
	}
	if ri.Capacity.r == nil || ri.Capacity.r.Sign() <= 0 {
		return nil, fmt.Errorf("capacity must be a positive rational")
	}
	if len(ri.SafeInterval) != 2 || ri.SafeInterval[0].r == nil || ri.SafeInterval[1].r == nil {
		return nil, fmt.Errorf("safe_interval must be [lo, hi]")
	}
	lo, hi := ri.SafeInterval[0].r, ri.SafeInterval[1].r
	if lo.Cmp(hi) > 0 {
		return nil, fmt.Errorf("safe_interval: lo > hi")
	}
	if len(ri.Items) > maxItems {
		return nil, fmt.Errorf("at most %d items allowed, got %d", maxItems, len(ri.Items))
	}
	if len(ri.Positions) > maxPositions {
		return nil, fmt.Errorf("at most %d positions allowed, got %d", maxPositions, len(ri.Positions))
	}
	if len(ri.Items) > 0 && len(ri.Positions) == 0 {
		return nil, fmt.Errorf("positions must be non-empty when there are items")
	}

	seenIDs := map[string]bool{}
	items := make([]Item, 0, len(ri.Items))
	for _, it := range ri.Items {
		if it.ID == "" {
			return nil, fmt.Errorf("item id must be non-empty")
		}
		if seenIDs[it.ID] {
			return nil, fmt.Errorf("duplicate item id %q", it.ID)
		}
		seenIDs[it.ID] = true
		if it.Weight.r == nil || it.Weight.r.Sign() <= 0 {
			return nil, fmt.Errorf("item %q: weight must be a positive rational", it.ID)
		}
		items = append(items, Item{ID: it.ID, Weight: it.Weight.r})
	}

	posSet := map[string]bool{}
	positions := make([]*big.Rat, 0, len(ri.Positions))
	for _, p := range ri.Positions {
		if p.r == nil {
			return nil, fmt.Errorf("positions contain an invalid value")
		}
		key := p.r.RatString()
		if posSet[key] {
			continue // duplicate coordinate: slots are interchangeable
		}
		posSet[key] = true
		positions = append(positions, p.r)
	}
	sort.Slice(positions, func(i, j int) bool { return positions[i].Cmp(positions[j]) < 0 })

	center := new(big.Rat).Add(lo, hi)
	center.Quo(center, big.NewRat(2, 1))

	return &Input{
		Capacity:  ri.Capacity.r,
		Positions: positions,
		Lo:        lo,
		Hi:        hi,
		Center:    center,
		Items:     items,
		MaxNodes:  ri.MaxNodes,
	}, nil
}

// ---------- solution document (solver output / verifier input) ----------

// Placement is one piece of placement evidence: item -> slot coordinate.
type Placement struct {
	Item     string `json:"item"`
	Position string `json:"position"`
}

// BinReport describes one used bin with exact rational evidence.
type BinReport struct {
	Index               int         `json:"index"`
	Placements          []Placement `json:"placements"`
	TotalWeight         string      `json:"total_weight"`
	Cog                 string      `json:"cog"` // exact fraction p/q
	CogDecimal          string      `json:"cog_decimal"`
	Eccentricity        string      `json:"eccentricity"` // |cog - center|, exact
	EccentricityDecimal string      `json:"eccentricity_decimal"`
	WithinSafeInterval  bool        `json:"within_safe_interval"`
}

// Stats reports search effort.
type Stats struct {
	Nodes int `json:"nodes"`
}

// Solution is the full solver output, self-contained for verification.
type Solution struct {
	Status                 string          `json:"status"` // "optimal" | "infeasible"
	BinsUsed               int             `json:"bins_used"`
	MaxEccentricity        string          `json:"max_eccentricity"`
	MaxEccentricityDecimal string          `json:"max_eccentricity_decimal"`
	Bins                   []BinReport     `json:"bins"`
	Stats                  Stats           `json:"stats"`
	Input                  json.RawMessage `json:"input"`
}

func dec(r *big.Rat) string { return r.FloatString(6) }

package main

import (
	"encoding/json"
	"fmt"
	"math/big"
)

// Item is a weighted item to place.
type Item struct {
	ID     string
	Weight *big.Rat
}

// Instance describes one bin-packing-with-balance problem.
//
// All quantities are exact rationals. Positions are the discrete slot
// coordinates available inside every bin (each bin offers the same slots,
// each slot holds at most one item). The center of gravity of every bin
// must lie inside [SafeLo, SafeHi]; eccentricity is the distance of the
// center of gravity from Target (default: midpoint of the safe interval).
type Instance struct {
	Items     []Item
	Positions []*big.Rat
	Capacity  *big.Rat
	SafeLo    *big.Rat
	SafeHi    *big.Rat
	Target    *big.Rat
}

const maxItems = 18

func (in *Instance) validate() error {
	if len(in.Items) == 0 {
		return fmt.Errorf("no items")
	}
	if len(in.Items) > maxItems {
		return fmt.Errorf("too many items: %d > %d", len(in.Items), maxItems)
	}
	if len(in.Positions) == 0 {
		return fmt.Errorf("no positions")
	}
	if len(in.Positions) > 63 {
		return fmt.Errorf("too many positions: %d > 63", len(in.Positions))
	}
	seen := map[string]bool{}
	for i, it := range in.Items {
		if it.ID == "" {
			return fmt.Errorf("item %d: empty id", i)
		}
		if seen[it.ID] {
			return fmt.Errorf("duplicate item id %q", it.ID)
		}
		seen[it.ID] = true
		if it.Weight == nil || it.Weight.Sign() <= 0 {
			return fmt.Errorf("item %q: weight must be positive", it.ID)
		}
		if it.Weight.Cmp(in.Capacity) > 0 {
			return fmt.Errorf("item %q: weight %s exceeds capacity %s",
				it.ID, ratStr(it.Weight), ratStr(in.Capacity))
		}
	}
	if in.Capacity == nil || in.Capacity.Sign() <= 0 {
		return fmt.Errorf("capacity must be positive")
	}
	if in.SafeLo == nil || in.SafeHi == nil || in.SafeLo.Cmp(in.SafeHi) > 0 {
		return fmt.Errorf("invalid safe interval")
	}
	if in.Target == nil {
		t := new(big.Rat).Add(in.SafeLo, in.SafeHi)
		t.Quo(t, big.NewRat(2, 1))
		in.Target = t
	}
	return nil
}

// jsonRat decodes a rational from a JSON number or string ("3/2", "1.5", "7").
type jsonRat struct {
	v *big.Rat
}

func (j *jsonRat) UnmarshalJSON(b []byte) error {
	var s string
	if len(b) > 0 && b[0] == '"' {
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
	} else {
		s = string(b)
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return fmt.Errorf("invalid rational %q", s)
	}
	j.v = r
	return nil
}

type rawItem struct {
	ID     string  `json:"id"`
	Weight jsonRat `json:"weight"`
}

type rawInstance struct {
	Items        []rawItem `json:"items"`
	Positions    []jsonRat `json:"positions"`
	Capacity     jsonRat   `json:"capacity"`
	SafeInterval []jsonRat `json:"safe_interval"`
	Target       *jsonRat  `json:"target,omitempty"`
}

func parseInstance(data []byte) (*Instance, error) {
	var ri rawInstance
	if err := json.Unmarshal(data, &ri); err != nil {
		return nil, fmt.Errorf("invalid JSON: %v", err)
	}
	if len(ri.SafeInterval) != 2 {
		return nil, fmt.Errorf("safe_interval must have exactly 2 elements")
	}
	in := &Instance{
		Capacity: ri.Capacity.v,
		SafeLo:   ri.SafeInterval[0].v,
		SafeHi:   ri.SafeInterval[1].v,
	}
	for _, p := range ri.Positions {
		in.Positions = append(in.Positions, p.v)
	}
	for _, it := range ri.Items {
		in.Items = append(in.Items, Item{ID: it.ID, Weight: it.Weight.v})
	}
	if ri.Target != nil {
		in.Target = ri.Target.v
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	return in, nil
}

func ratStr(r *big.Rat) string {
	if r == nil {
		return "null"
	}
	return r.RatString()
}

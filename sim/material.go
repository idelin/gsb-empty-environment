// Package sim implements a material-flow simulator in which every material
// entity carries a vector of source shares. Quantities are integers; source
// shares are exact rationals so that splits, merges and losses never lose
// track of provenance.
package sim

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// SourceVector maps a source ID to the exact (rational) amount of material
// contributed by that source.
type SourceVector map[string]*big.Rat

func (v SourceVector) clone() SourceVector {
	out := make(SourceVector, len(v))
	for k, a := range v {
		out[k] = new(big.Rat).Set(a)
	}
	return out
}

func (v SourceVector) total() *big.Rat {
	sum := new(big.Rat)
	for _, a := range v {
		sum.Add(sum, a)
	}
	return sum
}

// scale returns a new vector with every share multiplied by f.
func (v SourceVector) scale(f *big.Rat) SourceVector {
	out := make(SourceVector, len(v))
	for k, a := range v {
		out[k] = new(big.Rat).Mul(a, f)
	}
	return out
}

// add merges other into v in place.
func (v SourceVector) add(other SourceVector) {
	for k, a := range other {
		if cur, ok := v[k]; ok {
			cur.Add(cur, a)
		} else {
			v[k] = new(big.Rat).Set(a)
		}
	}
}

// String renders the vector as a linear combination, e.g. "5*oreA + 3/2*oreB".
func (v SourceVector) String() string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	terms := make([]string, 0, len(keys))
	for _, k := range keys {
		terms = append(terms, v[k].RatString()+"*"+k)
	}
	return strings.Join(terms, " + ")
}

// Entity is a quantity of material together with its provenance.
// Invariant: sum(Sources) == Quantity (as an exact rational).
type Entity struct {
	ID       string
	Quantity int64
	Sources  SourceVector
}

func (e *Entity) checkInvariant() error {
	total := e.Sources.total()
	if !total.IsInt() || total.Num().Int64() != e.Quantity {
		return fmt.Errorf("entity %s: source shares sum to %s, want %d", e.ID, total.RatString(), e.Quantity)
	}
	return nil
}

func (e *Entity) clone() *Entity {
	return &Entity{ID: e.ID, Quantity: e.Quantity, Sources: e.Sources.clone()}
}

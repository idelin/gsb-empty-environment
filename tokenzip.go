// Package tokenzip implements a dictionary-based token compression scheme.
//
// A dictionary is a list of entries; each entry is a phrase made of literal
// tokens and references to other entries. Decoding expands references
// recursively. The package builds a length index (using big integers) so the
// expanded length of every entry is known without materializing the
// expansion, and answers interval queries that map a range of expanded
// positions back to the original literal tokens.
package tokenzip

import (
	"fmt"
	"math/big"
)

// Item is one element of a dictionary entry: either a literal token or a
// reference to another entry.
type Item struct {
	Token string // literal token, valid when Ref < 0
	Ref   int    // referenced entry index, or -1 for a literal
}

// Lit returns a literal-token item.
func Lit(token string) Item { return Item{Token: token, Ref: -1} }

// Ref returns an item referencing dictionary entry i.
func Ref(i int) Item { return Item{Ref: i} }

// EmptyPhraseError reports a dictionary entry with no items.
type EmptyPhraseError struct{ Entry int }

func (e *EmptyPhraseError) Error() string {
	return fmt.Sprintf("tokenzip: entry %d is an empty phrase", e.Entry)
}

// BadReferenceError reports a reference to a non-existent entry.
type BadReferenceError struct{ Entry, Ref int }

func (e *BadReferenceError) Error() string {
	return fmt.Sprintf("tokenzip: entry %d references invalid entry %d", e.Entry, e.Ref)
}

// CycleError reports a reference cycle among entries.
type CycleError struct{ Entry int }

func (e *CycleError) Error() string {
	return fmt.Sprintf("tokenzip: reference cycle detected at entry %d", e.Entry)
}

// BudgetExceededError reports that an expansion length exceeds the budget.
type BudgetExceededError struct {
	Entry    int
	Limit    *big.Int
	Required *big.Int // lower bound on the length that tripped the budget
}

func (e *BudgetExceededError) Error() string {
	return fmt.Sprintf("tokenzip: entry %d expansion exceeds budget %s (at least %s)",
		e.Entry, e.Limit, e.Required)
}

// Dict is a validated dictionary with a precomputed length index.
type Dict struct {
	entries [][]Item
	lengths []*big.Int
}

const (
	unvisited = 0
	visiting  = 1
	done      = 2
)

// Build validates entries and computes the expanded length of every entry
// without expanding it. A nil budget means unlimited expansion; otherwise any
// entry whose expanded length exceeds budget fails with BudgetExceededError.
func Build(entries [][]Item, budget *big.Int) (*Dict, error) {
	d := &Dict{
		entries: entries,
		lengths: make([]*big.Int, len(entries)),
	}
	state := make([]int, len(entries))
	for i := range entries {
		if len(entries[i]) == 0 {
			return nil, &EmptyPhraseError{Entry: i}
		}
	}
	for i := range entries {
		if _, err := d.lengthOf(i, state, budget); err != nil {
			return nil, err
		}
	}
	return d, nil
}

// lengthOf computes the expanded length of entry i via memoized DFS.
func (d *Dict) lengthOf(i int, state []int, budget *big.Int) (*big.Int, error) {
	switch state[i] {
	case done:
		return d.lengths[i], nil
	case visiting:
		return nil, &CycleError{Entry: i}
	}
	state[i] = visiting
	total := new(big.Int)
	for _, it := range d.entries[i] {
		if it.Ref >= 0 {
			if it.Ref >= len(d.entries) {
				return nil, &BadReferenceError{Entry: i, Ref: it.Ref}
			}
			sub, err := d.lengthOf(it.Ref, state, budget)
			if err != nil {
				return nil, err
			}
			total.Add(total, sub)
		} else {
			total.Add(total, big.NewInt(1))
		}
		if budget != nil && total.Cmp(budget) > 0 {
			return nil, &BudgetExceededError{
				Entry:    i,
				Limit:    new(big.Int).Set(budget),
				Required: new(big.Int).Set(total),
			}
		}
	}
	state[i] = done
	d.lengths[i] = total
	return total, nil
}

// Len returns the expanded length of entry i.
func (d *Dict) Len(i int) *big.Int { return new(big.Int).Set(d.lengths[i]) }

// Query returns the original literal tokens occupying expanded positions
// [start, end) of the given entry, expanding only the parts of the dictionary
// that intersect the interval.
func (d *Dict) Query(entry int, start, end *big.Int) ([]string, error) {
	if entry < 0 || entry >= len(d.entries) {
		return nil, &BadReferenceError{Entry: -1, Ref: entry}
	}
	if start.Sign() < 0 || end.Cmp(start) < 0 || end.Cmp(d.lengths[entry]) > 0 {
		return nil, fmt.Errorf("tokenzip: interval [%s, %s) out of range for entry %d of length %s",
			start, end, entry, d.lengths[entry])
	}
	var out []string
	d.query(entry, start, end, &out)
	return out, nil
}

func (d *Dict) query(entry int, start, end *big.Int, out *[]string) {
	offset := new(big.Int)
	for _, it := range d.entries[entry] {
		if offset.Cmp(end) >= 0 {
			return
		}
		if it.Ref < 0 {
			if offset.Cmp(start) >= 0 {
				*out = append(*out, it.Token)
			}
			offset.Add(offset, big.NewInt(1))
			continue
		}
		subLen := d.lengths[it.Ref]
		subEnd := new(big.Int).Add(offset, subLen)
		if subEnd.Cmp(start) > 0 && offset.Cmp(end) < 0 {
			lo := new(big.Int).Sub(start, offset)
			if lo.Sign() < 0 {
				lo.SetInt64(0)
			}
			hi := new(big.Int).Sub(end, offset)
			if hi.Cmp(subLen) > 0 {
				hi.Set(subLen)
			}
			d.query(it.Ref, lo, hi, out)
		}
		offset.Add(offset, subLen)
	}
}

// Expand fully decodes an entry into its literal tokens. It is provided for
// verification and small inputs; prefer Query for large expansions.
func (d *Dict) Expand(entry int) ([]string, error) {
	return d.Query(entry, big.NewInt(0), d.lengths[entry])
}

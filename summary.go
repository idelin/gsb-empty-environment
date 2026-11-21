package indexvis

import (
	"fmt"
	"sort"
)

// Pair is a (creator, deleter) pair. Delete may be NoTransaction when the
// version has no deleter.
type Pair struct {
	Create TxID
	Delete TxID
}

func (p Pair) String() string {
	if p.Delete == NoTransaction {
		return fmt.Sprintf("(%d, none)", p.Create)
	}
	return fmt.Sprintf("(%d, %d)", p.Create, p.Delete)
}

// PairCount is one compressed page-summary entry.
type PairCount struct {
	Pair  Pair
	Count int64
}

// PageSummary compresses versions by transaction pair. Versions with the same
// creator/deleter pair are represented by one multiplicity. This is exact for
// snapshot visibility because visibility only depends on that pair.
//
// A summary can be split and merged. Merging summaries before evaluation is
// important when pages share unknown transactions: the unknown outcomes must
// be correlated across all pages.
type PageSummary struct {
	counts map[Pair]int64
}

// NewPageSummary returns an empty page summary.
func NewPageSummary() *PageSummary {
	return &PageSummary{counts: make(map[Pair]int64)}
}

func (p *PageSummary) ensure() {
	if p.counts == nil {
		p.counts = make(map[Pair]int64)
	}
}

func validatePair(p Pair) error {
	if p.Create == NoTransaction {
		return fmt.Errorf("version pair: creator is NoTransaction")
	}
	return nil
}

// Add adds n copies of a transaction pair.
func (p *PageSummary) Add(create, deleter TxID, n int64) error {
	return p.add(Pair{Create: create, Delete: deleter}, n)
}

func (p *PageSummary) add(pair Pair, delta int64) error {
	if err := validatePair(pair); err != nil {
		return err
	}
	if delta == 0 {
		return nil
	}
	if delta < 0 {
		return fmt.Errorf("page summary: negative add %d for pair %s", delta, pair)
	}
	p.ensure()
	p.counts[pair] += delta
	return nil
}

// Set sets the multiplicity of a pair. A zero multiplicity removes the pair.
func (p *PageSummary) Set(create, deleter TxID, n int64) error {
	pair := Pair{Create: create, Delete: deleter}
	if err := validatePair(pair); err != nil {
		return err
	}
	if n < 0 {
		return fmt.Errorf("page summary: negative multiplicity %d for pair %s", n, pair)
	}
	p.ensure()
	if n == 0 {
		delete(p.counts, pair)
		return nil
	}
	p.counts[pair] = n
	return nil
}

// Count returns the multiplicity of a pair.
func (p *PageSummary) Count(create, deleter TxID) int64 {
	if p == nil || p.counts == nil {
		return 0
	}
	return p.counts[Pair{Create: create, Delete: deleter}]
}

// Merge adds all entries of other into p.
func (p *PageSummary) Merge(other *PageSummary) error {
	if other == nil {
		return nil
	}
	for pair, n := range other.counts {
		if err := p.add(pair, n); err != nil {
			return err
		}
	}
	return nil
}

// Clone returns an independent copy.
func (p *PageSummary) Clone() *PageSummary {
	out := NewPageSummary()
	if p != nil {
		for pair, n := range p.counts {
			out.counts[pair] = n
		}
	}
	return out
}

// Split partitions a summary. keep reports whether an entry belongs to the
// first result; all other entries go to the second result.
func (p *PageSummary) Split(keep func(Pair) bool) (*PageSummary, *PageSummary) {
	left, right := NewPageSummary(), NewPageSummary()
	if p == nil {
		return left, right
	}
	for pair, n := range p.counts {
		if keep != nil && keep(pair) {
			left.counts[pair] = n
		} else {
			right.counts[pair] = n
		}
	}
	return left, right
}

// Pairs returns the compressed entries in deterministic order.
func (p *PageSummary) Pairs() []PairCount {
	if p == nil {
		return nil
	}
	out := make([]PairCount, 0, len(p.counts))
	for pair, n := range p.counts {
		if n != 0 {
			out = append(out, PairCount{Pair: pair, Count: n})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pair.Create != out[j].Pair.Create {
			return out[i].Pair.Create < out[j].Pair.Create
		}
		return out[i].Pair.Delete < out[j].Pair.Delete
	})
	return out
}

// TotalVersions returns the sum of all multiplicities.
func (p *PageSummary) TotalVersions() int64 {
	if p == nil {
		return 0
	}
	var total int64
	for _, n := range p.counts {
		total += n
	}
	return total
}

// MergePageSummaries merges any number of page summaries. A nil summary is
// treated as empty.
func MergePageSummaries(pages ...*PageSummary) (*PageSummary, error) {
	out := NewPageSummary()
	for _, page := range pages {
		if err := out.Merge(page); err != nil {
			return nil, err
		}
	}
	return out, nil
}

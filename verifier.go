package netsec

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"
)

// Verifier tracks per-group and overall counts over an ordered finite domain
// and performs exact earth-mover-distance audits. All probabilities and
// distances are rational numbers (math/big), so no floating-point rounding can
// enter the threshold decision.
type Verifier struct {
	mu     sync.RWMutex
	domain *Domain
	groups map[string]*groupState
	grand  []int64
	total  int64
}

type groupState struct {
	counts []int64
	total  int64
}

// NewVerifier creates an empty verifier for the given domain. A nil domain is
// a programming error and panics rather than silently dropping data.
func NewVerifier(domain *Domain) *Verifier {
	if domain == nil {
		panic("netsec: NewVerifier requires a non-nil domain")
	}
	return &Verifier{
		domain: domain,
		groups: make(map[string]*groupState),
		grand:  make([]int64, domain.Size()),
	}
}

// Domain returns the verifier's ordered domain.
func (v *Verifier) Domain() *Domain {
	return v.domain
}

func (v *Verifier) checkPos(pos int) error {
	if pos < 0 || pos >= v.domain.Size() {
		return fmt.Errorf("netsec: domain position %d out of range [0,%d)", pos, v.domain.Size())
	}
	return nil
}

// AddRecord inserts one record into group at the domain position of label and
// updates the population totals in the same operation.
func (v *Verifier) AddRecord(group, label string) error {
	pos, ok := v.domain.Index(label)
	if !ok {
		return fmt.Errorf("netsec: unknown sensitive value %q", label)
	}
	return v.addAt(group, pos)
}

// AddRecordAt is the position-based variant of AddRecord.
func (v *Verifier) AddRecordAt(group string, pos int) error {
	if err := v.checkPos(pos); err != nil {
		return err
	}
	return v.addAt(group, pos)
}

func (v *Verifier) addAt(group string, pos int) error {
	if group == "" {
		return errors.New("netsec: group name must not be empty")
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.grand[pos] == mathMaxInt64 || v.total == mathMaxInt64 {
		return errors.New("netsec: total count overflow")
	}
	g, ok := v.groups[group]
	if !ok {
		g = &groupState{counts: make([]int64, v.domain.Size())}
		v.groups[group] = g
	}
	if g.counts[pos] == mathMaxInt64 || g.total == mathMaxInt64 {
		return errors.New("netsec: group count overflow")
	}
	g.counts[pos]++
	g.total++
	v.grand[pos]++
	v.total++
	return nil
}

// WithdrawRecord removes one previously added record from group and from the
// population totals atomically.
func (v *Verifier) WithdrawRecord(group, label string) error {
	pos, ok := v.domain.Index(label)
	if !ok {
		return fmt.Errorf("netsec: unknown sensitive value %q", label)
	}
	return v.withdrawAt(group, pos)
}

// WithdrawRecordAt is the position-based variant of WithdrawRecord.
func (v *Verifier) WithdrawRecordAt(group string, pos int) error {
	if err := v.checkPos(pos); err != nil {
		return err
	}
	return v.withdrawAt(group, pos)
}

func (v *Verifier) withdrawAt(group string, pos int) error {
	if group == "" {
		return errors.New("netsec: group name must not be empty")
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	g, ok := v.groups[group]
	if !ok || g.counts[pos] == 0 {
		return fmt.Errorf("netsec: no record to withdraw in group %q at position %d", group, pos)
	}
	g.counts[pos]--
	g.total--
	v.grand[pos]--
	v.total--
	return nil
}

const mathMaxInt64 = int64(^uint64(0) >> 1)

type verifierSnapshot struct {
	names  []string
	groups map[string]*groupState
	grand  []int64
	total  int64
}

// snapshot copies the mutable state so distance arithmetic never holds the lock.
func (v *Verifier) snapshot() *verifierSnapshot {
	v.mu.RLock()
	defer v.mu.RUnlock()

	names := make([]string, 0, len(v.groups))
	groups := make(map[string]*groupState, len(v.groups))
	for name, g := range v.groups {
		names = append(names, name)
		groups[name] = &groupState{
			counts: append([]int64(nil), g.counts...),
			total:  g.total,
		}
	}
	sort.Strings(names)
	return &verifierSnapshot{
		names:  names,
		groups: groups,
		grand:  append([]int64(nil), v.grand...),
		total:  v.total,
	}
}

func countsToRatDist(counts []int64, total int64) []*big.Rat {
	dist := make([]*big.Rat, len(counts))
	if total == 0 {
		return dist
	}
	denom := big.NewInt(total)
	for i, c := range counts {
		dist[i] = new(big.Rat).SetFrac(big.NewInt(c), new(big.Int).Set(denom))
	}
	return dist
}

// PopulationDistribution returns the exact rational population distribution, or
// nil entries when no record has been added yet.
func (v *Verifier) PopulationDistribution() []*big.Rat {
	snap := v.snapshot()
	return countsToRatDist(snap.grand, snap.total)
}

// GroupDistribution returns the exact rational distribution of group, or nil
// entries when the group currently holds no records. The second result reports
// whether the group exists.
func (v *Verifier) GroupDistribution(group string) ([]*big.Rat, bool) {
	snap := v.snapshot()
	g, ok := snap.groups[group]
	if !ok {
		return nil, false
	}
	return countsToRatDist(g.counts, g.total), true
}

// GroupSize returns the current record count of group.
func (v *Verifier) GroupSize(group string) int64 {
	snap := v.snapshot()
	if g, ok := snap.groups[group]; ok {
		return g.total
	}
	return 0
}

// GrandTotal returns the current population record count.
func (v *Verifier) GrandTotal() int64 {
	snap := v.snapshot()
	return snap.total
}

package energy

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"
)

// Feature identifies a single coverage point (for example a basic-block edge).
type Feature uint64

// Sample is one corpus entry: its identifier, the deduplicated set of features
// it covers, and the number of times it has executed without the harness
// classifying the run as invalid.
type Sample struct {
	ID       string
	Coverage []Feature
	Execs    int64
}

// Allocation is the integer energy handed to one sample for a round.
type Allocation struct {
	SampleID string
	Energy   int64
}

// Sentinel errors returned by allocator and transaction operations.
var (
	// ErrDuplicateID is returned when a sample with the same ID already exists.
	ErrDuplicateID = errors.New("energy: duplicate sample id")
	// ErrSampleNotFound is returned when an operation targets a missing sample.
	ErrSampleNotFound = errors.New("energy: sample not found")
	// ErrInvalidExecs is returned for negative execution counts.
	ErrInvalidExecs = errors.New("energy: execs must be non-negative")
	// ErrInvalidBudget is returned when an allocation budget is negative.
	ErrInvalidBudget = errors.New("energy: budget must be non-negative")
	// ErrTxClosed is returned when a transaction is reused after commit/abort.
	ErrTxClosed = errors.New("energy: transaction already committed or rolled back")
)

// Allocator maintains corpus samples and their coverage frequencies
// incrementally. The zero value is not usable; create one with New.
type Allocator struct {
	mu        sync.RWMutex
	samples   map[string]*Sample
	frequency map[Feature]int64
}

// New returns an empty allocator.
func New() *Allocator {
	return &Allocator{
		samples:   make(map[string]*Sample),
		frequency: make(map[Feature]int64),
	}
}

// Len reports the number of registered samples.
func (a *Allocator) Len() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.samples)
}

// Add registers a new sample and increases every covered feature's frequency
// by its valid execution count.
func (a *Allocator) Add(s Sample) error {
	if s.Execs < 0 {
		return ErrInvalidExecs
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.samples[s.ID]; ok {
		return fmt.Errorf("%w: %q", ErrDuplicateID, s.ID)
	}
	a.addLocked(s)
	return nil
}

func (a *Allocator) addLocked(s Sample) {
	stored := &Sample{
		ID:       s.ID,
		Coverage: dedup(s.Coverage),
		Execs:    s.Execs,
	}
	a.samples[stored.ID] = stored
	for _, feature := range stored.Coverage {
		a.frequency[feature] += stored.Execs
	}
}

// Remove deletes a sample and subtracts its execution count from every covered
// feature's frequency, so the remaining corpus is scheduled against current
// rather than historical rarity.
func (a *Allocator) Remove(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	sample, ok := a.samples[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrSampleNotFound, id)
	}
	a.removeLocked(sample)
	return nil
}

func (a *Allocator) removeLocked(sample *Sample) {
	for _, feature := range sample.Coverage {
		a.frequency[feature] -= sample.Execs
	}
	delete(a.samples, sample.ID)
}

// SetExecs replaces the valid execution count of an existing sample and
// adjusts all of its feature frequencies by the difference.
func (a *Allocator) SetExecs(id string, execs int64) error {
	if execs < 0 {
		return ErrInvalidExecs
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	sample, ok := a.samples[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrSampleNotFound, id)
	}
	a.setExecsLocked(sample, execs)
	return nil
}

func (a *Allocator) setExecsLocked(sample *Sample, execs int64) {
	delta := execs - sample.Execs
	for _, feature := range sample.Coverage {
		a.frequency[feature] += delta
	}
	sample.Execs = execs
}

// AddExecs adds delta (which may be negative) to the sample's valid execution
// count, rejecting the change if it would make the count negative.
func (a *Allocator) AddExecs(id string, delta int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	sample, ok := a.samples[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrSampleNotFound, id)
	}
	next := sample.Execs + delta
	if next < 0 {
		return fmt.Errorf("%w: resulting execs %d for %q", ErrInvalidExecs, next, id)
	}
	a.setExecsLocked(sample, next)
	return nil
}

// ReplaceCoverage swaps the covered feature set of an existing sample and
// rebuilds the affected feature frequencies.
func (a *Allocator) ReplaceCoverage(id string, coverage []Feature) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	sample, ok := a.samples[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrSampleNotFound, id)
	}
	a.replaceCoverageLocked(sample, coverage)
	return nil
}

func (a *Allocator) replaceCoverageLocked(sample *Sample, coverage []Feature) {
	for _, feature := range sample.Coverage {
		a.frequency[feature] -= sample.Execs
	}
	sample.Coverage = dedup(coverage)
	for _, feature := range sample.Coverage {
		a.frequency[feature] += sample.Execs
	}
}

// Get returns a copy of the sample with the given ID.
func (a *Allocator) Get(id string) (Sample, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	sample, ok := a.samples[id]
	if !ok {
		return Sample{}, false
	}
	return *sample, true
}

// Samples returns copies of all samples ordered by ID.
func (a *Allocator) Samples() []Sample {
	a.mu.RLock()
	defer a.mu.RUnlock()
	ids := make([]string, 0, len(a.samples))
	for id := range a.samples {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Sample, 0, len(ids))
	for _, id := range ids {
		out = append(out, *a.samples[id])
	}
	return out
}

// Frequency returns the current frequency of a feature: the summed valid
// execution counts of every sample currently covering it.
func (a *Allocator) Frequency(feature Feature) int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.frequency[feature]
}

// Frequencies returns a copy of the whole feature frequency table.
func (a *Allocator) Frequencies() map[Feature]int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make(map[Feature]int64, len(a.frequency))
	for feature, count := range a.frequency {
		out[feature] = count
	}
	return out
}

// Allocate distributes budget over every registered sample. The returned slice
// is ordered by sample ID; every registered sample appears exactly once. When
// no sample has positive weight the whole budget is left unspent and every
// allocation is zero.
func (a *Allocator) Allocate(budget int64) ([]Allocation, error) {
	if budget < 0 {
		return nil, ErrInvalidBudget
	}
	a.mu.RLock()
	defer a.mu.RUnlock()

	ids := make([]string, 0, len(a.samples))
	for id := range a.samples {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	weights := make([]*big.Rat, len(ids))
	for index, id := range ids {
		weights[index] = sampleWeight(a.samples[id], a.frequency)
	}
	return largestRemainder(ids, weights, budget), nil
}

// RecomputeAllocate is the full-recomputation oracle: it rebuilds every feature
// frequency from scratch over the given samples and then applies exactly the
// same weighting and largest-remainder split as Allocator. It is pure and
// safe to call concurrently with any allocator operation.
func RecomputeAllocate(samples []Sample, budget int64) ([]Allocation, error) {
	if budget < 0 {
		return nil, ErrInvalidBudget
	}
	for _, sample := range samples {
		if sample.Execs < 0 {
			return nil, fmt.Errorf("%w: sample %q", ErrInvalidExecs, sample.ID)
		}
	}

	frequency := make(map[Feature]int64)
	seenIDs := make(map[string]struct{}, len(samples))
	normalized := make([]Sample, len(samples))
	for index, sample := range samples {
		if _, dup := seenIDs[sample.ID]; dup {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateID, sample.ID)
		}
		seenIDs[sample.ID] = struct{}{}
		sample.Coverage = dedup(sample.Coverage)
		normalized[index] = sample
		for _, feature := range sample.Coverage {
			frequency[feature] += sample.Execs
		}
	}

	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i].ID < normalized[j].ID
	})

	ids := make([]string, len(normalized))
	weights := make([]*big.Rat, len(normalized))
	for index, sample := range normalized {
		ids[index] = sample.ID
		copyOfSample := sample
		weights[index] = sampleWeight(&copyOfSample, frequency)
	}
	return largestRemainder(ids, weights, budget), nil
}

// sampleWeight returns 1 divided by the smallest frequency in the sample's
// coverage. Empty coverage or a sample without valid executions has no
// observed rarity and gets weight zero.
func sampleWeight(sample *Sample, frequency map[Feature]int64) *big.Rat {
	if sample.Execs <= 0 || len(sample.Coverage) == 0 {
		return new(big.Rat)
	}
	rarest := int64(0)
	for _, feature := range sample.Coverage {
		count := frequency[feature]
		if rarest == 0 || count < rarest {
			rarest = count
		}
	}
	if rarest <= 0 {
		return new(big.Rat)
	}
	return big.NewRat(1, rarest)
}

// candidate carries the unrounded share of one sample through the
// largest-remainder rounding pass.
type candidate struct {
	index     int
	remainder *big.Rat
}

// largestRemainder splits budget proportionally to weights using the
// largest-remainder (Hare) method. Each weight is first floored to an integer;
// the leftover units are handed out one by one in descending fractional
// remainder order, with equal remainders broken by ascending sample ID. The
// sum of the returned energies never exceeds budget and equals it whenever at
// least one weight is positive.
func largestRemainder(ids []string, weights []*big.Rat, budget int64) []Allocation {
	allocations := make([]Allocation, len(ids))
	for index, id := range ids {
		allocations[index] = Allocation{SampleID: id}
	}
	if budget == 0 || len(ids) == 0 {
		return allocations
	}

	totalWeight := new(big.Rat)
	for _, weight := range weights {
		totalWeight.Add(totalWeight, weight)
	}
	if totalWeight.Sign() == 0 {
		return allocations
	}

	budgetRat := big.NewRat(budget, 1)
	candidates := make([]candidate, 0, len(ids))
	assigned := int64(0)
	for index, weight := range weights {
		if weight.Sign() == 0 {
			continue
		}
		share := new(big.Rat).Mul(budgetRat, weight)
		share.Quo(share, totalWeight)
		base, remainder := splitFloor(share)
		assigned += base
		allocations[index].Energy = base
		candidates = append(candidates, candidate{
			index:     index,
			remainder: remainder,
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if compared := candidates[i].remainder.Cmp(candidates[j].remainder); compared != 0 {
			return compared > 0
		}
		return ids[candidates[i].index] < ids[candidates[j].index]
	})

	leftover := budget - assigned
	for index := 0; leftover > 0 && index < len(candidates); index++ {
		allocations[candidates[index].index].Energy++
		leftover--
	}
	return allocations
}

// splitFloor breaks value into its integer floor and the non-negative
// fractional remainder, so value == floor + remainder exactly.
func splitFloor(value *big.Rat) (int64, *big.Rat) {
	floor := new(big.Int).Quo(value.Num(), value.Denom())
	floorValue := floor.Int64()
	remainder := new(big.Rat).Sub(value, new(big.Rat).SetInt(floor))
	return floorValue, remainder
}

// dedup returns a freshly allocated, deduplicated copy of features.
func dedup(features []Feature) []Feature {
	if len(features) == 0 {
		return nil
	}
	seen := make(map[Feature]struct{}, len(features))
	out := make([]Feature, 0, len(features))
	for _, feature := range features {
		if _, ok := seen[feature]; ok {
			continue
		}
		seen[feature] = struct{}{}
		out = append(out, feature)
	}
	return out
}

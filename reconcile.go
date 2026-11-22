package takeover

import (
	"sort"
)

// EvidenceClass is a claim about a sequence range.
type EvidenceClass uint8

const (
	// EvidencePending says that the range has not been proven either way.
	EvidencePending EvidenceClass = iota
	// EvidenceControlled says that a live grant still controls the range.
	EvidenceControlled
	// EvidenceInvalid says that a fence, return, seal, or consumption has
	// definitively invalidated the range for further control.
	EvidenceInvalid
)

const (
	ClaimPending    = EvidencePending
	ClaimControlled = EvidenceControlled
	ClaimInvalid    = EvidenceInvalid
)

// String returns a stable name.
func (c EvidenceClass) String() string {
	switch c {
	case EvidenceControlled:
		return "controlled"
	case EvidenceInvalid:
		return "invalid"
	default:
		return "pending"
	}
}

// Observation is one piece of state evidence. Observations are independent of
// arrival order. For a sequence, only evidence at the highest observed epoch
// is decisive; conflicting classes at that epoch remain pending.
type Observation struct {
	ID             string         `json:"id,omitempty"`
	GrantID        GrantID        `json:"grant_id,omitempty"`
	ProcessStartID ProcessStartID `json:"process_start_id,omitempty"`
	SessionRoot    SessionRoot    `json:"session_root,omitempty"`
	Epoch          uint64         `json:"epoch"`
	Scope          Scope          `json:"scope"`
	Class          EvidenceClass  `json:"class"`
}

// ControlledObservation constructs a positive control observation.
func ControlledObservation(grantID GrantID, epoch uint64, sequences SeqSet, dimensions ...Dimension) Observation {
	return Observation{
		GrantID: grantID,
		Epoch:   epoch,
		Scope: Scope{
			Dimensions: Dimensions(dimensions...),
			Sequences:  sequences,
		},
		Class: EvidenceControlled,
	}
}

// InvalidObservation constructs a definitive-invalid observation.
func InvalidObservation(grantID GrantID, epoch uint64, sequences SeqSet, dimensions ...Dimension) Observation {
	return Observation{
		GrantID: grantID,
		Epoch:   epoch,
		Scope: Scope{
			Dimensions: Dimensions(dimensions...),
			Sequences:  sequences,
		},
		Class: EvidenceInvalid,
	}
}

// PendingObservation constructs a still-unproven observation.
func PendingObservation(grantID GrantID, epoch uint64, sequences SeqSet, dimensions ...Dimension) Observation {
	return Observation{
		GrantID: grantID,
		Epoch:   epoch,
		Scope: Scope{
			Dimensions: Dimensions(dimensions...),
			Sequences:  sequences,
		},
		Class: EvidencePending,
	}
}

// Partition is a three-way, non-overlapping partition of a sequence universe.
type Partition struct {
	Universe   SeqSet `json:"universe"`
	Controlled SeqSet `json:"controlled"`
	Invalid    SeqSet `json:"invalid"`
	Pending    SeqSet `json:"pending"`
}

// ProvenControlled returns the proven controllable range.
func (p Partition) ProvenControlled() SeqSet {
	return p.Controlled.Clone()
}

// Proven is an alias for ProvenControlled.
func (p Partition) Proven() SeqSet {
	return p.ProvenControlled()
}

// DefinitelyInvalid returns the definitively invalid range.
func (p Partition) DefinitelyInvalid() SeqSet {
	return p.Invalid.Clone()
}

// AwaitingProof returns the still-unproven range.
func (p Partition) AwaitingProof() SeqSet {
	return p.Pending.Clone()
}

// Unproven is an alias for AwaitingProof.
func (p Partition) Unproven() SeqSet {
	return p.AwaitingProof()
}

// Classify returns the class of seq and whether it belongs to the universe.
func (p Partition) Classify(seq uint64) (EvidenceClass, bool) {
	switch {
	case p.Controlled.Contains(seq):
		return EvidenceControlled, true
	case p.Invalid.Contains(seq):
		return EvidenceInvalid, true
	case p.Pending.Contains(seq):
		return EvidencePending, true
	default:
		return EvidencePending, false
	}
}

// Validate checks the non-overlap and exact-cover requirements.
func (p Partition) Validate() error {
	if !p.Controlled.IsDisjoint(p.Invalid) ||
		!p.Controlled.IsDisjoint(p.Pending) ||
		!p.Invalid.IsDisjoint(p.Pending) {
		return ErrPartition
	}
	union := p.Controlled.Union(p.Invalid).Union(p.Pending)
	if !union.Equal(p.Universe) {
		return ErrPartition
	}
	return nil
}

// Reconcile merges observations into one partition of universe. The result is
// independent of observation order. A sequence omitted from the universe is
// omitted from all three result sets.
func Reconcile(universe SeqSet, observations ...Observation) (Partition, error) {
	if universe.IsEmpty() {
		for _, observation := range observations {
			universe = universe.Union(observation.Scope.Sequences)
		}
	}
	partition := Partition{
		Universe:   universe.Clone(),
		Controlled: SeqSet{},
		Invalid:    SeqSet{},
		Pending:    SeqSet{},
	}
	if universe.IsEmpty() {
		return partition, nil
	}

	boundaries := make([]uint64, 0, len(observations)*2+2)
	for _, interval := range universe.Ranges() {
		boundaries = append(boundaries, interval.Start)
		if interval.End != ^uint64(0) {
			boundaries = append(boundaries, interval.End+1)
		}
	}
	for _, observation := range observations {
		for _, interval := range observation.Scope.Sequences.Ranges() {
			boundaries = append(boundaries, interval.Start)
			if interval.End != ^uint64(0) {
				boundaries = append(boundaries, interval.End+1)
			}
		}
	}
	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i] < boundaries[j] })
	boundaries = uniqueUint64(boundaries)

	for i, start := range boundaries {
		if !universe.Contains(start) {
			continue
		}
		end := ^uint64(0)
		if i+1 < len(boundaries) {
			end = boundaries[i+1] - 1
		}
		if end < start {
			continue
		}

		class := classifyPoint(start, observations)
		interval := SeqRange{Start: start, End: end}
		switch class {
		case EvidenceControlled:
			partition.Controlled = partition.Controlled.Union(Set(interval))
		case EvidenceInvalid:
			partition.Invalid = partition.Invalid.Union(Set(interval))
		default:
			partition.Pending = partition.Pending.Union(Set(interval))
		}
	}
	partition.Controlled = partition.Controlled.Intersect(universe)
	partition.Invalid = partition.Invalid.Intersect(universe)
	partition.Pending = partition.Pending.Intersect(universe)
	if err := partition.Validate(); err != nil {
		return Partition{}, err
	}
	return partition, nil
}

func classifyPoint(seq uint64, observations []Observation) EvidenceClass {
	maxEpoch := uint64(0)
	found := false
	var classes [3]bool
	for _, observation := range observations {
		if !observation.Scope.Sequences.Contains(seq) {
			continue
		}
		class := observation.Class
		if class > EvidenceInvalid {
			class = EvidencePending
		}
		if !found || observation.Epoch > maxEpoch {
			found = true
			maxEpoch = observation.Epoch
			classes = [3]bool{}
			classes[class] = true
			continue
		}
		if observation.Epoch == maxEpoch {
			classes[class] = true
		}
	}
	if !found {
		return EvidencePending
	}
	if classes[EvidenceInvalid] {
		if classes[EvidenceControlled] || classes[EvidencePending] {
			return EvidencePending
		}
		return EvidenceInvalid
	}
	if classes[EvidenceControlled] {
		if classes[EvidencePending] {
			return EvidencePending
		}
		return EvidenceControlled
	}
	return EvidencePending
}

func uniqueUint64(values []uint64) []uint64 {
	if len(values) == 0 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

// ReconcileViews computes the conservative intersection of multiple compatible
// views. A sequence is controlled or invalid only if every view agrees; any
// disagreement is pending.
func ReconcileViews(universe SeqSet, views ...[]Observation) (Partition, error) {
	if len(views) == 0 {
		return Reconcile(universe)
	}
	partitions := make([]Partition, 0, len(views))
	for _, view := range views {
		partition, err := Reconcile(universe, view...)
		if err != nil {
			return Partition{}, err
		}
		partitions = append(partitions, partition)
	}
	controlled := partitions[0].Controlled
	invalid := partitions[0].Invalid
	for _, partition := range partitions[1:] {
		controlled = controlled.Intersect(partition.Controlled)
		invalid = invalid.Intersect(partition.Invalid)
	}
	controlled = controlled.Intersect(universe)
	invalid = invalid.Intersect(universe).Subtract(controlled)
	pending := universe.Subtract(controlled).Subtract(invalid)
	result := Partition{
		Universe:   universe.Clone(),
		Controlled: controlled,
		Invalid:    invalid,
		Pending:    pending,
	}
	if err := result.Validate(); err != nil {
		return Partition{}, err
	}
	return result, nil
}

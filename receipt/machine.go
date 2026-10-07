package receipt

import (
	"errors"
	"fmt"
	"sort"
)

// generationState is the reduced state of one execution generation.
type generationState struct {
	Revision      int64
	Status        Status
	HasStatus     bool
	Archived      []Receipt // lower-revision / rejected receipts, evidence kept
	Seen          map[int64]Receipt
	Compensations []Compensation
	SupersededBy  string
}

// Machine is the deterministic core: it reduces the event stream into
// receipt state. It performs no I/O, so it is replayable and testable.
type Machine struct {
	generations map[string]*generationState
	current     string
	buffered    map[string][]Receipt // receipts for generations not yet announced
}

// NewMachine returns an empty machine (no current generation).
func NewMachine() *Machine {
	return &Machine{
		generations: make(map[string]*generationState),
		buffered:    make(map[string][]Receipt),
	}
}

// Bootstrap creates the first execution generation. It is a no-op if any
// generation is already current.
func (m *Machine) Bootstrap(generation string) error {
	if generation == "" {
		return fmt.Errorf("bootstrap: empty generation")
	}
	if m.current != "" {
		return fmt.Errorf("bootstrap: generation %q already current", m.current)
	}
	m.current = generation
	m.generation(generation)
	return nil
}

// Current returns the unique current generation, or "" if none exists.
func (m *Machine) Current() string { return m.current }

func (m *Machine) generation(id string) *generationState {
	g, ok := m.generations[id]
	if !ok {
		g = &generationState{Seen: make(map[int64]Receipt)}
		m.generations[id] = g
	}
	return g
}

// Apply folds one persisted event into the state.
func (m *Machine) Apply(e Event) error {
	switch e.Type {
	case EventBootstrap:
		if e.Bootstrap == nil {
			return fmt.Errorf("bootstrap event without payload")
		}
		return m.Bootstrap(*e.Bootstrap)
	case EventReceipt:
		if e.Receipt == nil {
			return fmt.Errorf("receipt event without payload")
		}
		return m.applyReceipt(*e.Receipt)
	case EventSupersede:
		if e.Supersede == nil {
			return fmt.Errorf("supersede event without payload")
		}
		return m.applySupersede(*e.Supersede)
	}
	return fmt.Errorf("unknown event type %q", e.Type)
}

// applyReceipt routes a receipt to its generation. It returns true when the
// receipt advanced the business state of its generation.
func (m *Machine) applyReceipt(r Receipt) error {
	if err := r.validate(); err != nil {
		return err
	}
	if m.current == "" {
		return fmt.Errorf("receipt: no current generation, bootstrap first")
	}
	g, known := m.generations[r.Generation]
	switch {
	case known && g.SupersededBy == "":
		// Current generation: advance business.
		return m.applyToGeneration(g, r)
	case known:
		// Superseded generation: never overwrite the successor; a late
		// terminal receipt records a pending compensation effect.
		if prev, dup := g.Seen[r.Revision]; dup {
			if prev.Status != r.Status {
				return &ConflictError{r.Generation, r.Revision, prev.Status, r.Status}
			}
			return nil
		}
		g.Seen[r.Revision] = r
		g.Archived = append(g.Archived, r)
		if r.Status.Terminal() {
			g.Compensations = append(g.Compensations, Compensation{
				Generation: r.Generation,
				Revision:   r.Revision,
				Status:     r.Status,
				Evidence:   r.Evidence,
			})
		}
		return nil
	default:
		// Not announced yet: buffer until a supersede resolution commits.
		m.buffered[r.Generation] = append(m.buffered[r.Generation], r)
		return &UnknownGenerationError{Generation: r.Generation}
	}
}

// applyToGeneration applies the revision rules for the current generation.
func (m *Machine) applyToGeneration(g *generationState, r Receipt) error {
	if prev, ok := g.Seen[r.Revision]; ok {
		if prev.Status != r.Status {
			return &ConflictError{r.Generation, r.Revision, prev.Status, r.Status}
		}
		return nil // idempotent duplicate
	}
	g.Seen[r.Revision] = r
	switch {
	case !g.HasStatus || r.Revision > g.Revision:
		if g.HasStatus && g.Status.Terminal() && !r.Status.Terminal() {
			// Terminal state never regresses to running/accepted; the
			// receipt is archived so no evidence is lost.
			g.Archived = append(g.Archived, r)
			return &RegressionError{r.Generation, r.Revision, g.Status, r.Status}
		}
		g.Revision, g.Status, g.HasStatus = r.Revision, r.Status, true
		return nil
	default:
		// Lower revision: archive only, evidence preserved.
		g.Archived = append(g.Archived, r)
		return nil
	}
}

// applySupersede commits a resolution: the old generation is retired and the
// new one becomes the unique current generation. Receipts buffered for the
// new generation are folded in revision order.
func (m *Machine) applySupersede(s SupersedeRecord) error {
	if err := s.validate(); err != nil {
		return err
	}
	if m.current == "" {
		return fmt.Errorf("supersede: no current generation")
	}
	if s.Old != m.current {
		return fmt.Errorf("supersede: generation %q is not current (%q is)", s.Old, m.current)
	}
	if _, exists := m.generations[s.New]; exists {
		return fmt.Errorf("supersede: generation %q already exists", s.New)
	}
	pending := m.buffered[s.New]
	sort.SliceStable(pending, func(i, j int) bool { return pending[i].Revision < pending[j].Revision })
	// Fold buffered receipts into a temporary state first: a conflicting
	// buffer vetoes the resolution without mutating anything.
	fresh := &generationState{Seen: make(map[int64]Receipt)}
	for _, r := range pending {
		if err := m.applyToGeneration(fresh, r); err != nil {
			var reg *RegressionError
			if errors.As(err, &reg) {
				continue // archived evidence, not a veto
			}
			return err
		}
	}
	old := m.generation(s.Old)
	old.SupersededBy = s.New
	m.current = s.New
	m.generations[s.New] = fresh
	delete(m.buffered, s.New)
	return nil
}

// --- read model -----------------------------------------------------------

// GenerationView is a snapshot of one generation for inspection and
// checkpointing.
type GenerationView struct {
	Revision      int64          `json:"revision"`
	Status        Status         `json:"status"`
	HasStatus     bool           `json:"hasStatus"`
	Archived      []Receipt      `json:"archived,omitempty"`
	Compensations []Compensation `json:"compensations,omitempty"`
	SupersededBy  string         `json:"supersededBy,omitempty"`
}

// View returns the state of one generation.
func (m *Machine) View(generation string) (GenerationView, bool) {
	g, ok := m.generations[generation]
	if !ok {
		return GenerationView{}, false
	}
	return GenerationView{
		Revision:      g.Revision,
		Status:        g.Status,
		HasStatus:     g.HasStatus,
		Archived:      append([]Receipt(nil), g.Archived...),
		Compensations: append([]Compensation(nil), g.Compensations...),
		SupersededBy:  g.SupersededBy,
	}, true
}

// Evidence returns every receipt recorded for a generation, applied or
// archived, ordered by revision. No evidence is ever lost.
func (m *Machine) Evidence(generation string) []Receipt {
	g, ok := m.generations[generation]
	if !ok {
		return nil
	}
	out := make([]Receipt, 0, len(g.Seen))
	for _, r := range g.Seen {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Revision < out[j].Revision })
	return out
}

// Buffered returns receipts waiting for a supersede resolution.
func (m *Machine) Buffered(generation string) []Receipt {
	return append([]Receipt(nil), m.buffered[generation]...)
}

// --- checkpointing --------------------------------------------------------

type generationSnapshot struct {
	Revision      int64             `json:"revision"`
	Status        Status            `json:"status"`
	HasStatus     bool              `json:"hasStatus"`
	Archived      []Receipt         `json:"archived,omitempty"`
	Seen          map[int64]Receipt `json:"seen,omitempty"`
	Compensations []Compensation    `json:"compensations,omitempty"`
	SupersededBy  string            `json:"supersededBy,omitempty"`
}

// Snapshot is the serializable full state of a Machine.
type Snapshot struct {
	Generations map[string]generationSnapshot `json:"generations"`
	Current     string                        `json:"current"`
	Buffered    map[string][]Receipt          `json:"buffered,omitempty"`
}

// Snapshot exports the complete machine state for checkpointing.
func (m *Machine) Snapshot() Snapshot {
	s := Snapshot{
		Generations: make(map[string]generationSnapshot, len(m.generations)),
		Current:     m.current,
	}
	for id, g := range m.generations {
		seen := make(map[int64]Receipt, len(g.Seen))
		for rev, r := range g.Seen {
			seen[rev] = r
		}
		s.Generations[id] = generationSnapshot{
			Revision:      g.Revision,
			Status:        g.Status,
			HasStatus:     g.HasStatus,
			Archived:      append([]Receipt(nil), g.Archived...),
			Seen:          seen,
			Compensations: append([]Compensation(nil), g.Compensations...),
			SupersededBy:  g.SupersededBy,
		}
	}
	if len(m.buffered) > 0 {
		s.Buffered = make(map[string][]Receipt, len(m.buffered))
		for id, rs := range m.buffered {
			s.Buffered[id] = append([]Receipt(nil), rs...)
		}
	}
	return s
}

// RestoreSnapshot rebuilds a machine from a checkpoint snapshot.
func RestoreSnapshot(s Snapshot) *Machine {
	m := NewMachine()
	m.current = s.Current
	for id, gs := range s.Generations {
		seen := make(map[int64]Receipt, len(gs.Seen))
		for rev, r := range gs.Seen {
			seen[rev] = r
		}
		m.generations[id] = &generationState{
			Revision:      gs.Revision,
			Status:        gs.Status,
			HasStatus:     gs.HasStatus,
			Archived:      append([]Receipt(nil), gs.Archived...),
			Seen:          seen,
			Compensations: append([]Compensation(nil), gs.Compensations...),
			SupersededBy:  gs.SupersededBy,
		}
	}
	for id, rs := range s.Buffered {
		m.buffered[id] = append([]Receipt(nil), rs...)
	}
	return m
}

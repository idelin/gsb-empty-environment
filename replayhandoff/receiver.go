package replayhandoff

import "sync"

type Receiver struct {
	cfg     Config
	modulus uint64
	half    uint64
	bound   uint64
	mu      sync.RWMutex
	states  map[StateKey]*deviceWindow
}

type deviceWindow struct {
	mu     sync.Mutex
	top    uint64
	hasTop bool
	seen   map[uint64]struct{}
}

func New(cfg Config) (*Receiver, error) {
	if cfg.Bits < 4 || cfg.Bits > 32 {
		return nil, conflictError(ConflictInvalidBitWidth, 0, 0, "bits must be between 4 and 32")
	}
	if cfg.Verifier == nil {
		return nil, conflictError(ConflictVerifierNotConfigured, 0, 0, "a checkpoint verifier is required")
	}

	modulus := uint64(1) << cfg.Bits
	half := modulus / 2
	bound := cfg.ReorderBound
	if bound == 0 {
		bound = half
	}
	if bound > half {
		return nil, conflictError(ConflictInvalidReorderBound, 0, 0, "reorder bound cannot exceed half the wire modulus")
	}

	return &Receiver{
		cfg:     cfg,
		modulus: modulus,
		half:    half,
		bound:   bound,
		states:  make(map[StateKey]*deviceWindow),
	}, nil
}

func conflictError(code ConflictCode, low, full uint64, detail string) error {
	return ConflictError{Conflict: Conflict{Code: code, Low: low, Full: full, Detail: detail}}
}

type ConflictError struct {
	Conflict Conflict
}

func (e ConflictError) Error() string {
	return string(e.Conflict.Code) + ": " + e.Conflict.Detail
}

func (r *Receiver) Import(material HandoffMaterial) Decision {
	key := StateKey{Device: material.Device, Generation: material.Generation}
	r.mu.RLock()
	state, exists := r.states[key]
	r.mu.RUnlock()
	if !exists {
		state = &deviceWindow{seen: make(map[uint64]struct{})}
	}

	state.mu.Lock()

	analysis := r.analyze(material, nil, state)
	if analysis.Outcome != OutcomeAccepted {
		state.mu.Unlock()
		return analysis.Decision
	}

	if !exists {
		r.mu.Lock()
		if existing, concurrent := r.states[key]; concurrent {
			state.mu.Unlock()
			state = existing
			state.mu.Lock()
			analysis = r.analyze(material, nil, state)
			if analysis.Outcome != OutcomeAccepted {
				state.mu.Unlock()
				r.mu.Unlock()
				return analysis.Decision
			}
		} else {
			r.states[key] = state
		}
		r.mu.Unlock()
	}

	imported := make([]uint64, 0)
	for i := range analysis.variables {
		if analysis.variables[i].entry.Admitted && len(analysis.variables[i].allowed) == 1 {
			imported = append(imported, analysis.variables[i].allowed[0])
		}
	}
	for _, checkpoint := range material.Checkpoints {
		imported = append(imported, checkpoint.FullSequence)
	}
	imported = sortedUnique(imported)
	state.commit(imported, r.half)
	analysis.Decision.ReplayWindowTop = state.top
	analysis.Decision.ImportedSequences = imported
	state.mu.Unlock()
	return analysis.Decision
}

func (r *Receiver) Admit(frame Frame, authenticated bool, material HandoffMaterial) Decision {
	if !authenticated {
		return Decision{
			Outcome:    OutcomeInvalidAuthentication,
			Device:     frame.Device,
			Generation: frame.Generation,
		}
	}

	state := r.state(StateKey{Device: frame.Device, Generation: frame.Generation})
	state.mu.Lock()
	defer state.mu.Unlock()

	analysis := r.analyze(material, &frame, state)
	if analysis.Outcome != OutcomeAccepted {
		return analysis.Decision
	}

	toRegister := make([]uint64, 0)
	for i := range analysis.variables {
		if analysis.variables[i].entry.Admitted {
			toRegister = append(toRegister, analysis.variables[i].allowed...)
		}
	}
	for _, checkpoint := range material.Checkpoints {
		toRegister = append(toRegister, checkpoint.FullSequence)
	}
	toRegister = append(toRegister, analysis.Decision.FullSequences...)
	toRegister = sortedUnique(toRegister)
	state.commit(toRegister, r.half)
	analysis.Decision.ReplayWindowTop = state.top
	analysis.Decision.ImportedSequences = toRegister
	return analysis.Decision
}

func (r *Receiver) state(key StateKey) *deviceWindow {
	r.mu.Lock()
	defer r.mu.Unlock()

	state, ok := r.states[key]
	if !ok {
		state = &deviceWindow{seen: make(map[uint64]struct{})}
		r.states[key] = state
	}
	return state
}

func (s *deviceWindow) commit(values []uint64, windowSize uint64) {
	for _, value := range values {
		if !s.hasTop || value > s.top {
			s.top = value
		}
		s.seen[value] = struct{}{}
	}
	s.hasTop = true

	cutoff := uint64(0)
	if s.top >= windowSize {
		cutoff = s.top - windowSize + 1
	}
	for value := range s.seen {
		if value < cutoff {
			delete(s.seen, value)
		}
	}
}

func (s *deviceWindow) isOld(value uint64, windowSize uint64) bool {
	if _, ok := s.seen[value]; ok {
		return true
	}
	if !s.hasTop || s.top < windowSize {
		return false
	}
	return value <= s.top-windowSize
}

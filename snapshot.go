package impact

import (
	"errors"
	"fmt"
	"sort"
)

// Snapshot is a versioned collection of signed summaries, symbol bindings,
// entry points, and the legal-configuration BDD. Builders mutate a snapshot
// until it is handed to an Engine; Engine clones its input, so later builder
// calls never alter an existing analysis or certificate.
type Snapshot struct {
	manager   *Manager
	valid     BDD
	summaries map[SummaryKey]SignedSummary
	bindings  map[string][]Binding
	entries   []SummaryKey
	keyRing   *KeyRing
}

// NewSnapshot starts an empty snapshot with a fixed manager and valid-
// configuration constraint.
func NewSnapshot(m *Manager, valid BDD) (*Snapshot, error) {
	if m == nil {
		return nil, errors.New("impact: snapshot requires a manager")
	}
	if !valid.Valid() {
		return nil, errors.New("impact: snapshot requires an initialized valid BDD")
	}
	if valid.m != m {
		return nil, errors.New("impact: valid BDD belongs to a different manager")
	}
	return &Snapshot{
		manager:   m,
		valid:     valid,
		summaries: make(map[SummaryKey]SignedSummary),
		bindings:  make(map[string][]Binding),
	}, nil
}

// MustNewSnapshot is NewSnapshot with panic-on-error semantics.
func MustNewSnapshot(m *Manager, valid BDD) *Snapshot {
	s, err := NewSnapshot(m, valid)
	if err != nil {
		panic(err)
	}
	return s
}

// BuildSnapshot is a convenience constructor for a complete snapshot.
func BuildSnapshot(m *Manager, valid BDD, entries []SummaryKey, signed []SignedSummary, bindings map[string][]Binding) (*Snapshot, error) {
	s, err := NewSnapshot(m, valid)
	if err != nil {
		return nil, err
	}
	for _, ss := range signed {
		if err := s.AddSummary(ss); err != nil {
			return nil, err
		}
	}
	symbols := make([]string, 0, len(bindings))
	for symbol := range bindings {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	for _, symbol := range symbols {
		if err := s.SetBindings(symbol, bindings[symbol]); err != nil {
			return nil, err
		}
	}
	if err := s.SetEntries(entries...); err != nil {
		return nil, err
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return s, nil
}

// Manager returns the fixed BDD manager.
func (s *Snapshot) Manager() *Manager { return s.manager }

// Valid returns the legal-configuration constraint.
func (s *Snapshot) Valid() BDD { return s.valid }

// SetValid replaces the legal-configuration constraint. The BDD must use the
// snapshot manager's fixed variable order.
func (s *Snapshot) SetValid(valid BDD) error {
	if s.manager == nil {
		return errors.New("impact: uninitialized snapshot")
	}
	if !valid.Valid() || valid.m != s.manager {
		return errors.New("impact: valid BDD manager mismatch")
	}
	if err := valid.IsCanonical(); err != nil {
		return fmt.Errorf("impact: valid BDD: %w", err)
	}
	s.valid = valid
	return nil
}

// AddSummary adds or replaces one signed summary. Replacing a summary is an
// explicit snapshot edit; the old value is not mutated.
func (s *Snapshot) AddSummary(signed SignedSummary) error {
	if s.manager == nil {
		return errors.New("impact: uninitialized snapshot")
	}
	if err := signed.Summary.Validate(s.manager); err != nil {
		return err
	}
	key := signed.Summary.Key()
	if key == SummaryKey(Provenance{}.Key()) {
		return errors.New("impact: summary has an empty provenance key")
	}
	if signed.Certificate != nil && signed.Certificate.Provenance != signed.Summary.Provenance {
		return fmt.Errorf("impact: summary %q has a certificate for a different provenance", signed.Summary.Provenance)
	}
	s.summaries[key] = cloneSignedSummary(signed)
	return nil
}

// RemoveSummary removes a summary. Calls that resolve to its key become
// missing evidence rather than silently resolving by name.
func (s *Snapshot) RemoveSummary(key SummaryKey) {
	delete(s.summaries, key)
}

// SetBindings replaces every binding for symbol.
func (s *Snapshot) SetBindings(symbol string, bindings []Binding) error {
	if symbol == "" {
		return errors.New("impact: empty binding symbol")
	}
	copied := make([]Binding, len(bindings))
	for i, b := range bindings {
		if b.Symbol != "" && b.Symbol != symbol {
			return fmt.Errorf("impact: binding %q[%d] names a different symbol %q", symbol, i, b.Symbol)
		}
		if err := b.Validate(s.manager, symbol, i); err != nil {
			return err
		}
		b.Symbol = symbol
		copied[i] = b
	}
	if len(copied) == 0 {
		delete(s.bindings, symbol)
	} else {
		s.bindings[symbol] = copied
	}
	return nil
}

// SetBinding replaces symbol with exactly one binding.
func (s *Snapshot) SetBinding(symbol string, binding Binding) error {
	return s.SetBindings(symbol, []Binding{binding})
}

// AddBinding appends one binding for symbol.
func (s *Snapshot) AddBinding(symbol string, binding Binding) error {
	current := append([]Binding(nil), s.bindings[symbol]...)
	current = append(current, binding)
	return s.SetBindings(symbol, current)
}

// ClearBindings removes all bindings for symbol.
func (s *Snapshot) ClearBindings(symbol string) { delete(s.bindings, symbol) }

// SetEntries replaces the entry list, preserving order.
func (s *Snapshot) SetEntries(entries ...SummaryKey) error {
	s.entries = append([]SummaryKey(nil), entries...)
	return nil
}

// AddEntry appends one entry point.
func (s *Snapshot) AddEntry(entry SummaryKey) { s.entries = append(s.entries, entry) }

// SetKeyRing installs the key ring used by certificate verification helpers.
func (s *Snapshot) SetKeyRing(ring *KeyRing) {
	if ring == nil {
		s.keyRing = nil
		return
	}
	copied := NewKeyRing()
	for id, secret := range ring.keys {
		copied.keys[id] = append([]byte(nil), secret...)
	}
	s.keyRing = copied
}

// KeyRing returns a defensive copy of the snapshot's key ring, if any.
func (s *Snapshot) KeyRing() *KeyRing {
	if s.keyRing == nil {
		return nil
	}
	out := NewKeyRing()
	for id, secret := range s.keyRing.keys {
		out.keys[id] = append([]byte(nil), secret...)
	}
	return out
}

// Entries returns the entry list in order.
func (s *Snapshot) Entries() []SummaryKey {
	return append([]SummaryKey(nil), s.entries...)
}

// Summary returns a defensive copy of a signed summary.
func (s *Snapshot) Summary(key SummaryKey) (SignedSummary, bool) {
	ss, ok := s.summaries[key]
	if !ok {
		return SignedSummary{}, false
	}
	return cloneSignedSummary(ss), true
}

// HasSummary reports whether key has a signed summary.
func (s *Snapshot) HasSummary(key SummaryKey) bool {
	_, ok := s.summaries[key]
	return ok
}

// Summaries returns a defensive copy of all signed summaries.
func (s *Snapshot) Summaries() map[SummaryKey]SignedSummary {
	out := make(map[SummaryKey]SignedSummary, len(s.summaries))
	for k, v := range s.summaries {
		out[k] = cloneSignedSummary(v)
	}
	return out
}

// Bindings returns a defensive copy of all bindings for symbol.
func (s *Snapshot) Bindings(symbol string) []Binding {
	return append([]Binding(nil), s.bindings[symbol]...)
}

// AllBindings returns a defensive copy of the binding table.
func (s *Snapshot) AllBindings() map[string][]Binding {
	out := make(map[string][]Binding, len(s.bindings))
	for k, v := range s.bindings {
		out[k] = append([]Binding(nil), v...)
	}
	return out
}

// Validate checks cross-component invariants.
func (s *Snapshot) Validate() error {
	if s.manager == nil || !s.valid.Valid() {
		return errors.New("impact: uninitialized snapshot")
	}
	if s.valid.m != s.manager {
		return errors.New("impact: valid BDD manager mismatch")
	}
	if err := s.valid.IsCanonical(); err != nil {
		return fmt.Errorf("impact: valid BDD: %w", err)
	}
	for key, ss := range s.summaries {
		if key != ss.Summary.Key() {
			return fmt.Errorf("impact: summary map key %q does not match provenance %q", key, ss.Summary.Key())
		}
		if err := ss.Summary.Validate(s.manager); err != nil {
			return err
		}
	}
	for symbol, bindings := range s.bindings {
		if symbol == "" {
			return errors.New("impact: empty binding symbol in table")
		}
		for i, b := range bindings {
			if err := b.Validate(s.manager, symbol, i); err != nil {
				return err
			}
		}
	}
	seen := make(map[SummaryKey]bool)
	for _, entry := range s.entries {
		if entry == "" {
			return errors.New("impact: empty entry key")
		}
		if seen[entry] {
			return fmt.Errorf("impact: duplicate entry %q", entry)
		}
		seen[entry] = true
	}
	return nil
}

// VerifyCertificates verifies every present certificate. With a key ring it
// checks HMAC signatures; without one it checks provenance, signature presence,
// and the canonical digest. A summary with a nil certificate is reported as
// missing evidence; analysis treats both missing and invalid certificates as
// Unknown when reached.
func (s *Snapshot) VerifyCertificates(ring *KeyRing) error {
	if ring == nil {
		ring = s.keyRing
	}
	keys := make([]string, 0, len(s.summaries))
	for key := range s.summaries {
		keys = append(keys, string(key))
	}
	sort.Strings(keys)
	var problems []error
	for _, raw := range keys {
		key := SummaryKey(raw)
		ss := s.summaries[key]
		if ss.Certificate == nil {
			problems = append(problems, fmt.Errorf("impact: summary %q has no certificate", key))
			continue
		}
		if ring != nil {
			if err := ring.Verify(s.manager, ss.Summary, ss.Certificate); err != nil {
				problems = append(problems, fmt.Errorf("impact: summary %q: %w", key, err))
			}
			continue
		}
		if !certificatePresent(ss.Certificate, ss.Summary) {
			problems = append(problems, fmt.Errorf("impact: summary %q has missing signed evidence", key))
			continue
		}
		digest, err := SummaryDigest(s.manager, ss.Summary)
		if err != nil || digest != ss.Certificate.Digest {
			problems = append(problems, fmt.Errorf("impact: summary %q digest does not match", key))
		}
	}
	return errors.Join(problems...)
}

// CertificateValid reports whether key has a certificate that verifies under
// ring. Missing summaries are treated as invalid.
func (s *Snapshot) CertificateValid(ring *KeyRing, key SummaryKey) bool {
	ss, ok := s.summaries[key]
	if !ok || !certificatePresent(ss.Certificate, ss.Summary) {
		return false
	}
	if ring == nil {
		ring = s.keyRing
	}
	if ring != nil {
		return ring.Verify(s.manager, ss.Summary, ss.Certificate) == nil
	}
	digest, err := SummaryDigest(s.manager, ss.Summary)
	return err == nil && ss.Certificate.Provenance == ss.Summary.Provenance && digest == ss.Certificate.Digest
}

// Clone returns a deep copy safe for independent mutation.
func (s *Snapshot) Clone() *Snapshot {
	if s == nil {
		return nil
	}
	out := &Snapshot{
		manager:   s.manager,
		valid:     s.valid,
		summaries: make(map[SummaryKey]SignedSummary, len(s.summaries)),
		bindings:  make(map[string][]Binding, len(s.bindings)),
		entries:   append([]SummaryKey(nil), s.entries...),
	}
	for k, v := range s.summaries {
		out.summaries[k] = cloneSignedSummary(v)
	}
	for k, v := range s.bindings {
		out.bindings[k] = append([]Binding(nil), v...)
	}
	if s.keyRing != nil {
		out.SetKeyRing(s.keyRing)
	}
	return out
}

func cloneSignedSummary(ss SignedSummary) SignedSummary {
	out := SignedSummary{
		Summary: Summary{
			Provenance:      ss.Summary.Provenance,
			Body:            make([]Event, len(ss.Summary.Body)),
			OptionalSymbols: make([]OptionalSymbol, len(ss.Summary.OptionalSymbols)),
		},
	}
	for i, e := range ss.Summary.Body {
		e.Origin = cloneProvenance(e.Origin)
		out.Summary.Body[i] = e
	}
	copy(out.Summary.OptionalSymbols, ss.Summary.OptionalSymbols)
	if ss.Certificate != nil {
		c := *ss.Certificate
		c.Signature = append([]byte(nil), ss.Certificate.Signature...)
		out.Certificate = &c
	}
	return out
}

func cloneProvenance(p *Provenance) *Provenance {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

package impact

import (
	"fmt"
	"strings"
)

// SummaryKey is the exact provenance identity of one function summary. It is
// deliberately not a bare symbol name: two objects may export the same name
// and must never be confused with one another.
type SummaryKey string

// SiteID identifies a call, registration, or return site. Sites are unique
// within a summary and are used to keep callback returns paired with the
// registration site that created the handle.
type SiteID string

// HandleID identifies one registration handle. Distinct registrations use
// distinct handles even when they register the same callback and object.
type HandleID string

// Provenance is the signed origin identity of a summary. Module, Version,
// Package, Object, Symbol, and BuildID participate in the exact key.
type Provenance struct {
	Module  string `json:"module,omitempty"`
	Version string `json:"version,omitempty"`
	Package string `json:"package,omitempty"`
	Object  string `json:"object,omitempty"`
	Symbol  string `json:"symbol,omitempty"`
	BuildID string `json:"build_id,omitempty"`
}

// ObjectID identifies the object file a provenance belongs to. Revocation is
// object-granular because a patch may replace only one object file.
type ObjectID struct {
	Module  string `json:"module,omitempty"`
	Version string `json:"version,omitempty"`
	Package string `json:"package,omitempty"`
	Object  string `json:"object,omitempty"`
	BuildID string `json:"build_id,omitempty"`
}

// Key encodes p without delimiter ambiguity.
func (p Provenance) Key() SummaryKey {
	return SummaryKey(joinKey(p.Module, p.Version, p.Package, p.Object, p.Symbol, p.BuildID))
}

// ObjectID returns the object-level identity of p.
func (p Provenance) ObjectID() ObjectID {
	return ObjectID{
		Module:  p.Module,
		Version: p.Version,
		Package: p.Package,
		Object:  p.Object,
		BuildID: p.BuildID,
	}
}

// IsZero reports whether p is the zero value.
func (p Provenance) IsZero() bool {
	return p == Provenance{}
}

// String renders a readable identity.
func (p Provenance) String() string {
	var b strings.Builder
	if p.Module != "" {
		b.WriteString(p.Module)
		if p.Version != "" {
			b.WriteByte('@')
			b.WriteString(p.Version)
		}
		b.WriteByte('/')
	}
	if p.Package != "" {
		b.WriteString(p.Package)
		b.WriteByte('/')
	}
	if p.Object != "" {
		b.WriteString(p.Object)
		b.WriteByte('#')
	}
	b.WriteString(p.Symbol)
	if p.BuildID != "" {
		b.WriteByte('+')
		b.WriteString(p.BuildID)
	}
	return b.String()
}

// ObjectID returns the key of o.
func (o ObjectID) Key() string {
	return joinKey(o.Module, o.Version, o.Package, o.Object, o.BuildID)
}

// IsZero reports whether o is the zero value.
func (o ObjectID) IsZero() bool { return o == ObjectID{} }

// String renders a readable object identity.
func (o ObjectID) String() string {
	var b strings.Builder
	if o.Module != "" {
		b.WriteString(o.Module)
		if o.Version != "" {
			b.WriteByte('@')
			b.WriteString(o.Version)
		}
		b.WriteByte('/')
	}
	if o.Package != "" {
		b.WriteString(o.Package)
		b.WriteByte('/')
	}
	b.WriteString(o.Object)
	if o.BuildID != "" {
		b.WriteByte('+')
		b.WriteString(o.BuildID)
	}
	return b.String()
}

func joinKey(parts ...string) string {
	var b strings.Builder
	for _, part := range parts {
		fmt.Fprintf(&b, "%d:%s", len(part), part)
	}
	return b.String()
}

// SymbolRef is a name resolved through the snapshot's binding table. A name
// never identifies a summary by itself.
type SymbolRef struct {
	Name string `json:"name"`
}

// EventKind distinguishes the operations in a flat call summary.
type EventKind uint8

const (
	// EventCall performs a normal interprocedural call and resumes at the next
	// event when the callee returns.
	EventCall EventKind = iota + 1
	// EventReturn returns from the current summary.
	EventReturn
	// EventRegister installs a callback handle. The handle is bound to Origin's
	// object provenance, and callback execution resumes at the next event
	// (the continuation of the registration call site).
	EventRegister
	// EventUnregister removes one specific handle.
	EventUnregister
)

// String renders the event kind.
func (k EventKind) String() string {
	switch k {
	case EventCall:
		return "call"
	case EventReturn:
		return "return"
	case EventRegister:
		return "register"
	case EventUnregister:
		return "unregister"
	default:
		return fmt.Sprintf("event(%d)", uint8(k))
	}
}

// Event is one guarded step in a summary body. An event with an uninitialized
// Guard is treated as unconditionally enabled.
type Event struct {
	Kind   EventKind   `json:"kind"`
	Site   SiteID      `json:"site,omitempty"`
	Guard  BDD         `json:"-"`
	Target SymbolRef   `json:"target,omitempty"`
	Handle HandleID    `json:"handle,omitempty"`
	Origin *Provenance `json:"origin,omitempty"`
	// ReturnTo is the paired return site. For Register it names the
	// registration site and must equal Site when set. For Call it optionally
	// names the expected return site in the callee.
	ReturnTo SiteID `json:"return_to,omitempty"`
}

// OptionalSymbol declares a symbol exported only under a configuration
// condition. Multiple declarations for one name are alternatives (OR).
type OptionalSymbol struct {
	Name  string `json:"name"`
	Guard BDD    `json:"-"`
}

// Summary is the signed, flat call summary of one function.
type Summary struct {
	Provenance      Provenance       `json:"provenance"`
	Body            []Event          `json:"body,omitempty"`
	OptionalSymbols []OptionalSymbol `json:"optional_symbols,omitempty"`
}

// Key returns the exact provenance key.
func (s Summary) Key() SummaryKey { return s.Provenance.Key() }

// Validate checks structural invariants. It does not require target summaries
// to exist; missing summaries are analysis evidence, not malformed input.
func (s Summary) Validate(m *Manager) error {
	if m == nil {
		return fmt.Errorf("impact: summary %q has no manager", s.Provenance)
	}
	if s.Provenance.Object == "" {
		return fmt.Errorf("impact: summary %q has no object identity", s.Provenance)
	}
	if s.Provenance.Symbol == "" {
		return fmt.Errorf("impact: summary %q has no symbol identity", s.Provenance)
	}
	type siteUse struct {
		kind EventKind
	}
	seenSites := make(map[SiteID]siteUse)
	handleOrigins := make(map[HandleID]string)
	for i, e := range s.Body {
		where := fmt.Sprintf("%s body[%d]", s.Provenance, i)
		if e.Kind < EventCall || e.Kind > EventUnregister {
			return fmt.Errorf("impact: %s has invalid kind %d", where, e.Kind)
		}
		if e.Guard.m != nil {
			if e.Guard.m != m {
				return fmt.Errorf("impact: %s guard uses a different manager", where)
			}
			if err := e.Guard.IsCanonical(); err != nil {
				return fmt.Errorf("impact: %s guard: %w", where, err)
			}
		}
		if e.Site != "" {
			if previous, ok := seenSites[e.Site]; ok {
				// A callback registration site may be executed repeatedly with
				// distinct handles. Other duplicate sites are ambiguous.
				if !(previous.kind == EventRegister && e.Kind == EventRegister) {
					return fmt.Errorf("impact: %s has duplicate site %q", where, e.Site)
				}
			}
			seenSites[e.Site] = siteUse{kind: e.Kind}
		}
		switch e.Kind {
		case EventCall:
			if e.Site == "" {
				return fmt.Errorf("impact: %s call has no site", where)
			}
			if e.Target.Name == "" {
				return fmt.Errorf("impact: %s call has no target symbol", where)
			}
		case EventReturn:
			// Return sites are optional; a summary may have several guarded
			// returns with distinct site labels.
		case EventRegister:
			if e.Site == "" {
				return fmt.Errorf("impact: %s registration has no site", where)
			}
			if e.Handle == "" {
				return fmt.Errorf("impact: %s registration has no handle", where)
			}
			if e.Target.Name == "" && e.Origin == nil {
				return fmt.Errorf("impact: %s registration has neither target symbol nor origin", where)
			}
			if e.ReturnTo != "" && e.ReturnTo != e.Site {
				return fmt.Errorf("impact: %s callback returns to %q, not registration site %q", where, e.ReturnTo, e.Site)
			}
			sig := ""
			if e.Origin != nil {
				if e.Origin.IsZero() {
					return fmt.Errorf("impact: %s registration has a zero origin", where)
				}
				sig = string(e.Origin.Key()) + "|" + e.Target.Name
			} else {
				sig = "|" + e.Target.Name
			}
			if prev, ok := handleOrigins[e.Handle]; ok && prev != sig {
				return fmt.Errorf("impact: %s reuses handle %q with a different origin", where, e.Handle)
			}
			handleOrigins[e.Handle] = sig
		case EventUnregister:
			if e.Handle == "" {
				return fmt.Errorf("impact: %s unregister has no handle", where)
			}
		}
	}
	for i, sym := range s.OptionalSymbols {
		where := fmt.Sprintf("%s optional_symbols[%d]", s.Provenance, i)
		if sym.Name == "" {
			return fmt.Errorf("impact: %s has no name", where)
		}
		if sym.Guard.m == nil {
			// A zero guard means the optional symbol is always available.
			continue
		}
		if sym.Guard.m != m {
			return fmt.Errorf("impact: %s guard uses a different manager", where)
		}
		if err := sym.Guard.IsCanonical(); err != nil {
			return fmt.Errorf("impact: %s guard: %w", where, err)
		}
	}
	return nil
}

// effectiveGuard returns true when the guard is the zero value.
func effectiveGuard(m *Manager, g BDD) BDD {
	if g.m == nil {
		return m.True()
	}
	return g
}

// Binding resolves a symbol name to an exact summary key. A binding may be
// weak; a strong binding wins even when a weak one is available. Guards are
// evaluated in the current configuration.
type Binding struct {
	Symbol     string     `json:"symbol,omitempty"`
	Target     SummaryKey `json:"target,omitempty"`
	Provenance Provenance `json:"provenance,omitempty"`
	Object     ObjectID   `json:"object,omitempty"`
	Guard      BDD        `json:"-"`
	Weak       bool       `json:"weak,omitempty"`
}

// EffectiveTarget returns the explicit target, or the provenance key when the
// target field is omitted.
func (b Binding) EffectiveTarget() SummaryKey {
	if b.Target != "" {
		return b.Target
	}
	return b.Provenance.Key()
}

// EffectiveObject returns the explicit object, or the provenance object when
// the object field is omitted.
func (b Binding) EffectiveObject() ObjectID {
	if !b.Object.IsZero() {
		return b.Object
	}
	if !b.Provenance.IsZero() {
		return b.Provenance.ObjectID()
	}
	if provenance, ok := parseProvenanceKey(b.Target); ok {
		return provenance.ObjectID()
	}
	return ObjectID{}
}

// Validate checks one binding.
func (b Binding) Validate(m *Manager, symbol string, index int) error {
	where := fmt.Sprintf("binding %q[%d]", symbol, index)
	if b.EffectiveTarget() == (Provenance{}).Key() {
		return fmt.Errorf("impact: %s has no target", where)
	}
	if b.Guard.m != nil {
		if b.Guard.m != m {
			return fmt.Errorf("impact: %s guard uses a different manager", where)
		}
		if err := b.Guard.IsCanonical(); err != nil {
			return fmt.Errorf("impact: %s guard: %w", where, err)
		}
	}
	if !b.Provenance.IsZero() && b.Target != "" && b.Target != b.Provenance.Key() {
		return fmt.Errorf("impact: %s target %q does not match provenance key", where, b.Target)
	}
	if !b.Provenance.IsZero() && !b.Object.IsZero() && b.Provenance.ObjectID() != b.Object {
		return fmt.Errorf("impact: %s object does not match provenance object", where)
	}
	return nil
}

// Result is the per-entry impact result. Both BDDs are canonical diagrams in
// the snapshot manager and are subsets of the snapshot's Valid diagram.
//
// Affected contains configurations with a fully evidenced, feasible path from
// the entry to an object whose revocation is in effect. Unknown contains
// configurations that are not already Affected and for which a feasible path
// reaches missing evidence (missing or invalid signature, missing summary, or
// an unresolved/ambiguous symbol binding). The two sets are disjoint.
type Result struct {
	Entry    SummaryKey `json:"entry"`
	Affected BDD        `json:"-"`
	Unknown  BDD        `json:"-"`
}

// MustRevoke returns Affected | Unknown, the configurations for which the
// release service must revoke the entry's signed artifact.
func (r Result) MustRevoke() BDD {
	if !r.Affected.Valid() {
		return r.Unknown
	}
	if !r.Unknown.Valid() {
		return r.Affected
	}
	return r.Affected.Or(r.Unknown)
}

// Analysis is an immutable set of per-entry results.
type Analysis struct {
	manager *Manager
	valid   BDD
	results []Result
	byEntry map[SummaryKey]Result
}

// Manager returns the manager that owns every result BDD.
func (a *Analysis) Manager() *Manager { return a.manager }

// Valid returns the common valid-configuration constraint.
func (a *Analysis) Valid() BDD { return a.valid }

// Entries returns results in the snapshot's entry order.
func (a *Analysis) Entries() []Result {
	return append([]Result(nil), a.results...)
}

// EntryKeys returns the entry keys in snapshot order.
func (a *Analysis) EntryKeys() []SummaryKey {
	out := make([]SummaryKey, 0, len(a.results))
	for _, result := range a.results {
		out = append(out, result.Entry)
	}
	return out
}

// Result returns the result for entry.
func (a *Analysis) Result(entry SummaryKey) (Result, bool) {
	r, ok := a.byEntry[entry]
	return r, ok
}

// Results returns a copy of the entry-to-result map.
func (a *Analysis) Results() map[SummaryKey]Result {
	out := make(map[SummaryKey]Result, len(a.byEntry))
	for k, v := range a.byEntry {
		out[k] = v
	}
	return out
}

// MustRevoke returns the revocation set for one entry.
func (a *Analysis) MustRevoke(entry SummaryKey) (BDD, bool) {
	r, ok := a.byEntry[entry]
	if !ok {
		return BDD{}, false
	}
	return r.MustRevoke(), true
}

// RevokedEntries returns entries whose MustRevoke set is not empty, in snapshot
// entry order.
func (a *Analysis) RevokedEntries() []SummaryKey {
	var out []SummaryKey
	for _, result := range a.results {
		if !result.MustRevoke().IsFalse() {
			out = append(out, result.Entry)
		}
	}
	return out
}

// AffectedMap returns entry -> Affected in snapshot order.
func (a *Analysis) AffectedMap() map[SummaryKey]BDD {
	out := make(map[SummaryKey]BDD, len(a.results))
	for _, result := range a.results {
		out[result.Entry] = result.Affected
	}
	return out
}

// UnknownMap returns entry -> Unknown in snapshot order.
func (a *Analysis) UnknownMap() map[SummaryKey]BDD {
	out := make(map[SummaryKey]BDD, len(a.results))
	for _, result := range a.results {
		out[result.Entry] = result.Unknown
	}
	return out
}

// MustRevokeMap returns entry -> Affected|Unknown in snapshot order.
func (a *Analysis) MustRevokeMap() map[SummaryKey]BDD {
	out := make(map[SummaryKey]BDD, len(a.results))
	for _, result := range a.results {
		out[result.Entry] = result.MustRevoke()
	}
	return out
}

// IsEmpty reports whether both result sets are empty.
func (r Result) IsEmpty() bool {
	return r.MustRevoke().IsFalse()
}

// RequiresRevocation reports whether at least one legal configuration needs
// the entry's signed artifact revoked.
func (r Result) RequiresRevocation() bool {
	return !r.IsEmpty()
}

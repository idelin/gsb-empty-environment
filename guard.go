package netsec

import (
	"bytes"
	"encoding/xml"
	"errors"
)

// Sentinel errors returned by the guard. Callers can use errors.Is to match
// the specific policy violation.
var (
	// ErrNoProlog is returned for input that is empty or whose prolog cannot
	// be recognized before the document element.
	ErrNoProlog = errors.New("netsec: malformed XML prolog")

	// ErrMalformedDTD is returned for a DOCTYPE whose internal subset does
	// not conform to the grammar the guard is prepared to validate.
	ErrMalformedDTD = errors.New("netsec: malformed DOCTYPE declaration")

	// ErrExternalEntity is returned whenever a SYSTEM or PUBLIC external
	// identifier appears in the DOCTYPE.
	ErrExternalEntity = errors.New("netsec: external entity (SYSTEM/PUBLIC) is not permitted")

	// ErrParameterEntity is returned for any parameter entity declaration or
	// parameter entity reference.
	ErrParameterEntity = errors.New("netsec: parameter entities are not permitted")

	// ErrRecursiveEntity is returned when an entity references itself, directly
	// or indirectly.
	ErrRecursiveEntity = errors.New("netsec: recursive entity reference")

	// ErrUndeclaredEntity is returned when an internal entity value references
	// an entity name that has not been declared in the subset.
	ErrUndeclaredEntity = errors.New("netsec: reference to undeclared entity")

	// ErrEntityLimit is returned once the accumulated size of fully expanded
	// internal entities exceeds MaxEntityExpansion.
	ErrEntityLimit = errors.New("netsec: internal entity expansion limit exceeded")

	// ErrUnsupportedMarkup is returned for markup declarations in the internal
	// subset that the guard does not explicitly allow (for example NOTATION).
	ErrUnsupportedMarkup = errors.New("netsec: unsupported DOCTYPE markup declaration")
)

// EntityResolver is an optional, caller-supplied lookup for entity names a
// document does not declare itself. The guard only consults it while
// expanding internal entities, never for character references, and never at
// all when the document has no DOCTYPE.
type EntityResolver func(name string) (string, bool)

// Guard is the embeddable DOCTYPE policy. Create one with New.
type Guard struct {
	// MaxEntityExpansion bounds the total number of bytes produced while
	// expanding internal entities, charging every repeated occurrence of a
	// referenced entity so amplification attacks stay bounded. A
	// non-positive limit rejects any document that declares internal
	// entities; documents without a DOCTYPE still pass.
	MaxEntityExpansion int

	// ResolveEntity is an optional fallback consulted only while expanding
	// an internal entity that references a name absent from the document's
	// own declarations. Check never invokes it for character references,
	// and a document without a DOCTYPE never triggers it at all. Its output
	// is charged against MaxEntityExpansion like any other expansion.
	ResolveEntity EntityResolver

	lastEntities map[string]entityInfo
}

// New returns a Guard whose internal entities may expand to at most
// maxExpansion bytes in total.
func New(maxExpansion int, resolver EntityResolver) *Guard {
	return &Guard{
		MaxEntityExpansion: maxExpansion,
		ResolveEntity:      resolver,
	}
}

// Validate is a convenience wrapper for New(maxExpansion, resolver).Check.
func Validate(data []byte, maxExpansion int, resolver EntityResolver) error {
	return New(maxExpansion, resolver).Check(data)
}

// Check scans the XML prolog and DOCTYPE internal subset of data. A document
// without a DOCTYPE is accepted unchanged. The input slice is never
// modified.
func (g *Guard) Check(data []byte) error {
	g.lastEntities = nil
	s := &scanner{
		data:     data,
		guard:    g,
		entities: make(map[string]entityInfo),
	}
	return s.scanProlog()
}

// Entities returns the internal general entity table collected during the
// most recent Check. Values are fully expanded, hence contain no markup or
// entity references and are safe to hand to encoding/xml.Decoder.Entity.
// It returns nil for documents without a DOCTYPE or before a successful
// Check.
func (g *Guard) Entities() map[string]string {
	if len(g.lastEntities) == 0 {
		return nil
	}
	out := make(map[string]string, len(g.lastEntities))
	for name, info := range g.lastEntities {
		out[name] = info.expanded
	}
	return out
}

// NewDecoder performs a successful Check and returns a strict
// encoding/xml decoder configured with the bounded internal entity table.
// No custom entity resolution occurs: every reference resolves against the
// bounded, fully expanded table, so an external resolver is never needed.
func (g *Guard) NewDecoder(data []byte) (*xml.Decoder, error) {
	if err := g.Check(data); err != nil {
		return nil, err
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	if entities := g.Entities(); len(entities) > 0 {
		dec.Entity = entities
	}
	return dec, nil
}

type entityInfo struct {
	// raw is the unexpanded quoted literal as declared.
	raw string
	// expanded is populated once the whole subset has been collected, so
	// forward references and cycles can be resolved deterministically.
	expanded string
}

type scanner struct {
	data     []byte
	pos      int
	guard    *Guard
	entities map[string]entityInfo
	charged  int
}

func (s *scanner) fail(cause error) error {
	return &PositionError{Offset: s.pos, Err: cause}
}

// PositionError annotates a policy violation with the byte offset at which
// it was detected.
type PositionError struct {
	Offset int
	Err    error
}

func (e *PositionError) Error() string {
	return e.Err.Error()
}

func (e *PositionError) Unwrap() error {
	return e.Err
}

func (s *scanner) charge(n int) error {
	s.charged += n
	if s.charged > s.guard.MaxEntityExpansion {
		return s.fail(ErrEntityLimit)
	}
	return nil
}

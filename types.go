package netsec

import (
	"errors"
	"sort"
	"strings"
)

const (
	// MaxDepth is the maximum number of role-chain hops permitted by the model.
	MaxDepth = 4

	// Default byte limits.  They are deliberately generous enough for ordinary
	// identity labels, while still bounding attacker-controlled input.
	MaxKeyBytes    = 128
	MaxValueBytes  = 2048
	MaxRoleIDBytes = 128
)

var (
	ErrEmptyKey       = errors.New("netsec: empty key")
	ErrEmptyValue     = errors.New("netsec: empty value")
	ErrInvalidUTF8    = errors.New("netsec: invalid UTF-8")
	ErrKeyTooLong     = errors.New("netsec: key exceeds byte limit")
	ErrValueTooLong   = errors.New("netsec: value exceeds byte limit")
	ErrRoleIDTooLong  = errors.New("netsec: role ID exceeds byte limit")
	ErrDuplicateKey   = errors.New("netsec: duplicate key from one source")
	ErrFixedConflict  = errors.New("netsec: fixed-label conflict")
	ErrNotOverridable = errors.New("netsec: requested key is not caller-overridable")
	ErrFixedOverride  = errors.New("netsec: fixed label cannot be overridden")
	ErrTransitiveUp   = errors.New("netsec: illegal transitive upgrade")
	ErrTransitiveGone = errors.New("netsec: transitive key is absent from result")
	ErrDepthExceeded  = errors.New("netsec: chain depth exceeds limit")
	ErrRoleCycle      = errors.New("netsec: role ID repeats in the chain")
	ErrInitialAllow   = errors.New("netsec: initial policy already allows access")
	ErrNoEscalation   = errors.New("netsec: no label-escalation witness found")
	ErrLabelConflict  = errors.New("netsec: conflicting labels from two chains")
	ErrInvalidInput   = errors.New("netsec: invalid input")

	ErrFixedLabelConflict = ErrFixedConflict
	ErrTransitiveUpgrade  = ErrTransitiveUp
	ErrDepthLimit         = ErrDepthExceeded
	ErrDuplicate          = ErrDuplicateKey
)

// Limits controls the byte limits applied to attacker-controlled strings.
// A zero field means that the corresponding package default is used.
type Limits struct {
	MaxKeyBytes    int
	MaxValueBytes  int
	MaxRoleIDBytes int
}

var DefaultLimits = Limits{
	MaxKeyBytes:    MaxKeyBytes,
	MaxValueBytes:  MaxValueBytes,
	MaxRoleIDBytes: MaxRoleIDBytes,
}

func (l Limits) normalized() Limits {
	if l.MaxKeyBytes <= 0 {
		l.MaxKeyBytes = MaxKeyBytes
	}
	if l.MaxValueBytes <= 0 {
		l.MaxValueBytes = MaxValueBytes
	}
	if l.MaxRoleIDBytes <= 0 {
		l.MaxRoleIDBytes = MaxRoleIDBytes
	}
	return l
}

// Pair is used by callers that need duplicate-key detection before a map is
// built.  A map cannot represent two entries with the same key, while a
// sequence can.
type Pair struct {
	Key   string
	Value string
}

// OriginKind describes how a label entered a chain.
type OriginKind string

const (
	OriginInitial    OriginKind = "initial"
	OriginInherited  OriginKind = "inherited"
	OriginFixed      OriginKind = "fixed"
	OriginNew        OriginKind = "new"
	OriginOverride   OriginKind = "override"
	OriginTransitive OriginKind = "transitive"
	OriginMerge      OriginKind = "merge"
)

// Origin is one immutable step in a label's provenance chain.
type Origin struct {
	RoleID string     `json:"role_id"`
	Hop    int        `json:"hop"`
	Kind   OriginKind `json:"kind"`
	Value  string     `json:"value"`
}

// Proof is the per-hop provenance proof for one label.
type Proof struct {
	Key        string   `json:"key"`
	Value      string   `json:"value"`
	Fixed      bool     `json:"fixed"`
	Transitive bool     `json:"transitive"`
	Origins    []Origin `json:"origins"`
	// Digest binds the proof to its exact values and provenance.  It is a
	// SHA-256 digest in lower-case hexadecimal form.
	Digest string `json:"digest"`
}

// Label is a trusted, provenance-bearing label.  Values are exact byte
// strings; key comparison is exact and never case-folded.
type Label struct {
	Key        string   `json:"key"`
	Value      string   `json:"value"`
	Fixed      bool     `json:"fixed"`
	Transitive bool     `json:"transitive"`
	Origins    []Origin `json:"origins"`
}

func (l Label) clone() Label {
	l.Origins = append([]Origin(nil), l.Origins...)
	return l
}

func (l Label) Clone() Label { return l.clone() }

// RoleChain returns the role IDs recorded in the label's provenance, ordered
// from oldest to newest.
func (l Label) RoleChain() []string {
	out := make([]string, 0, len(l.Origins))
	for _, o := range l.Origins {
		if o.RoleID != "" {
			out = append(out, o.RoleID)
		}
	}
	return out
}

// HopRequest is the complete state transition input for one role hop.
//
// The first block contains the canonical fields.  The remaining fields are
// compatibility spellings; when more than one spelling is present the
// canonical field wins.
type HopRequest struct {
	// CurrentLabels is the current session's label map.
	CurrentLabels map[string]string
	// CurrentTransitive is the set of keys that may be inherited by this hop.
	CurrentTransitive []string
	// RequestedLabels are labels the caller asks the target role to add or
	// override.
	RequestedLabels map[string]string
	// RequestedTransitive is the set of keys the caller asks to keep
	// transitive in the next hop.
	RequestedTransitive []string
	// FixedLabels are labels installed by the target role itself.
	FixedLabels map[string]string
	// OverrideKeys are the only keys the caller may request.
	OverrideKeys []string
	// Depth is the 1-based depth of this hop.  A zero value is inferred from
	// CurrentRoleIDs (or treated as the first hop).
	Depth int
	// RoleID identifies the target role for this hop.
	RoleID string

	// TargetTransitiveKeys optionally narrows which newly added labels the
	// target permits to be transitive.  A nil slice means "all newly added
	// caller-overridable labels".  A non-nil empty slice means none.
	TargetTransitiveKeys []string
	// FixedTransitiveKeys is reserved target metadata.  The secure model never
	// marks a fixed label transitive: callers cannot upgrade fixed labels.
	FixedTransitiveKeys []string

	// CurrentLabelSet preserves provenance from a previous hop.  When set it
	// takes precedence over CurrentLabels.
	CurrentLabelSet map[string]Label
	// CurrentRoleIDs is the ordered role chain already traversed.  It is used
	// for cycle detection when no provenance is supplied.
	CurrentRoleIDs []string
	// NonTransitiveKeys are keys observed as non-transitive in an earlier
	// hop.  They remain tainted even after being dropped, so a caller cannot
	// remove and re-add a key to make it transitive later.
	NonTransitiveKeys []string
	TaintedKeys       []string

	// Set forms of the above, convenient when the caller already has sets.
	CurrentTransitiveSet   map[string]bool
	RequestedTransitiveSet map[string]bool
	OverridableSet         map[string]bool
	TargetTransitiveSet    map[string]bool
	FixedTransitiveSet     map[string]bool
	NonTransitiveSet       map[string]bool
	TaintedSet             map[string]bool

	// Duplicate-aware sequence forms.  If non-nil they take precedence over
	// the corresponding map and permit same-source duplicate detection.
	CurrentPairs   []Pair
	RequestedPairs []Pair
	FixedPairs     []Pair

	// Compatibility aliases.
	Current                 map[string]string
	Labels                  map[string]string
	Transitive              []string
	NewLabels               map[string]string
	Requested               map[string]string
	RequestedLabelMap       map[string]string
	RequestedTransitiveKeys []string
	Fixed                   map[string]string
	TargetFixedLabels       map[string]string
	AllowedOverrideKeys     []string
	TargetOverrideKeys      []string
	OverridableKeys         []string
	ChainDepth              int
	Role                    string
	RoleId                  string
	AllowedTransitiveKeys   []string
	TargetAllowedTransitive []string
}

// Common aliases for the request type.
type HopInput = HopRequest
type HopSpec = HopRequest
type Request = HopRequest

// HopResult is the secure-model output for one hop.
type HopResult struct {
	Labels        map[string]Label
	Values        map[string]string
	Transitive    []string
	TransitiveSet map[string]bool
	NonTransitive []string
	TaintedKeys   []string
	Depth         int
	RoleIDs       []string
	Proofs        map[string]Proof
	Session       *Session
}

// Result is an alias kept for callers that prefer the shorter name.
type Result = HopResult

func (r *HopResult) LabelMap() map[string]string { return r.Values }

func (r *HopResult) View() LabelView { return NewLabelView(r.Values) }

func (r *HopResult) TransitiveKeys() []string { return append([]string(nil), r.Transitive...) }

// Session is a complete chain state.  Labels carry provenance and
// Transitive is the exact set of keys eligible for inheritance by the next
// hop.
type Session struct {
	Labels        map[string]Label
	Transitive    map[string]bool
	NonTransitive map[string]bool
	Depth         int
	RoleIDs       []string
	Proofs        map[string]Proof
}

// NewSession creates an initial session.  Initial labels are non-transitive
// unless their keys are passed in transitive.
func NewSession(labels map[string]string, transitive ...string) *Session {
	s := &Session{
		Labels:        make(map[string]Label, len(labels)),
		Transitive:    make(map[string]bool, len(transitive)),
		NonTransitive: make(map[string]bool, len(labels)),
		Proofs:        make(map[string]Proof, len(labels)),
	}
	keys := sortedKeys(labels)
	for _, k := range keys {
		orig := []Origin{{RoleID: "", Hop: 0, Kind: OriginInitial, Value: labels[k]}}
		s.Labels[k] = Label{Key: k, Value: labels[k], Origins: orig}
	}
	for _, k := range transitive {
		s.Transitive[k] = true
	}
	for _, l := range s.Labels {
		l.Transitive = s.Transitive[l.Key]
		if !l.Transitive {
			s.NonTransitive[l.Key] = true
		}
		s.Labels[l.Key] = l
		s.Proofs[l.Key] = makeProof(l)
	}
	return s
}

// NewSessionFromLabels preserves existing provenance.
func NewSessionFromLabels(labels map[string]Label, transitive []string, depth int, roleIDs []string) *Session {
	s := &Session{
		Labels:        cloneLabelMap(labels),
		Transitive:    make(map[string]bool, len(transitive)),
		NonTransitive: make(map[string]bool, len(labels)),
		Depth:         depth,
		RoleIDs:       append([]string(nil), roleIDs...),
		Proofs:        make(map[string]Proof, len(labels)),
	}
	for _, k := range transitive {
		s.Transitive[k] = true
	}
	for k, l := range s.Labels {
		if l.Transitive {
			s.Transitive[k] = true
		}
	}
	for k, l := range s.Labels {
		l.Key = k
		l.Transitive = s.Transitive[k]
		if !l.Transitive {
			s.NonTransitive[k] = true
		}
		s.Labels[k] = l
	}
	for k, l := range s.Labels {
		s.Proofs[k] = makeProof(l)
	}
	return s
}

func (s *Session) Clone() *Session {
	if s == nil {
		return nil
	}
	out := &Session{
		Labels:        cloneLabelMap(s.Labels),
		Transitive:    make(map[string]bool, len(s.Transitive)),
		NonTransitive: make(map[string]bool, len(s.NonTransitive)),
		Depth:         s.Depth,
		RoleIDs:       append([]string(nil), s.RoleIDs...),
		Proofs:        make(map[string]Proof, len(s.Proofs)),
	}
	for k, v := range s.Transitive {
		out.Transitive[k] = v
	}
	for k, v := range s.NonTransitive {
		out.NonTransitive[k] = v
	}
	for k, v := range s.Proofs {
		out.Proofs[k] = cloneProof(v)
	}
	return out
}

func (s *Session) Values() map[string]string {
	out := make(map[string]string, len(s.Labels))
	for k, l := range s.Labels {
		out[k] = l.Value
	}
	return out
}

func (s *Session) View() LabelView { return NewLabelView(s.Values()) }

func (s *Session) TransitiveKeys() []string {
	out := make([]string, 0, len(s.Transitive))
	for k, v := range s.Transitive {
		if v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Session) NonTransitiveKeys() []string {
	out := make([]string, 0, len(s.NonTransitive))
	for k, v := range s.NonTransitive {
		if v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Request returns a transition request whose current state is this session.
func (s *Session) Request() HopRequest {
	req := HopRequest{
		CurrentLabelSet:      cloneLabelMap(s.Labels),
		CurrentTransitiveSet: cloneBoolMap(s.Transitive),
		CurrentRoleIDs:       append([]string(nil), s.RoleIDs...),
		NonTransitiveSet:     cloneBoolMap(s.NonTransitive),
		Depth:                s.Depth + 1,
	}
	return req
}

// Advance applies one secure hop and returns the next session.
func (s *Session) Advance(req HopRequest) (*Session, error) {
	req.CurrentLabelSet = cloneLabelMap(s.Labels)
	req.CurrentTransitiveSet = cloneBoolMap(s.Transitive)
	req.CurrentLabels = nil
	req.CurrentPairs = nil
	req.CurrentTransitive = nil
	req.CurrentTransitiveSet = cloneBoolMap(s.Transitive)
	req.CurrentRoleIDs = append([]string(nil), s.RoleIDs...)
	req.NonTransitiveKeys = nil
	req.TaintedKeys = nil
	req.NonTransitiveSet = cloneBoolMap(s.NonTransitive)
	req.TaintedSet = nil
	req.Depth = s.Depth + 1
	res, err := ApplyHop(req)
	if err != nil {
		return nil, err
	}
	return res.Session, nil
}

// Apply is an alias for Advance.
func (s *Session) Apply(req HopRequest) (*Session, error) { return s.Advance(req) }

func cloneLabelMap(in map[string]Label) map[string]Label {
	out := make(map[string]Label, len(in))
	for k, v := range in {
		out[k] = v.clone()
	}
	return out
}

func cloneBoolMap(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneProof(p Proof) Proof {
	p.Origins = append([]Origin(nil), p.Origins...)
	return p
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedPairKeys(m map[string]Pair) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// CanonicalKey returns the key unchanged.  It is intentionally not folded,
// trimmed, normalized, or otherwise transformed.
func CanonicalKey(key string) string { return key }

// CanonicalCompare compares keys byte-for-byte and performs no case folding.
func CanonicalCompare(a, b string) int { return strings.Compare(a, b) }

// CanonicalEqual reports exact byte equality.
func CanonicalEqual(a, b string) bool { return a == b }

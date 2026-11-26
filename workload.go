package spiffefed

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Selector is a canonical workload selector in type:value form. Selectors are
// never interpreted as globs.
type Selector string

// NewSelector validates and canonicalizes a selector. Surrounding whitespace
// is rejected rather than trimmed, and wildcard characters are forbidden.
func NewSelector(raw string) (Selector, error) {
	if raw == "" {
		return "", fmt.Errorf("%w: empty selector", ErrInvalidSelector)
	}
	if len(raw) > 1024 {
		return "", fmt.Errorf("%w: selector exceeds 1024 bytes", ErrInvalidSelector)
	}
	if !isASCII(raw) {
		return "", fmt.Errorf("%w: selector must be ASCII", ErrInvalidSelector)
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c < 0x21 || c > 0x7e {
			return "", fmt.Errorf("%w: selector contains whitespace or control bytes", ErrInvalidSelector)
		}
		switch c {
		case '*', '?', '[', ']', '%', '\\':
			return "", fmt.Errorf("%w: wildcard, encoding, or escape character %q is forbidden", ErrInvalidSelector, c)
		}
	}
	firstColon := strings.IndexByte(raw, ':')
	if firstColon <= 0 || firstColon == len(raw)-1 {
		return "", fmt.Errorf("%w: selector must have a non-empty type and value", ErrInvalidSelector)
	}
	if raw[len(raw)-1] == ':' {
		return "", fmt.Errorf("%w: selector value is empty", ErrInvalidSelector)
	}
	return Selector(raw), nil
}

// String returns the canonical selector text.
func (s Selector) String() string {
	return string(s)
}

// SelectorSet is a validated set with exact, case-sensitive equality.
type SelectorSet struct {
	values map[Selector]struct{}
}

// NewSelectorSet constructs a set and rejects duplicate or non-canonical
// selectors. Rejecting duplicates prevents order-dependent registration
// behavior.
func NewSelectorSet(selectors ...string) (*SelectorSet, error) {
	set := &SelectorSet{values: make(map[Selector]struct{}, len(selectors))}
	for _, raw := range selectors {
		selector, err := NewSelector(raw)
		if err != nil {
			return nil, err
		}
		if _, duplicate := set.values[selector]; duplicate {
			return nil, fmt.Errorf("%w: duplicate selector %q", ErrInvalidSelector, selector)
		}
		set.values[selector] = struct{}{}
	}
	return set, nil
}

// Len returns the selector count.
func (s *SelectorSet) Len() int {
	if s == nil {
		return 0
	}
	return len(s.values)
}

// Values returns the sorted selector strings.
func (s *SelectorSet) Values() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.values))
	for selector := range s.values {
		out = append(out, selector.String())
	}
	sort.Strings(out)
	return out
}

// Equal reports exact set equality. There is no subset or wildcard semantics.
func (s *SelectorSet) Equal(other *SelectorSet) bool {
	if s == nil || other == nil {
		return s == nil && other == nil
	}
	if len(s.values) != len(other.values) {
		return false
	}
	for selector := range s.values {
		if _, ok := other.values[selector]; !ok {
			return false
		}
	}
	return true
}

// WorkloadRegistration is a complete node-process selector registration.
// Both selector sets must match the attestation evidence exactly.
type WorkloadRegistration struct {
	ID               string   `json:"id"`
	NodeSelectors    []string `json:"node_selectors"`
	ProcessSelectors []string `json:"process_selectors"`
}

// NodeAttestation contains node-level evidence.
type NodeAttestation struct {
	NodeID    string   `json:"node_id"`
	Selectors []string `json:"selectors"`
}

// ProcessRecord describes one process in the ancestry chain.
type ProcessRecord struct {
	PID        int    `json:"pid"`
	ParentPID  int    `json:"parent_pid"`
	Namespace  string `json:"namespace"`
	UID        uint32 `json:"uid"`
	Privileged bool   `json:"privileged"`
}

// ProcessAttestation contains the target process evidence. Ancestors must be
// ordered from the immediate parent to the namespace/root ancestor.
type ProcessAttestation struct {
	ProcessRecord
	Selectors []string        `json:"selectors"`
	Ancestors []ProcessRecord `json:"ancestors"`
}

// AttestationEvidence is the complete input to exact registration matching.
type AttestationEvidence struct {
	Node    NodeAttestation    `json:"node"`
	Process ProcessAttestation `json:"process"`
}

type compiledRegistration struct {
	registration WorkloadRegistration
	node         *SelectorSet
	process      *SelectorSet
}

// WorkloadMatcher performs fail-closed, exact selector matching.
type WorkloadMatcher struct {
	registrations []compiledRegistration
}

// NewWorkloadMatcher validates and compiles registrations. Both node and
// process selector sets must be non-empty.
func NewWorkloadMatcher(registrations []WorkloadRegistration) (*WorkloadMatcher, error) {
	if len(registrations) == 0 {
		return nil, fmt.Errorf("%w: no registrations", ErrAttestationDenied)
	}
	matcher := &WorkloadMatcher{registrations: make([]compiledRegistration, 0, len(registrations))}
	seenIDs := make(map[string]struct{}, len(registrations))
	seenPairs := make(map[string]struct{}, len(registrations))

	for i, registration := range registrations {
		if registration.ID != "" {
			if _, duplicate := seenIDs[registration.ID]; duplicate {
				return nil, fmt.Errorf("%w: duplicate registration id %q", ErrAttestationDenied, registration.ID)
			}
			seenIDs[registration.ID] = struct{}{}
		}
		if len(registration.NodeSelectors) == 0 {
			return nil, fmt.Errorf("%w: registration %d has no node selectors", ErrAttestationDenied, i)
		}
		if len(registration.ProcessSelectors) == 0 {
			return nil, fmt.Errorf("%w: registration %d has no process selectors", ErrAttestationDenied, i)
		}
		node, err := NewSelectorSet(registration.NodeSelectors...)
		if err != nil {
			return nil, fmt.Errorf("%w: registration %d node selectors: %w", ErrAttestationDenied, i, err)
		}
		process, err := NewSelectorSet(registration.ProcessSelectors...)
		if err != nil {
			return nil, fmt.Errorf("%w: registration %d process selectors: %w", ErrAttestationDenied, i, err)
		}
		pair := strings.Join(node.Values(), "\x00") + "\x01" + strings.Join(process.Values(), "\x00")
		if _, duplicate := seenPairs[pair]; duplicate {
			return nil, fmt.Errorf("%w: registration selector pair is duplicated", ErrAmbiguousRegistration)
		}
		seenPairs[pair] = struct{}{}

		registration.NodeSelectors = node.Values()
		registration.ProcessSelectors = process.Values()
		matcher.registrations = append(matcher.registrations, compiledRegistration{
			registration: registration,
			node:         node,
			process:      process,
		})
	}
	return matcher, nil
}

// Match returns the uniquely matching registration. Missing selectors, extra
// selectors, namespace mismatches, and ambiguous registrations are rejected.
func (m *WorkloadMatcher) Match(evidence AttestationEvidence) (WorkloadRegistration, error) {
	if m == nil || len(m.registrations) == 0 {
		return WorkloadRegistration{}, fmt.Errorf("%w: matcher is not configured", ErrAttestationDenied)
	}
	node, process, err := compileEvidence(evidence)
	if err != nil {
		return WorkloadRegistration{}, err
	}

	var match *compiledRegistration
	for i := range m.registrations {
		registration := &m.registrations[i]
		if registration.node.Equal(node) && registration.process.Equal(process) {
			if match != nil {
				return WorkloadRegistration{}, fmt.Errorf("%w: registration %q and %q", ErrAmbiguousRegistration, match.registration.ID, registration.registration.ID)
			}
			match = registration
		}
	}
	if match == nil {
		return WorkloadRegistration{}, fmt.Errorf("%w: no registration has exactly these node and process selectors", ErrAttestationDenied)
	}

	result := match.registration
	result.NodeSelectors = append([]string(nil), match.registration.NodeSelectors...)
	result.ProcessSelectors = append([]string(nil), match.registration.ProcessSelectors...)
	return result, nil
}

func compileEvidence(evidence AttestationEvidence) (*SelectorSet, *SelectorSet, error) {
	if len(evidence.Node.Selectors) == 0 {
		return nil, nil, fmt.Errorf("%w: node evidence has no selectors", ErrAttestationDenied)
	}
	if len(evidence.Process.Selectors) == 0 {
		return nil, nil, fmt.Errorf("%w: process evidence has no selectors", ErrAttestationDenied)
	}
	node, err := NewSelectorSet(evidence.Node.Selectors...)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: node evidence: %w", ErrAttestationDenied, err)
	}
	process, err := NewSelectorSet(evidence.Process.Selectors...)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: process evidence: %w", ErrAttestationDenied, err)
	}
	if err := validateProcessLineage(evidence.Process); err != nil {
		return nil, nil, err
	}
	if err := crossCheckProcessSelectors(process, evidence.Process.ProcessRecord); err != nil {
		return nil, nil, err
	}
	return node, process, nil
}

func validateProcessLineage(process ProcessAttestation) error {
	if process.PID <= 0 {
		return fmt.Errorf("%w: process PID must be positive", ErrAttestationDenied)
	}
	if process.ParentPID < 0 {
		return fmt.Errorf("%w: process parent PID is negative", ErrAttestationDenied)
	}
	if process.Namespace == "" {
		return fmt.Errorf("%w: process namespace is required", ErrAttestationDenied)
	}
	if process.Namespace != strings.TrimSpace(process.Namespace) {
		return fmt.Errorf("%w: process namespace contains surrounding whitespace", ErrAttestationDenied)
	}

	if process.PID != 1 && len(process.Ancestors) == 0 {
		return fmt.Errorf("%w: process ancestry evidence is required", ErrAttestationDenied)
	}
	if process.PID == 1 && len(process.Ancestors) != 0 {
		return fmt.Errorf("%w: PID 1 cannot have an in-namespace ancestor chain", ErrAttestationDenied)
	}
	if process.PID == 1 && process.ParentPID != 0 {
		return fmt.Errorf("%w: PID 1 must have parent PID 0", ErrAttestationDenied)
	}

	expectedParent := process.ParentPID
	seen := map[int]struct{}{process.PID: {}}
	for i, ancestor := range process.Ancestors {
		if ancestor.PID <= 0 {
			return fmt.Errorf("%w: ancestor %d PID must be positive", ErrAttestationDenied, i)
		}
		if ancestor.PID != expectedParent {
			return fmt.Errorf("%w: ancestor %d PID %d does not match expected parent PID %d", ErrAttestationDenied, i, ancestor.PID, expectedParent)
		}
		if _, duplicate := seen[ancestor.PID]; duplicate {
			return fmt.Errorf("%w: process ancestry contains a cycle or duplicate PID %d", ErrAttestationDenied, ancestor.PID)
		}
		seen[ancestor.PID] = struct{}{}
		if ancestor.Namespace == "" {
			return fmt.Errorf("%w: ancestor PID %d namespace is required", ErrAttestationDenied, ancestor.PID)
		}
		if ancestor.Privileged && ancestor.Namespace != process.Namespace {
			return fmt.Errorf("%w: privileged ancestor PID %d in namespace %q cannot vouch for workload namespace %q", ErrAttestationDenied, ancestor.PID, ancestor.Namespace, process.Namespace)
		}
		if ancestor.Namespace != process.Namespace {
			return fmt.Errorf("%w: ancestor PID %d is in namespace %q, workload is in %q", ErrAttestationDenied, ancestor.PID, ancestor.Namespace, process.Namespace)
		}
		if ancestor.ParentPID < 0 {
			return fmt.Errorf("%w: ancestor PID %d parent PID is negative", ErrAttestationDenied, ancestor.PID)
		}
		expectedParent = ancestor.ParentPID
	}

	if len(process.Ancestors) > 0 {
		last := process.Ancestors[len(process.Ancestors)-1]
		if last.ParentPID != 0 && last.PID != 1 {
			return fmt.Errorf("%w: ancestry chain is incomplete at parent PID %d", ErrAttestationDenied, last.ParentPID)
		}
	}
	return nil
}

func crossCheckProcessSelectors(selectors *SelectorSet, process ProcessRecord) error {
	for selector := range selectors.values {
		raw := selector.String()
		switch {
		case strings.HasPrefix(raw, "k8s:ns:"):
			claimed := strings.TrimPrefix(raw, "k8s:ns:")
			if claimed != process.Namespace {
				return fmt.Errorf("%w: selector %q does not match attested namespace %q", ErrAttestationDenied, raw, process.Namespace)
			}
		case strings.HasPrefix(raw, "k8s:namespace:"):
			claimed := strings.TrimPrefix(raw, "k8s:namespace:")
			if claimed != process.Namespace {
				return fmt.Errorf("%w: selector %q does not match attested namespace %q", ErrAttestationDenied, raw, process.Namespace)
			}
		case strings.HasPrefix(raw, "unix:uid:"):
			claimed := strings.TrimPrefix(raw, "unix:uid:")
			uid, err := strconv.ParseUint(claimed, 10, 32)
			if err != nil || uint32(uid) != process.UID {
				return fmt.Errorf("%w: selector %q does not match attested UID %d", ErrAttestationDenied, raw, process.UID)
			}
		}
	}
	return nil
}

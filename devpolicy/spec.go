package devpolicy

// SpecEntry is one statement of the target specification policy.
type SpecEntry struct {
	// Allow true contributes a permitted set; false contributes a mandatory deny.
	Allow              bool
	Type               DevType
	MajorMin, MajorMax uint16
	MinorMin, MinorMax uint16
	Perm               uint8
	Name               string
}

// Spec is the target policy: a finite allow list plus mandatory deny sets.
// Mandatory denies win over allows when both mention the same bit.
type Spec struct {
	Entries []SpecEntry
}

// AddAllow appends a permitted (type, rectangle, bits) entry.
func (s *Spec) AddAllow(t DevType, majLo, majHi, minLo, minHi uint16, perm uint8, name string) *Spec {
	s.Entries = append(s.Entries, SpecEntry{
		Allow: true, Type: t,
		MajorMin: majLo, MajorMax: majHi, MinorMin: minLo, MinorMax: minHi,
		Perm: perm, Name: name,
	})
	return s
}

// AddDeny appends a mandatory-deny entry.
func (s *Spec) AddDeny(t DevType, majLo, majHi, minLo, minHi uint16, perm uint8, name string) *Spec {
	s.Entries = append(s.Entries, SpecEntry{
		Allow: false, Type: t,
		MajorMin: majLo, MajorMax: majHi, MinorMin: minLo, MinorMax: minHi,
		Perm: perm, Name: name,
	})
	return s
}

// toRules renders the spec as ordered rules: allows first, every mandatory
// deny applied last so denials cannot be overridden. Each spec entry becomes
// a witness-able rule named after the entry.
func (s *Spec) toRules() ([]Rule, error) {
	var rules []Rule
	order := 0
	for _, e := range s.Entries {
		if e.Allow {
			rules = append(rules, e.toRule(EffectAllow, order))
			order++
		}
	}
	for _, e := range s.Entries {
		if !e.Allow {
			rules = append(rules, e.toRule(EffectDeny, order))
			order++
		}
	}
	for _, r := range rules {
		if err := r.validate(); err != nil {
			return nil, err
		}
	}
	return rules, nil
}

func (e SpecEntry) toRule(effect RuleEffect, order int) Rule {
	return Rule{
		Layer: 0, Order: order, Effect: effect, Type: e.Type,
		MajorMin: e.MajorMin, MajorMax: e.MajorMax,
		MinorMin: e.MinorMin, MinorMax: e.MinorMax,
		Perm: e.Perm, Name: e.Name,
	}
}

// Compile builds the symbolic reference diagram for the specification.
func (s *Spec) Compile() (*CompiledPolicy, error) {
	return s.CompileDomain(MaxDeviceNo)
}

// CompileDomain mirrors Policy.CompileDomain for small-domain testing.
func (s *Spec) CompileDomain(domainMax uint16) (*CompiledPolicy, error) {
	rules, err := s.toRules()
	if err != nil {
		return nil, err
	}
	return (&Policy{Rules: rules}).CompileDomain(domainMax)
}

// Violation is one lexicographically minimal bit-level discrepancy between a
// compiled policy and its target specification.
type Violation struct {
	// Escalation is true for an over-permissive grant (policy allows a bit the
	// spec forbids/never grants); false for an over-tight denial.
	Escalation bool
	Type       DevType
	Major      uint16
	Minor      uint16
	// Bit is one of BitR/BitW/BitM identifying the offending permission.
	Bit uint8
	// Expected/Actual are the spec and policy masks at the device.
	Expected uint8
	Actual   uint8
	// PolicyWitness replays the audited policy's rule chain at this device.
	PolicyWitness Witness
	// SpecWitness replays the specification's rule chain at this device.
	SpecWitness Witness
}

// BitName renders the permission bit as r, w or m.
func BitName(bit uint8) string {
	switch bit {
	case BitR:
		return "r"
	case BitW:
		return "w"
	case BitM:
		return "m"
	default:
		return "?"
	}
}

// AuditReport summarizes a policy-vs-spec symbolic audit.
type AuditReport struct {
	// Equivalent is true when the policy decides exactly the spec bits on
	// every possible device.
	Equivalent bool
	// MinEscalation is the lexicographically smallest over-permissive tuple,
	// or nil when the policy grants nothing beyond the spec.
	MinEscalation *Violation
	// MinOverDeny is the lexicographically smallest over-tight tuple, or nil.
	MinOverDeny *Violation
}

// bitOrder fixes the in-device permutation ordering: r < w < m.
var bitOrder = [3]uint8{BitR, BitW, BitM}

// Audit compares the compiled policy against the spec purely on the symbolic
// partitions. For every disjoint overlay cell it computes the granted and
// denied extra bits, then keeps the lexicographically minimal tuple under
// (type c<b, major, minor, bit r<w<m). No device-number enumeration occurs.
func Audit(policy, spec *CompiledPolicy) (*AuditReport, error) {
	if policy.DomainMax != spec.DomainMax {
		return nil, invalidf("domain mismatch: policy %d vs spec %d", policy.DomainMax, spec.DomainMax)
	}
	report := &AuditReport{Equivalent: true}

	for idx, t := range orderedTypes() {
		cellPairs := overlayPairsDomain(
			policy.Effective[idx].Partition,
			spec.Effective[idx].Partition,
			policy.DomainMax,
		)
		for _, pair := range cellPairs {
			actual, expected := pair.actualMask, pair.expectedMask
			extra := actual &^ expected   // bits granted beyond the spec
			missing := expected &^ actual // spec bits the policy refuses
			if extra != 0 {
				report.Equivalent = false
				if v := minimalViolation(pair.Rect, t, extra, true, expected, actual, policy, spec); report.MinEscalation == nil || v.less(report.MinEscalation) {
					report.MinEscalation = v
				}
			}
			if missing != 0 {
				report.Equivalent = false
				if v := minimalViolation(pair.Rect, t, missing, false, expected, actual, policy, spec); report.MinOverDeny == nil || v.less(report.MinOverDeny) {
					report.MinOverDeny = v
				}
			}
		}
	}
	return report, nil
}

// maskPair carries both masks for one overlay rectangle.
type maskPair struct {
	Rect
	actualMask   uint8
	expectedMask uint8
}

// overlayPairs refines the two partitions into shared rectangles retaining
// both decisions, mirroring combine but without collapsing the masks.
// overlayPairsDomain builds the shared coordinate-compressed grid for the two
// partitions and emits one maskPair per slab cell, retaining both decisions
// without collapsing them through a mask-combining callback.
func overlayPairsDomain(actual, expected Partition, domainMax uint16) []maskPair {
	rects := make([]Rect, 0, len(actual)+len(expected))
	for _, cell := range actual {
		rects = append(rects, cell.Rect)
	}
	for _, cell := range expected {
		rects = append(rects, cell.Rect)
	}
	edges := collectBounds(rects, domainMax)
	slabs := edges.slabs()
	var out []maskPair
	for _, maj := range slabs {
		for _, min := range slabs {
			out = append(out, maskPair{
				Rect:         Rect{MajMin: maj.lo, MajMax: maj.hi, MinMin: min.lo, MinMax: min.hi},
				actualMask:   maskInPartition(actual, maj.lo, min.lo),
				expectedMask: maskInPartition(expected, maj.lo, min.lo),
			})
		}
	}
	return out
}

// minimalViolation builds the smallest tuple inside one overlay cell for the
// given offending bits. The minimum point of any rectangle is its lower-left
// corner and the minimum bit is the first set bit in r<w<m order.
func minimalViolation(rect Rect, t DevType, bits uint8, escalation bool, expected, actual uint8, policy, spec *CompiledPolicy) *Violation {
	bit := uint8(0)
	for _, candidate := range bitOrder {
		if bits&candidate != 0 {
			bit = candidate
			break
		}
	}
	v := &Violation{
		Escalation:    escalation,
		Type:          t,
		Major:         rect.MajMin,
		Minor:         rect.MinMin,
		Bit:           bit,
		Expected:      expected,
		Actual:        actual,
		PolicyWitness: policy.Trace(t, rect.MajMin, rect.MinMin),
		SpecWitness:   spec.Trace(t, rect.MajMin, rect.MinMin),
	}
	return v
}

// less implements the required total ordering (type c<b, major, minor, bit).
func (v *Violation) less(other *Violation) bool {
	if typeIndex(v.Type) != typeIndex(other.Type) {
		return typeIndex(v.Type) < typeIndex(other.Type)
	}
	if v.Major != other.Major {
		return v.Major < other.Major
	}
	if v.Minor != other.Minor {
		return v.Minor < other.Minor
	}
	return bitRank(v.Bit) < bitRank(other.Bit)
}

// bitRank maps r<w<m to 0<1<2.
func bitRank(bit uint8) int {
	for i, b := range bitOrder {
		if b == bit {
			return i
		}
	}
	return len(bitOrder)
}

package devpolicy

// Delegation models handing a (possibly rewritten) device policy across a
// mount-namespace boundary. A delegation is legal only when it carries an
// explicit non-nil decay mask that can remove rights but never add them.
//
// A nil DecayMask is rejected: cross-namespace delegation without an
// attenuation mask is exactly the mknod/read-write escape channel this
// package is built to catch.
type Delegation struct {
	// Target is the policy the delegate intends to run.
	Target *Policy
	// DecayMask is ANDed with every effective permission bit. Set bits are the
	// maximum permissions that may survive the namespace hand-off.
	DecayMask *uint8
	// RegionMask optionally restricts the decay to rectangles. When nil the
	// single DecayMask applies to the whole plane; otherwise every rectangle
	// must still be a subset of MaskAll and can only attenuate.
	RegionMask []RegionDecay
}

// RegionDecay binds an attenuation mask to one rectangle for scoped
// delegation. Rectangles may overlap; overlapping masks are ANDed.
type RegionDecay struct {
	Rect
	Mask uint8
}

// DelegationReport is the symbolic result of auditing one hand-off.
type DelegationReport struct {
	// Valid is true when the decayed delegate policy is everywhere contained in
	// the granter policy, bit for bit.
	Valid bool
	// Escape is the lexicographically smallest tuple that would gain a bit
	// across the namespace boundary, or nil.
	Escape *Violation
	// Decayed is the compiled effective policy after attenuation.
	Decayed *CompiledPolicy
}

// validate checks the mandatory decay mask and structural legality.
func (d *Delegation) validate() error {
	if d.Target == nil {
		return invalidf("delegation: target policy is nil")
	}
	if d.DecayMask == nil {
		return invalidf("delegation: cross-namespace hand-off requires an explicit decay mask")
	}
	if *d.DecayMask&^MaskAll != 0 {
		return invalidf("delegation: decay mask 0b%b contains unknown bits", *d.DecayMask)
	}
	for i, region := range d.RegionMask {
		if region.Mask&^MaskAll != 0 || region.Mask == 0 {
			return invalidf("delegation region %d: mask 0b%b must be a non-empty rwm subset", i, region.Mask)
		}
		if region.Rect.empty() || region.MajMax > MaxDeviceNo || region.MinMax > MaxDeviceNo {
			return invalidf("delegation region %d: illegal rectangle", i)
		}
	}
	return nil
}

// attenuationDiagram builds, per type, a partition whose mask is the decay
// applied at every point. The global decay mask is the default; region masks
// further AND their rectangles.
func (d *Delegation) attenuationDiagram(t DevType, domainMax uint16) *Diagram {
	base := *d.DecayMask
	part := fullPartition(base)
	for _, region := range d.RegionMask {
		if region.MajMin > domainMax || region.MinMin > domainMax {
			continue
		}
		clipped := region.Rect
		clipped.MajMax = minU16(clipped.MajMax, domainMax)
		clipped.MinMax = minU16(clipped.MinMax, domainMax)
		part = part.applyRule(clipped, EffectDeny, MaskAll&^region.Mask)
	}
	return &Diagram{Type: t, Partition: compact(part)}
}

// AuditDelegation compiles the target policy, applies the mandatory decay
// mask symbolically and verifies the delegate can never hold a bit the
// granter's effective policy withholds. The granter is typically an
// already-compiled nested cgroup policy.
func AuditDelegation(granter *CompiledPolicy, delegation *Delegation) (*DelegationReport, error) {
	if err := delegation.validate(); err != nil {
		return nil, err
	}
	target, err := delegation.Target.CompileDomain(granter.DomainMax)
	if err != nil {
		return nil, err
	}

	decayed := &CompiledPolicy{
		Rules:     target.Rules,
		DomainMax: granter.DomainMax,
	}
	for idx, t := range orderedTypes() {
		decay := delegation.attenuationDiagram(t, granter.DomainMax)
		// Attenuated rights are the delegate's own permissions after the mandatory
		// decay mask. They are audited *before* granter intersection, otherwise
		// intersecting with the very reference we compare against would hide every
		// escape. Decayed separately stores the enforceable intersection.
		decayed.Effective[idx] = intersectDiagramsDomain(target.Effective[idx], decay, granter.DomainMax)
		decayed.Layers[idx] = target.Layers[idx]
	}

	report := &DelegationReport{Valid: true, Decayed: decayed}
	for idx, t := range orderedTypes() {
		attenuated := decayed.Effective[idx]
		pairs := overlayPairsDomain(attenuated.Partition, granter.Effective[idx].Partition, granter.DomainMax)
		for _, pair := range pairs {
			escape := pair.actualMask &^ pair.expectedMask
			if escape == 0 {
				continue
			}
			report.Valid = false
			v := minimalViolation(pair.Rect, t, escape, true, pair.expectedMask, pair.actualMask, target, granter)
			if report.Escape == nil || v.less(report.Escape) {
				report.Escape = v
			}
		}
		// Fold the granter into the enforceable effective policy for callers.
		decayed.Effective[idx] = intersectDiagramsDomain(attenuated, granter.Effective[idx], granter.DomainMax)
	}
	return report, nil
}

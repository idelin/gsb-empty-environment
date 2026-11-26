package devpolicy

// Diff is the lexicographically first device tuple on which two compiled
// policies reach different permission decisions.
type Diff struct {
	Type  DevType
	Major uint16
	Minor uint16
	// Bit is the first (r<w<m) permission bit whose decision differs.
	Bit   uint8
	MaskA uint8
	MaskB uint8
}

// EquivalentPolicies reports whether two already-compiled policies reach the
// same bit-accurate decision on every possible device. The comparison runs
// symbolically over the orthogonal overlay of the two interval diagrams; it
// never iterates the device-number space. On mismatch it returns the
// lexicographically smallest differing tuple under
// (type c<b, major, minor, bit r<w<m).
func EquivalentPolicies(a, b *CompiledPolicy) (bool, *Diff, error) {
	if a.DomainMax != b.DomainMax {
		return false, nil, invalidf("domain mismatch: %d vs %d", a.DomainMax, b.DomainMax)
	}
	var diff *Diff
	for idx, t := range orderedTypes() {
		pairs := overlayPairsDomain(a.Effective[idx].Partition, b.Effective[idx].Partition, a.DomainMax)
		for _, pair := range pairs {
			if pair.actualMask == pair.expectedMask {
				continue
			}
			candidate := &Diff{
				Type:  t,
				Major: pair.Rect.MajMin,
				Minor: pair.Rect.MinMin,
				MaskA: pair.actualMask,
				MaskB: pair.expectedMask,
				Bit:   firstSetBit(pair.actualMask ^ pair.expectedMask),
			}
			if diff == nil || diffLess(candidate, diff) {
				diff = candidate
			}
		}
	}
	if diff != nil {
		return false, diff, nil
	}
	return true, nil, nil
}

// diffLess mirrors the required (type c<b, major, minor, bit r<w<m) order.
func diffLess(a, b *Diff) bool {
	if typeIndex(a.Type) != typeIndex(b.Type) {
		return typeIndex(a.Type) < typeIndex(b.Type)
	}
	if a.Major != b.Major {
		return a.Major < b.Major
	}
	if a.Minor != b.Minor {
		return a.Minor < b.Minor
	}
	return bitRank(a.Bit) < bitRank(b.Bit)
}

// Equivalent is the optimizer review entry point: it compiles the original
// and the candidate (possibly optimizer-rewritten) policies and compares them
// symbolically. A non-nil diff is a concrete optimizer bug witness, returned
// at the lexicographically smallest violating device.
func Equivalent(original, optimized *Policy) (bool, *Diff, error) {
	a, err := original.Compile()
	if err != nil {
		return false, nil, err
	}
	b, err := optimized.Compile()
	if err != nil {
		return false, nil, err
	}
	return EquivalentPolicies(a, b)
}

// EquivalentDomain is Equivalent with an explicit small domain for exhaustive
// cross-checking in tests.
func EquivalentDomain(original, optimized *Policy, domainMax uint16) (bool, *Diff, error) {
	a, err := original.CompileDomain(domainMax)
	if err != nil {
		return false, nil, err
	}
	b, err := optimized.CompileDomain(domainMax)
	if err != nil {
		return false, nil, err
	}
	return EquivalentPolicies(a, b)
}

// firstSetBit returns the lowest-permitted bit in r<w<m order present in mask.
func firstSetBit(mask uint8) uint8 {
	for _, bit := range bitOrder {
		if mask&bit != 0 {
			return bit
		}
	}
	return 0
}

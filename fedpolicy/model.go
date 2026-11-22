package fedpolicy

// Model is a bounded but exhaustive universe of principals. Every path up to
// MaxDepth over Alphabet is enumerated for every authority, which is a
// complete model check within the bound: the automata arguments in
// PROOF.md extend the same conclusions to unbounded paths.
type Model struct {
	Authorities []HeaderKey
	Alphabet    []string
	MaxDepth    int
	Labeler     Labeler
}

// Enumerate returns every principal in the model, including empty paths.
func (m Model) Enumerate() []Name {
	var names []Name
	for _, header := range m.Authorities {
		for _, path := range allPaths(m.Alphabet, m.MaxDepth) {
			n := Name{Issuer: header.Issuer, Tenant: header.Tenant, Path: path}
			if m.Labeler != nil {
				n.Labels = m.Labeler.Labels(n)
			}
			names = append(names, n)
		}
	}
	return names
}

// allPaths returns all words over alphabet of length 0..maxDepth inclusive.
// The empty word is returned first, which makes empty-path behavior explicit
// in tests.
func allPaths(alphabet []string, maxDepth int) [][]string {
	var result [][]string
	result = append(result, nil)
	var grow func(prefix []string, depth int)
	grow = func(prefix []string, depth int) {
		if depth == maxDepth {
			return
		}
		for _, sym := range alphabet {
			next := append(append([]string{}, prefix...), sym)
			result = append(result, next)
			grow(next, depth+1)
		}
	}
	grow(nil, 0)
	return result
}

// VerifySoundness checks that the projection never admits a target identity
// unless some compatible old principal that maps to it is both old-allowed and
// carries the required labels. It returns the first violation found and true
// when the model is fully sound.
func VerifySoundness(report *Report, mapper *Mapper, policy OldPolicy, model Model) (Name, bool) {
	for _, target := range model.Enumerate() {
		if !report.Policy.Allows(target) {
			continue
		}

		// Every selected rule under whose prefix target falls must be
		// independently justified per old preimage: the preimage is old-
		// allowed and carries the rule's required labels. The target-side
		// label view is irrelevant here because the guarantee is about the
		// signed labels of the real old principal.
		for _, rule := range report.Policy.Rules {
			if !rule.Prefix.Matches(target) {
				continue
			}
			for _, ch := range mapper.ChannelsForNew(target.Header()) {
				for _, old := range model.Enumerate() {
					if old.Header() != ch.Old {
						continue
					}
					realOld := Name{
						Issuer: old.Issuer,
						Tenant: old.Tenant,
						Path:   old.Path,
						Labels: model.Labeler.Labels(old),
					}
					for _, img := range ch.Transducer.Images(realOld.Path) {
						if !PathEquals(img.Out, target.Path) {
							continue
						}
						if !policy.Allows(realOld) ||
							!newLabelSet(rule.Require).isSubset(newLabelSet(realOld.Labels)) {
							return target, false
						}
					}
				}
			}
		}
	}
	return Name{}, true
}

// VerifyCoverage checks that every target identity which is representable by
// at least one safe candidate is actually admitted by the minimized policy.
// Missing such an identity would mean the projection is incomplete.
func VerifyCoverage(report *Report, model Model) (Name, bool) {
	for _, target := range model.Enumerate() {
		representable := false
		for _, decision := range report.Decisions {
			if !decision.Safe {
				continue
			}
			if decision.Candidate.Prefix.Matches(target) &&
				newLabelSet(decision.Candidate.Require).isSubset(newLabelSet(target.Labels)) {
				representable = true
				break
			}
		}
		if representable && !report.Policy.Allows(target) {
			return target, false
		}
	}
	return Name{}, true
}

// VerifyWitnesses replays every reject witness against the real old policy
// and mapping machinery.
//
// For an old-deny witness the old principal must be genuinely denied (or miss
// a required label) and its trace must reproduce the target path.
// For a no-preimage witness the trace, when present, is the boundary
// trajectory: it reproduces a strict prefix of the gap target (the point at
// which the image language runs out), while the gap target itself must have
// no preimage at all.
func VerifyWitnesses(report *Report, policy OldPolicy) (Witness, bool) {
	for _, decision := range report.Decisions {
		if decision.Safe {
			continue
		}
		for _, w := range decision.Witnesses {
			if w.Old.Issuer != "" {
				_, effect := policy.MatchedRule(w.Old)
				missing := missingOf(decision.Candidate, w.Old)
				switch decision.Reason {
				case RejectOldDeny:
					if effect == Allow && len(missing) == 0 {
						return w, false
					}
					if !PathEquals(traceOutput(w.Trace), w.Target.Path) {
						return w, false
					}
				case RejectNoPreimage:
					output := traceOutput(w.Trace)
					if !HasPrefix(w.Target.Path, output) {
						return w, false
					}
					if len(output) >= len(w.Target.Path) {
						return w, false
					}
				}
			}
		}
	}
	return Witness{}, true
}

func missingOf(c Candidate, old Name) []Label {
	have := newLabelSet(old.Labels)
	var missing []Label
	for _, l := range c.Require {
		if !have.contains(l) {
			missing = append(missing, l)
		}
	}
	return missing
}

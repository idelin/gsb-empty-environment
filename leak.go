package netsec

import "fmt"

// intInterval is one atomic segment of a finite numeric domain.
// Every value in [Lo, Hi] produces the same condition outcome for the
// cut points that generated it, so Lo alone is a valid representative.
type intInterval struct {
	Lo uint32
	Hi uint32
}

// LeakSpec asks the analyzer to prove or disprove a confidentiality
// violation: a route that initially carries SecretTag must not be
// accepted for any of the ForbiddenNeighbors.
type LeakSpec struct {
	// Entry is the default export policy name run for a neighbor.
	Entry string
	// Neighbors are the peers that must not receive the secret route.
	Neighbors []Neighbor
	// SecretTag must be present on a candidate route to count.
	SecretTag Tag
	// MaxPathLen bounds the path-length domain (default 255).
	MaxPathLen int
	// FullTagDomain forces enumeration of all 2^16 tag sets instead
	// of only the sets built from tags referenced by the policies plus
	// the secret tag. Both are exact over the 16-tag universe.
	FullTagDomain bool
	// RequireSecretRetained additionally demands the secret tag still
	// be present on the exported route. By default a leak is any
	// accept of a route that originally carried the secret.
	RequireSecretRetained bool
}

// Neighbor pairs a peer identifier with the policy it invokes.
type Neighbor struct {
	Name  string
	Entry string
}

// LeakWitness is the minimal leaking route together with the per-rule
// trace that explains how the leak happened.
type LeakWitness struct {
	Neighbor       Neighbor
	Route          Route
	ExportedRoute  Route
	SecretRetained bool
	Trace          *Trace
}

// DomainStats records the size of the finite domains that were covered.
type DomainStats struct {
	TagSets      int
	Prefixes     int
	PathLens     int
	Evaluations  int
	PrefixFamily string
}

// LeakReport is the result of a finite-domain exhaustive check.
type LeakReport struct {
	Leak   *LeakWitness
	Safe   bool
	Stats  DomainStats
	Errors []error
}

// CheckLeak validates the policy set and exhaustively enumerates the
// finite domains of tag sets, prefixes and path lengths to find the
// minimal route carrying spec.SecretTag that is accepted by a
// forbidden neighbor.
func (ps *PolicySet) CheckLeak(spec LeakSpec) *LeakReport {
	report := &LeakReport{Safe: true}
	if err := ps.Validate(); err != nil {
		report.Safe = false
		report.Errors = append(report.Errors, err)
		return report
	}
	if !validTag(spec.SecretTag) {
		report.Safe = false
		report.Errors = append(report.Errors, fmt.Errorf("secret tag %d outside the %d-tag universe", spec.SecretTag, MaxTags))
		return report
	}
	if len(spec.Neighbors) == 0 {
		report.Errors = append(report.Errors, fmt.Errorf("no forbidden neighbors supplied"))
		return report
	}
	for i := range spec.Neighbors {
		n := spec.Neighbors[i]
		if n.Entry == "" {
			n.Entry = spec.Entry
			spec.Neighbors[i] = n
		}
		if _, ok := ps.Policies[spec.Neighbors[i].Entry]; !ok {
			report.Safe = false
			report.Errors = append(report.Errors, fmt.Errorf("neighbor %q references unknown entry policy %q", n.Name, spec.Neighbors[i].Entry))
			return report
		}
	}

	tagSets := enumerateTagSets(spec, collectReferencedTags(ps))
	prefixes, family := enumeratePrefixes(collectPrefixRanges(ps))
	pathLens := enumeratePathLens(collectPathLenRanges(ps), spec.MaxPathLen)
	report.Stats = DomainStats{
		TagSets:      len(tagSets),
		Prefixes:     len(prefixes),
		PathLens:     len(pathLens),
		PrefixFamily: family,
	}

	for _, neighbor := range spec.Neighbors {
		for _, prefix := range prefixes {
			for _, pathLen := range pathLens {
				for _, tags := range tagSets {
					if !tags.Contains(spec.SecretTag) {
						continue
					}
					candidate := Route{Prefix: prefix, PathLen: pathLen, Tags: tags}
					report.Stats.Evaluations++
					verdict, exported, trace, err := ps.Eval(neighbor.Entry, candidate)
					if err != nil {
						report.Safe = false
						report.Errors = append(report.Errors, err)
						return report
					}
					if verdict != VerdictAccept {
						continue
					}
					if spec.RequireSecretRetained && !exported.Tags.Contains(spec.SecretTag) {
						continue
					}
					witness := &LeakWitness{
						Neighbor:       neighbor,
						Route:          candidate,
						ExportedRoute:  exported,
						SecretRetained: exported.Tags.Contains(spec.SecretTag),
						Trace:          trace,
					}
					if report.Leak == nil || lessWitness(witness, report.Leak) {
						report.Leak = witness
					}
				}
			}
		}
	}
	report.Safe = report.Leak == nil
	return report
}

// lessWitness defines the total order used to pick the minimal leak:
// prefix address, prefix length, path length, initial tag set.
func lessWitness(a, b *LeakWitness) bool {
	aa, aok := addrAsUint32(a.Route.Prefix)
	bb, bok := addrAsUint32(b.Route.Prefix)
	if aok && bok && aa != bb {
		return aa < bb
	}
	if a.Route.Prefix.Bits() != b.Route.Prefix.Bits() {
		return a.Route.Prefix.Bits() < b.Route.Prefix.Bits()
	}
	if a.Route.PathLen != b.Route.PathLen {
		return a.Route.PathLen < b.Route.PathLen
	}
	if a.Route.Tags != b.Route.Tags {
		return uint16(a.Route.Tags) < uint16(b.Route.Tags)
	}
	return a.Neighbor.Name < b.Neighbor.Name
}

func addrAsUint32(p Prefix) (uint32, bool) {
	if !p.Valid() || p.Net().Addr().BitLen() != 32 {
		return 0, false
	}
	b := p.Net().Addr().As4()
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]), true
}

// enumerateTagSets returns the finite domain of initial tag sets. In
// referenced mode only tags appearing in the policies (plus the secret)
// vary, which is exact because unreferenced tags never affect a
// condition. Full mode enumerates every one of the 65536 sets.
func enumerateTagSets(spec LeakSpec, referenced TagSet) []TagSet {
	if spec.FullTagDomain {
		out := make([]TagSet, 0, 1<<MaxTags)
		for x := 0; x < 1<<MaxTags; x++ {
			out = append(out, TagSet(x))
		}
		return out
	}
	vary := referenced.With(spec.SecretTag)
	bits := vary.List()
	out := []TagSet{}
	for mask := 0; mask < 1<<len(bits); mask++ {
		var set TagSet
		for i, t := range bits {
			if mask&(1<<i) != 0 {
				set = set.With(t)
			}
		}
		out = append(out, set)
	}
	return out
}

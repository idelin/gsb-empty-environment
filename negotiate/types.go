// Package negotiate implements capability negotiation between two parties.
//
// Each party publishes a Profile: a set of feature declarations carrying a
// supported version range, a required/optional flag, a preference weight,
// dependencies (optionally conditioned on the selected version of the
// declaring feature), and mutual exclusions. Negotiation searches the joint
// constraint space for the combination that both parties can satisfy and
// that maximizes, lexicographically:
//
//  1. total preference weight of selected optional features;
//  2. total of selected versions (higher versions preferred);
//  3. a deterministic feature-by-feature tie-break (selected beats absent,
//     then higher version, in sorted feature-name order).
//
// Because dependencies, exclusions and version conditions couple features,
// this is a global search; intersecting each feature's range locally is not
// sufficient. On success the result is bound to a Certificate whose summary
// authenticates both profiles and the selection for all later decoding. On
// failure the result carries human-readable rejection reasons plus a
// 1-minimal subset of the declared conditions that already conflict.
// Budget exhaustion is reported as its own status, never as infeasibility.
package negotiate

import (
	"errors"
	"fmt"
)

// VersionRange is an inclusive range of feature versions.
type VersionRange struct {
	Min int
	Max int
}

func (r VersionRange) String() string { return fmt.Sprintf("[%d..%d]", r.Min, r.Max) }

// Contains reports whether v lies inside the range.
func (r VersionRange) Contains(v int) bool { return v >= r.Min && v <= r.Max }

// Intersect returns the overlap of two ranges, or ok=false if it is empty.
func (r VersionRange) Intersect(o VersionRange) (VersionRange, bool) {
	lo := max(r.Min, o.Min)
	hi := min(r.Max, o.Max)
	if lo > hi {
		return VersionRange{}, false
	}
	return VersionRange{Min: lo, Max: hi}, true
}

// Requirement is a dependency on another feature. The dependency is active
// only while the declaring feature is selected and, if When is non-nil, its
// selected version lies inside When. While active, the target feature must
// be selected at a version inside Range.
type Requirement struct {
	Feature string
	Range   VersionRange
	When    *VersionRange
}

func (r Requirement) active(selfVersion int) bool {
	return r.When == nil || r.When.Contains(selfVersion)
}

func (r Requirement) String() string {
	if r.When != nil {
		return fmt.Sprintf("%s@%s (when self in %s)", r.Feature, r.Range, r.When)
	}
	return fmt.Sprintf("%s@%s", r.Feature, r.Range)
}

// FeatureDecl is one party's declaration for a single feature.
type FeatureDecl struct {
	Name      string
	Versions  VersionRange
	Required  bool // this party cannot operate without the feature
	Priority  int  // preference weight for optional features (may be negative)
	Depends   []Requirement
	Conflicts []string // names of mutually exclusive features
}

// Profile is one party's full negotiation declaration.
type Profile struct {
	Name     string
	Features []FeatureDecl
}

// Validate checks structural well-formedness of a profile.
func (p *Profile) Validate() error {
	if p.Name == "" {
		return errors.New("negotiate: profile name is empty")
	}
	seen := make(map[string]bool, len(p.Features))
	for _, f := range p.Features {
		if f.Name == "" {
			return fmt.Errorf("negotiate: %s: feature with empty name", p.Name)
		}
		if seen[f.Name] {
			return fmt.Errorf("negotiate: %s: duplicate feature %q", p.Name, f.Name)
		}
		seen[f.Name] = true
		if f.Versions.Min < 0 || f.Versions.Min > f.Versions.Max {
			return fmt.Errorf("negotiate: %s: feature %q has invalid range %s", p.Name, f.Name, f.Versions)
		}
		for _, d := range f.Depends {
			if d.Feature == "" {
				return fmt.Errorf("negotiate: %s: feature %q has dependency with empty target", p.Name, f.Name)
			}
			if d.Range.Min < 0 || d.Range.Min > d.Range.Max {
				return fmt.Errorf("negotiate: %s: feature %q dependency %q has invalid range %s", p.Name, f.Name, d.Feature, d.Range)
			}
			if d.When != nil && (d.When.Min < 0 || d.When.Min > d.When.Max) {
				return fmt.Errorf("negotiate: %s: feature %q dependency %q has invalid when-range %s", p.Name, f.Name, d.Feature, d.When)
			}
		}
		for _, c := range f.Conflicts {
			if c == "" {
				return fmt.Errorf("negotiate: %s: feature %q has empty conflict target", p.Name, f.Name)
			}
		}
	}
	return nil
}

func (p *Profile) feature(name string) *FeatureDecl {
	for i := range p.Features {
		if p.Features[i].Name == name {
			return &p.Features[i]
		}
	}
	return nil
}

// Clone deep-copies the profile.
func (p *Profile) Clone() *Profile {
	q := &Profile{Name: p.Name, Features: make([]FeatureDecl, len(p.Features))}
	for i, f := range p.Features {
		nf := f
		nf.Depends = append([]Requirement(nil), f.Depends...)
		nf.Conflicts = append([]string(nil), f.Conflicts...)
		q.Features[i] = nf
	}
	return q
}

// Selection maps feature names to the negotiated version. Absence of a
// feature means it is not used.
type Selection map[string]int

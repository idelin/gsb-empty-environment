package resolver

import (
	"fmt"
	"sort"
	"strings"
)

// PkgVersion identifies one concrete version of one package.
type PkgVersion struct {
	Pkg string
	Ver Version
}

func (pv PkgVersion) String() string { return pv.Pkg + "@" + pv.Ver.String() }

// Dependency states that a package version requires package Pkg to be
// installed at a version inside Range (closed interval).
type Dependency struct {
	Pkg   string
	Range Range
}

// Manifest is a finite, closed universe of packages and versions.
//   - Versions lists every available version of every package (sorted ascending).
//   - Deps maps a concrete package version to its dependencies.
//   - Conflicts lists pairs of package versions that cannot coexist.
//
// At most one version of any package may be selected in a solution.
type Manifest struct {
	Versions  map[string][]Version
	Deps      map[PkgVersion][]Dependency
	Conflicts [][2]PkgVersion
}

// Validate checks the manifest for internal consistency.
func (m *Manifest) Validate() error {
	for pkg, vs := range m.Versions {
		if len(vs) == 0 {
			return fmt.Errorf("package %q has no versions", pkg)
		}
		for i := 1; i < len(vs); i++ {
			if vs[i-1].Compare(vs[i]) >= 0 {
				return fmt.Errorf("package %q: versions not strictly increasing", pkg)
			}
		}
	}
	for pv, deps := range m.Deps {
		if !m.hasVersion(pv.Pkg, pv.Ver) {
			return fmt.Errorf("deps listed for unknown package version %s", pv)
		}
		for _, d := range deps {
			if _, ok := m.Versions[d.Pkg]; !ok {
				return fmt.Errorf("%s depends on unknown package %q", pv, d.Pkg)
			}
		}
	}
	for _, c := range m.Conflicts {
		for _, pv := range c {
			if !m.hasVersion(pv.Pkg, pv.Ver) {
				return fmt.Errorf("conflict references unknown package version %s", pv)
			}
		}
	}
	return nil
}

func (m *Manifest) hasVersion(pkg string, v Version) bool {
	for _, u := range m.Versions[pkg] {
		if u == v {
			return true
		}
	}
	return false
}

// hasVersionInRange reports whether the manifest offers any version of pkg in r.
func (m *Manifest) hasVersionInRange(pkg string, r Range) bool {
	for _, v := range m.Versions[pkg] {
		if r.Contains(v) {
			return true
		}
	}
	return false
}

// versionsInRange returns the manifest versions of pkg inside r, descending.
func (m *Manifest) versionsInRange(pkg string, r Range) []Version {
	vs := m.Versions[pkg]
	var out []Version
	for i := len(vs) - 1; i >= 0; i-- {
		if r.Contains(vs[i]) {
			out = append(out, vs[i])
		}
	}
	return out
}

// conflictsWith reports whether the two package versions cannot coexist.
func (m *Manifest) conflictsWith(a, b PkgVersion) bool {
	for _, c := range m.Conflicts {
		if (c[0] == a && c[1] == b) || (c[0] == b && c[1] == a) {
			return true
		}
	}
	return false
}

// Requirement is a root request: package Pkg must be installed at a version
// inside Range.
type Requirement struct {
	Pkg   string
	Range Range
}

func (r Requirement) String() string { return r.Pkg + " " + r.Range.String() }

// Lock is the resolution result: exactly one version per selected package.
type Lock struct {
	Versions map[string]Version
}

// Sorted returns the locked package versions in package-name order.
func (l *Lock) Sorted() []PkgVersion {
	out := make([]PkgVersion, 0, len(l.Versions))
	for pkg, v := range l.Versions {
		out = append(out, PkgVersion{pkg, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pkg < out[j].Pkg })
	return out
}

func (l *Lock) String() string {
	parts := make([]string, 0, len(l.Versions))
	for _, pv := range l.Sorted() {
		parts = append(parts, pv.String())
	}
	return strings.Join(parts, ", ")
}

// Package resolve implements a small dependency resolver over a finite,
// closed-world version manifest.
//
// The manifest lists, for every package, the complete set of available
// versions. Each version may declare dependencies as closed (inclusive)
// version ranges on other packages, and the manifest may declare conflicts:
// pairs of concrete package versions that cannot coexist. A solution assigns
// at most one version to each package.
//
// Solve optimizes two objectives, in order:
//  1. Maximize the version vector of the requested packages, compared
//     lexicographically in the order the requests are given.
//  2. Minimize the number of resolved (transitive) packages.
package resolve

import (
	"errors"
	"fmt"
	"sort"
)

// Version is a semantic-style version. The zero value is 0.0.0.
type Version struct {
	Major, Minor, Patch int
}

// Compare returns -1, 0 or +1 if v is less than, equal to, or greater than o.
func (v Version) Compare(o Version) int {
	switch {
	case v.Major != o.Major:
		if v.Major < o.Major {
			return -1
		}
		return 1
	case v.Minor != o.Minor:
		if v.Minor < o.Minor {
			return -1
		}
		return 1
	case v.Patch != o.Patch:
		if v.Patch < o.Patch {
			return -1
		}
		return 1
	}
	return 0
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Dep is a dependency on another package, constrained to the closed
// (inclusive) version range [Min, Max].
type Dep struct {
	Name     string
	Min, Max Version
}

// PackageVersion is a single version of a package and its dependencies.
type PackageVersion struct {
	Version Version
	Deps    []Dep
}

// Package is a named package with its complete list of available versions,
// sorted ascending by Version.
type Package struct {
	Name     string
	Versions []PackageVersion
}

// Conflict declares that PackageA at VersionA cannot coexist with
// PackageB at VersionB in the same solution.
type Conflict struct {
	PackageA string
	VersionA Version
	PackageB string
	VersionB Version
}

// Manifest is the closed world of packages the resolver may choose from.
type Manifest struct {
	Packages  map[string]*Package
	Conflicts []Conflict
}

// Validate checks the manifest for internal consistency. Solve returns an
// error for any manifest that does not validate.
func (m *Manifest) Validate() error {
	if m == nil {
		return errors.New("resolve: nil manifest")
	}
	for key, p := range m.Packages {
		if p == nil {
			return fmt.Errorf("resolve: package %q is nil", key)
		}
		if p.Name == "" {
			return errors.New("resolve: package with empty name")
		}
		if p.Name != key {
			return fmt.Errorf("resolve: package %q registered under key %q", p.Name, key)
		}
		for i, pv := range p.Versions {
			if i > 0 && p.Versions[i-1].Version.Compare(pv.Version) >= 0 {
				return fmt.Errorf("resolve: package %q versions not strictly increasing at %s", p.Name, pv.Version)
			}
			for _, d := range pv.Deps {
				if d.Name == "" {
					return fmt.Errorf("resolve: %s@%s has dependency with empty name", p.Name, pv.Version)
				}
				if d.Min.Compare(d.Max) > 0 {
					return fmt.Errorf("resolve: %s@%s depends on %q with empty range [%s, %s]",
						p.Name, pv.Version, d.Name, d.Min, d.Max)
				}
				if _, ok := m.Packages[d.Name]; !ok {
					return fmt.Errorf("resolve: %s@%s depends on %q, which is not in the manifest",
						p.Name, pv.Version, d.Name)
				}
			}
		}
	}
	for _, c := range m.Conflicts {
		for _, side := range [][2]interface{}{{c.PackageA, c.VersionA}, {c.PackageB, c.VersionB}} {
			name := side[0].(string)
			ver := side[1].(Version)
			p, ok := m.Packages[name]
			if !ok {
				return fmt.Errorf("resolve: conflict references %q, which is not in the manifest", name)
			}
			if p.version(ver) == nil {
				return fmt.Errorf("resolve: conflict references %s@%s, which is not in the manifest", name, ver)
			}
		}
	}
	return nil
}

// version returns the PackageVersion for v, or nil if the package does not
// provide it.
func (p *Package) version(v Version) *PackageVersion {
	i := sort.Search(len(p.Versions), func(i int) bool {
		return p.Versions[i].Version.Compare(v) >= 0
	})
	if i < len(p.Versions) && p.Versions[i].Version.Compare(v) == 0 {
		return &p.Versions[i]
	}
	return nil
}

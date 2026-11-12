package modselect

import (
	"errors"
	"fmt"
	"sort"
)

// ErrNoVersion is returned when no selectable version remains.
var ErrNoVersion = errors.New("modselect: no version available")

// Selection is the result of resolving a query against a Selector.
type Selection struct {
	Version   string
	Retracted bool
	Rationale string // retraction rationale, set when Retracted is true
}

// Selector resolves version queries for one module, honoring the
// retractions published by the module author and the exclusions declared
// by the main module.
type Selector struct {
	versions    []Version // sorted ascending by semver precedence
	retractions []Retraction
	excluded    map[string]bool
}

// NewSelector builds a Selector from the versions known to the proxy, the
// module's retraction directives, and the main module's exclude list.
func NewSelector(versions []string, retractions []Retraction, excludes []string) (*Selector, error) {
	seen := make(map[string]bool, len(versions))
	parsed := make([]Version, 0, len(versions))
	for _, raw := range versions {
		if seen[raw] {
			continue
		}
		v, err := Parse(raw)
		if err != nil {
			return nil, err
		}
		seen[raw] = true
		parsed = append(parsed, v)
	}
	sort.Slice(parsed, func(i, j int) bool { return Compare(parsed[i], parsed[j]) < 0 })
	excluded := make(map[string]bool, len(excludes))
	for _, e := range excludes {
		excluded[e] = true
	}
	return &Selector{versions: parsed, retractions: retractions, excluded: excluded}, nil
}

func (s *Selector) retractionFor(v Version) (Retraction, bool) {
	for _, r := range s.retractions {
		if r.Contains(v.Raw) {
			return r, true
		}
	}
	return Retraction{}, false
}

func (s *Selector) selection(v Version) Selection {
	r, retracted := s.retractionFor(v)
	return Selection{Version: v.Raw, Retracted: retracted, Rationale: r.Rationale}
}

// Latest resolves the @latest query: the highest non-excluded,
// non-retracted release; failing that the highest non-retracted
// prerelease; failing that a retracted version (releases first).
func (s *Selector) Latest() (Selection, error) {
	// Tiers: 0 = unretracted release, 1 = unretracted prerelease,
	// 2 = retracted release, 3 = retracted prerelease.
	for tier := 0; tier < 4; tier++ {
		wantRelease := tier%2 == 0
		allowRetracted := tier >= 2
		for i := len(s.versions) - 1; i >= 0; i-- {
			v := s.versions[i]
			if s.excluded[v.Raw] || v.IsRelease() != wantRelease {
				continue
			}
			_, retracted := s.retractionFor(v)
			if retracted != allowRetracted {
				continue
			}
			return s.selection(v), nil
		}
	}
	return Selection{}, ErrNoVersion
}

// Select resolves an explicit version query ("v1.2.3") or "latest".
// A retracted version is returned with its rationale attached. An excluded
// version is skipped and the query is bumped to the next higher
// non-excluded version.
func (s *Selector) Select(query string) (Selection, error) {
	if query == "latest" || query == "upgrade" {
		return s.Latest()
	}
	v, err := Parse(query)
	if err != nil {
		return Selection{}, err
	}
	idx := sort.Search(len(s.versions), func(i int) bool {
		return Compare(s.versions[i], v) >= 0
	})
	found := idx < len(s.versions) && s.versions[idx].Raw == query
	if !found {
		return Selection{}, fmt.Errorf("modselect: unknown version %q", query)
	}
	if !s.excluded[query] {
		return s.selection(s.versions[idx]), nil
	}
	// Excluded: bump to the next higher non-excluded version.
	for idx++; idx < len(s.versions); idx++ {
		cand := s.versions[idx]
		if s.excluded[cand.Raw] {
			continue
		}
		return s.selection(cand), nil
	}
	return Selection{}, fmt.Errorf("modselect: %q is excluded and no higher version is available: %w", query, ErrNoVersion)
}

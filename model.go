package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Version is a parsed semantic version (major.minor.patch, missing parts are 0).
type Version struct {
	Nums [3]int
	Raw  string
}

func ParseVersion(s string) (Version, error) {
	v := Version{Raw: s}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return v, fmt.Errorf("bad version %q", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, fmt.Errorf("bad version %q", s)
		}
		v.Nums[i] = n
	}
	return v, nil
}

func (v Version) Less(o Version) bool {
	for i := 0; i < 3; i++ {
		if v.Nums[i] != o.Nums[i] {
			return v.Nums[i] < o.Nums[i]
		}
	}
	return false
}

// Constraint is a conjunction of comparators, e.g. ">=1.2.0, <2.0.0". "*" matches all.
type Constraint struct {
	Raw  string
	cmps []comparator
}

type comparator struct {
	op  string // one of "=", ">", "<", ">=", "<="
	ver Version
}

func ParseConstraint(s string) (Constraint, error) {
	c := Constraint{Raw: s}
	s = strings.TrimSpace(s)
	if s == "" || s == "*" {
		return c, nil
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		op := "="
		for _, p := range []string{">=", "<=", "==", ">", "<", "="} {
			if strings.HasPrefix(part, p) {
				op = p
				part = strings.TrimSpace(part[len(p):])
				break
			}
		}
		if op == "==" {
			op = "="
		}
		v, err := ParseVersion(part)
		if err != nil {
			return c, err
		}
		c.cmps = append(c.cmps, comparator{op: op, ver: v})
	}
	return c, nil
}

func (c Constraint) Allows(v Version) bool {
	for _, cmp := range c.cmps {
		less, eq := v.Less(cmp.ver), v.Nums == cmp.ver.Nums
		switch cmp.op {
		case "=":
			if !eq {
				return false
			}
		case ">":
			if !(!less && !eq) {
				return false
			}
		case "<":
			if !less {
				return false
			}
		case ">=":
			if less {
				return false
			}
		case "<=":
			if !less && !eq {
				return false
			}
		}
	}
	return true
}

// Candidate is one installable artifact: a package version from a given source.
type Candidate struct {
	Name    string            `json:"name"`
	Version string            `json:"version"`
	Source  string            `json:"source"`
	Size    int64             `json:"size"`
	Deps    map[string]string `json:"deps"`

	parsed Version
	deps   []depEdge // sorted by dep name
}

type depEdge struct {
	name string
	con  Constraint
}

// LockEntry is one pinned package in the lock file.
type LockEntry struct {
	Version string `json:"version"`
	Source  string `json:"source"`
}

// Registry is the universe of installable candidates.
type Registry struct {
	byName map[string][]*Candidate // each list sorted by (version, source)
}

func NewRegistry(cands []Candidate) (*Registry, error) {
	r := &Registry{byName: map[string][]*Candidate{}}
	for i := range cands {
		c := &cands[i]
		v, err := ParseVersion(c.Version)
		if err != nil {
			return nil, fmt.Errorf("package %s: %v", c.Name, err)
		}
		c.parsed = v
		names := make([]string, 0, len(c.Deps))
		for n := range c.Deps {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			con, err := ParseConstraint(c.Deps[n])
			if err != nil {
				return nil, fmt.Errorf("package %s@%s dep %s: %v", c.Name, c.Version, n, err)
			}
			c.deps = append(c.deps, depEdge{name: n, con: con})
		}
		r.byName[c.Name] = append(r.byName[c.Name], c)
	}
	for _, list := range r.byName {
		sort.Slice(list, func(i, j int) bool {
			a, b := list[i], list[j]
			if a.parsed.Nums != b.parsed.Nums {
				return a.parsed.Less(b.parsed)
			}
			return a.Source < b.Source
		})
	}
	return r, nil
}

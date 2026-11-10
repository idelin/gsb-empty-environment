package main

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	SourcePublic  = "public"
	SourcePrivate = "private"
)

type Version struct {
	parts []int
	raw   string
}

func ParseVersion(s string) (Version, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Version{}, fmt.Errorf("空版本号")
	}
	segs := strings.Split(s, ".")
	parts := make([]int, len(segs))
	for i, seg := range segs {
		n, err := strconv.Atoi(seg)
		if err != nil || n < 0 {
			return Version{}, fmt.Errorf("非法版本号 %q", s)
		}
		parts[i] = n
	}
	return Version{parts: parts, raw: s}, nil
}

func (v Version) String() string { return v.raw }

func CompareVersion(a, b Version) int {
	n := len(a.parts)
	if len(b.parts) > n {
		n = len(b.parts)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(a.parts) {
			x = a.parts[i]
		}
		if i < len(b.parts) {
			y = b.parts[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

type Comparator struct {
	Op  string
	Ver Version
}

type Range struct {
	raw   string
	comps []Comparator
}

func ParseRange(s string) (Range, error) {
	r := Range{raw: strings.TrimSpace(s)}
	if r.raw == "" || r.raw == "*" {
		r.raw = "*"
		return r, nil
	}
	for _, tok := range strings.Fields(strings.ReplaceAll(r.raw, ",", " ")) {
		op := "="
		for _, p := range []string{">=", "<=", "==", "!=", ">", "<", "="} {
			if strings.HasPrefix(tok, p) {
				op = p
				tok = strings.TrimSpace(tok[len(p):])
				break
			}
		}
		v, err := ParseVersion(tok)
		if err != nil {
			return Range{}, fmt.Errorf("非法区间 %q: %v", r.raw, err)
		}
		r.comps = append(r.comps, Comparator{Op: op, Ver: v})
	}
	return r, nil
}

func (r Range) String() string { return r.raw }

func (r Range) Allows(v Version) bool {
	for _, c := range r.comps {
		cmp := CompareVersion(v, c.Ver)
		var ok bool
		switch c.Op {
		case "=", "==":
			ok = cmp == 0
		case "!=":
			ok = cmp != 0
		case ">":
			ok = cmp > 0
		case ">=":
			ok = cmp >= 0
		case "<":
			ok = cmp < 0
		case "<=":
			ok = cmp <= 0
		}
		if !ok {
			return false
		}
	}
	return true
}

type VersionInfo struct {
	Deps map[string]string `json:"deps"`
}

type Source struct {
	Packages map[string]map[string]VersionInfo `json:"packages"`
}

type Input struct {
	Roots           map[string]string `json:"roots"`
	PrivatePackages []string          `json:"private_packages"`
	Sources         map[string]Source `json:"sources"`
}

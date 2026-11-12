package taskpkg

import (
	"fmt"
	"sort"
	"strings"
)

// RRType is a DNS-style record or query type. It is a string so callers can
// use the common textual spellings (A, AAAA, CNAME, and so on) without a
// conversion layer. Comparison is case-insensitive after normalization.
type RRType string

const (
	TypeA     RRType = "A"
	TypeAAAA  RRType = "AAAA"
	TypeCNAME RRType = "CNAME"
	TypeNS    RRType = "NS"
	TypeTXT   RRType = "TXT"
	TypeAny   RRType = "*"
)

// IsAny reports whether t is the wildcard/unspecified type.
func (t RRType) IsAny() bool {
	s := strings.TrimSpace(string(t))
	return s == "" || s == "*"
}

// CanonicalType normalizes a record type for comparison. An empty value is
// retained as empty, which callers may use as an unspecified/wildcard value.
func CanonicalType(t RRType) RRType {
	return RRType(strings.ToUpper(strings.TrimSpace(string(t))))
}

func canonicalType(t RRType) RRType {
	return CanonicalType(t)
}

// Query is one resolution request. Type may be TypeAny (or empty) when the
// caller does not care about the concrete type.
type Query struct {
	Name string
	Type RRType
}

func (q Query) canonical() (string, RRType, error) {
	if strings.TrimSpace(q.Name) == "" {
		return "", "", fmt.Errorf("taskpkg: empty query name")
	}
	name, err := CanonicalName(q.Name)
	if err != nil {
		return "", "", err
	}
	qtype := canonicalType(q.Type)
	if qtype == "" {
		qtype = TypeAny
	}
	return name, qtype, nil
}

// CanonicalName lowercases a DNS-style name, removes one trailing root dot,
// and validates that it has no empty labels. The root name is returned as
// ".".
func CanonicalName(name string) (string, error) {
	s := strings.TrimSpace(name)
	if s == "" || s == "." {
		return ".", nil
	}
	s = strings.ToLower(s)
	s = strings.TrimSuffix(s, ".")
	if s == "" {
		return ".", nil
	}
	labels := strings.Split(s, ".")
	for _, label := range labels {
		if label == "" {
			return "", fmt.Errorf("taskpkg: empty label in name %q", name)
		}
		if strings.ContainsAny(label, " \t\r\n") {
			return "", fmt.Errorf("taskpkg: whitespace in name %q", name)
		}
	}
	return strings.Join(labels, "."), nil
}

// ZoneSet is a finite, explicitly configured tree of authoritative zones.
// There is no implicit root: a name outside every configured zone has no
// authority and therefore cannot have a trusted proof. This is intentional;
// silently adding "." would make a proof that merely claims the root zone
// authoritative for every name.
type ZoneSet struct {
	zones []string
	set   map[string]struct{}
}

// NewZoneSet builds an authoritative zone set. Zone names are normalized in
// the same way as ordinary names.
func NewZoneSet(zones ...string) (*ZoneSet, error) {
	seen := make(map[string]struct{}, len(zones))
	out := make([]string, 0, len(zones))
	for _, zone := range zones {
		if strings.TrimSpace(zone) == "" {
			return nil, fmt.Errorf("taskpkg: empty zone name")
		}
		z, err := CanonicalName(zone)
		if err != nil {
			return nil, fmt.Errorf("taskpkg: bad zone %q: %w", zone, err)
		}
		if _, ok := seen[z]; ok {
			continue
		}
		seen[z] = struct{}{}
		out = append(out, z)
	}
	sort.Slice(out, func(i, j int) bool {
		di, dj := zoneDepth(out[i]), zoneDepth(out[j])
		if di != dj {
			return di > dj
		}
		return out[i] < out[j]
	})
	return &ZoneSet{zones: out, set: seen}, nil
}

// Zones returns a copy of the configured zones, ordered from most specific to
// least specific.
func (zs *ZoneSet) Zones() []string {
	if zs == nil {
		return nil
	}
	out := make([]string, len(zs.zones))
	copy(out, zs.zones)
	return out
}

// Contains reports whether zone is one of the configured zones.
func (zs *ZoneSet) Contains(zone string) bool {
	if zs == nil {
		return false
	}
	z, err := CanonicalName(zone)
	if err != nil {
		return false
	}
	_, ok := zs.set[z]
	return ok
}

// Authority returns the deepest configured zone that is a suffix of name.
// It returns false when name is outside all configured zones.
func (zs *ZoneSet) Authority(name string) (string, bool) {
	if zs == nil {
		return "", false
	}
	n, err := CanonicalName(name)
	if err != nil {
		return "", false
	}
	for _, zone := range zs.zones {
		if zone == "." {
			return zone, true
		}
		if n == zone || strings.HasSuffix(n, "."+zone) {
			return zone, true
		}
	}
	return "", false
}

func zoneDepth(zone string) int {
	if zone == "." {
		return 0
	}
	return strings.Count(zone, ".") + 1
}

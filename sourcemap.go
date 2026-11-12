package netsec

import "strings"

// MapEntry binds a pattern to an ordered set of source names. A pattern is
// either a literal package ID or a prefix ending in '*'. Matching is
// case-insensitive.
type MapEntry struct {
	Pattern string
	Sources []string
}

type mapRule struct {
	raw     string
	stem    string
	prefix  bool
	sources []string
}

// SourceMap is an immutable snapshot of the source mapping table. Admins
// swap it atomically via Resolver.ReplaceMap.
type SourceMap struct {
	rules []mapRule
}

// NewSourceMap validates patterns and builds an immutable mapping table.
func NewSourceMap(entries ...MapEntry) (*SourceMap, error) {
	rules := make([]mapRule, 0, len(entries))
	for _, e := range entries {
		p := strings.ToLower(strings.TrimSpace(e.Pattern))
		if p == "" {
			return nil, ErrBadPattern
		}
		r := mapRule{raw: p, sources: append([]string(nil), e.Sources...)}
		if strings.HasSuffix(p, "*") {
			r.prefix = true
			r.stem = strings.TrimSuffix(p, "*")
			if r.stem == "" || strings.Contains(r.stem, "*") {
				return nil, ErrBadPattern
			}
		} else {
			r.stem = p
		}
		rules = append(rules, r)
	}
	return &SourceMap{rules: rules}, nil
}

// lookup returns the source set of the longest matching pattern, or nil when
// no pattern matches. There is no fallback to any other source.
func (m *SourceMap) lookup(id string) []string {
	if m == nil {
		return nil
	}
	lower := strings.ToLower(id)
	best := -1
	var sources []string
	for _, r := range m.rules {
		matched := r.prefix && strings.HasPrefix(lower, r.stem) ||
			!r.prefix && lower == r.stem
		if matched && len(r.raw) > best {
			best = len(r.raw)
			sources = r.sources
		}
	}
	return sources
}

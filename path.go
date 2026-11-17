package netsec

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// A pathPrefix is a canonical, slash-separated resource path. Prefix matching
// is performed on whole path components, never on raw string prefixes.
type pathPrefix struct {
	text     string
	parts    []string
	absolute bool
}

func parsePathPrefix(raw string) (pathPrefix, error) {
	if raw == "" {
		return pathPrefix{}, fmt.Errorf("%w: empty resource prefix", ErrInvalidCertificate)
	}
	if len(raw) > maxStringLength {
		return pathPrefix{}, fmt.Errorf("%w: resource prefix too long", ErrInvalidCertificate)
	}
	if strings.IndexByte(raw, 0) >= 0 {
		return pathPrefix{}, fmt.Errorf("%w: resource prefix contains NUL", ErrInvalidCertificate)
	}

	cleaned := path.Clean(raw)
	if cleaned == "." {
		return pathPrefix{}, fmt.Errorf("%w: invalid resource prefix %q", ErrInvalidCertificate, raw)
	}

	absolute := strings.HasPrefix(cleaned, "/")
	var parts []string
	for _, part := range strings.Split(cleaned, "/") {
		if part != "" {
			parts = append(parts, part)
		}
	}

	return pathPrefix{text: cleaned, parts: parts, absolute: absolute}, nil
}

func (p pathPrefix) isPrefixOf(other pathPrefix) bool {
	if p.absolute != other.absolute || len(p.parts) > len(other.parts) {
		return false
	}
	for i := range p.parts {
		if p.parts[i] != other.parts[i] {
			return false
		}
	}
	return true
}

func normalizePrefixSet(raw []string) ([]pathPrefix, error) {
	seen := make(map[string]pathPrefix, len(raw))
	for _, item := range raw {
		prefix, err := parsePathPrefix(item)
		if err != nil {
			return nil, err
		}
		seen[prefix.text] = prefix
	}

	prefixes := make([]pathPrefix, 0, len(seen))
	for _, prefix := range seen {
		prefixes = append(prefixes, prefix)
	}
	sort.Slice(prefixes, func(i, j int) bool {
		if prefixes[i].absolute != prefixes[j].absolute {
			return prefixes[i].absolute
		}
		return prefixes[i].text < prefixes[j].text
	})
	return prefixes, nil
}

// reducePrefixes removes entries covered by a broader prefix. The remaining
// set is the canonical representation of the union of all supplied prefix
// languages.
func reducePrefixes(prefixes []pathPrefix) []pathPrefix {
	sort.Slice(prefixes, func(i, j int) bool {
		if len(prefixes[i].parts) != len(prefixes[j].parts) {
			return len(prefixes[i].parts) < len(prefixes[j].parts)
		}
		if prefixes[i].absolute != prefixes[j].absolute {
			return prefixes[i].absolute
		}
		return prefixes[i].text < prefixes[j].text
	})

	out := make([]pathPrefix, 0, len(prefixes))
	for _, prefix := range prefixes {
		covered := false
		for _, kept := range out {
			if kept.isPrefixOf(prefix) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, prefix)
		}
	}
	return out
}

// intersectPrefixes computes the exact intersection of two path-prefix
// languages. Two prefixes intersect only when one is a component-wise prefix
// of the other; the narrower prefix is the intersection.
func intersectPrefixes(left, right []pathPrefix) []pathPrefix {
	var out []pathPrefix
	for _, a := range left {
		for _, b := range right {
			switch {
			case a.isPrefixOf(b):
				out = append(out, b)
			case b.isPrefixOf(a):
				out = append(out, a)
			}
		}
	}
	return reducePrefixes(dedupePrefixes(out))
}

func dedupePrefixes(prefixes []pathPrefix) []pathPrefix {
	seen := make(map[string]pathPrefix, len(prefixes))
	for _, prefix := range prefixes {
		seen[prefix.text] = prefix
	}
	out := make([]pathPrefix, 0, len(seen))
	for _, prefix := range seen {
		out = append(out, prefix)
	}
	return out
}

func anyPrefixMatches(prefixes []pathPrefix, resource pathPrefix) bool {
	for _, prefix := range prefixes {
		if prefix.isPrefixOf(resource) {
			return true
		}
	}
	return false
}

func prefixTexts(prefixes []pathPrefix) []string {
	out := make([]string, len(prefixes))
	for i, prefix := range prefixes {
		out[i] = prefix.text
	}
	return out
}

func prefixStateKey(prefixes []pathPrefix) string {
	var b strings.Builder
	for _, prefix := range prefixes {
		if prefix.absolute {
			b.WriteByte('A')
		} else {
			b.WriteByte('R')
		}
		b.WriteString(prefix.text)
		b.WriteByte(0)
	}
	return b.String()
}

// PathPrefixMatches reports whether the canonical resource path is authorized
// by the prefix. Matching is component based: prefix "/a/b" matches
// "/a/b/c" but not "/a/bc".
func PathPrefixMatches(prefix, resource string) (bool, error) {
	p, err := parsePathPrefix(prefix)
	if err != nil {
		return false, err
	}
	r, err := parsePath(resource)
	if err != nil {
		return false, err
	}
	return p.isPrefixOf(r), nil
}

func parsePath(raw string) (pathPrefix, error) {
	if raw == "" {
		return pathPrefix{}, fmt.Errorf("%w: empty resource path", ErrInvalidRequest)
	}
	if len(raw) > maxStringLength {
		return pathPrefix{}, fmt.Errorf("%w: resource path too long", ErrInvalidRequest)
	}
	if strings.IndexByte(raw, 0) >= 0 {
		return pathPrefix{}, fmt.Errorf("%w: resource path contains NUL", ErrInvalidRequest)
	}

	cleaned := path.Clean(raw)
	if cleaned == "." {
		return pathPrefix{}, fmt.Errorf("%w: invalid resource path %q", ErrInvalidRequest, raw)
	}

	absolute := strings.HasPrefix(cleaned, "/")
	var parts []string
	for _, part := range strings.Split(cleaned, "/") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return pathPrefix{text: cleaned, parts: parts, absolute: absolute}, nil
}

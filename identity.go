package taskpkg

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// SiteConfig declares the identity rules for one site. The zero value is not
// usable; construct one with [NewSiteConfig].
type SiteConfig struct {
	// Base is the fixed snapshot address used to resolve every relative
	// reference. It is never mutated by [SiteConfig.Identify].
	Base *url.URL

	// IgnoredParams lists query parameter names that do not contribute to the
	// identity (tracking tokens, session ids, ...). They remain present in the
	// requestable URL.
	IgnoredParams map[string]bool

	// SortableParams lists query parameter names whose occurrences may be
	// reordered without changing the identity, e.g. multi-value filters. The
	// occurrences are sorted as a multiset, so duplicates and values survive.
	SortableParams map[string]bool

	// OrderedParams lists query parameter names whose sequence is meaningful;
	// they are kept verbatim and order-sensitive, including repetitions.
	OrderedParams map[string]bool

	// ClientRouting reports whether the site renders routes on the client.
	// Only when true does the URL fragment participate in the identity and
	// survive in the requestable URL; otherwise fragments are stripped.
	ClientRouting bool
}

// NewSiteConfig parses baseURL and builds a validated [SiteConfig]. The three
// parameter lists must be pairwise disjoint.
func NewSiteConfig(baseURL string, ignored, sortable, ordered []string, clientRouting bool) (*SiteConfig, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("taskpkg: parse base url %q: %w", baseURL, err)
	}
	cfg := &SiteConfig{
		Base:           base,
		IgnoredParams:  toSet(ignored),
		SortableParams: toSet(sortable),
		OrderedParams:  toSet(ordered),
		ClientRouting:  clientRouting,
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate checks that the base is absolute and that no parameter is declared
// in more than one role.
func (c *SiteConfig) Validate() error {
	if c == nil || c.Base == nil {
		return errors.New("taskpkg: site config requires a base url")
	}
	if !c.Base.IsAbs() || c.Base.Host == "" {
		return fmt.Errorf("taskpkg: base url %q must be absolute", c.Base.String())
	}
	for name, group := range map[string]map[string]bool{
		"ignored":  c.IgnoredParams,
		"sortable": c.SortableParams,
		"ordered":  c.OrderedParams,
	} {
		if group[""] {
			return fmt.Errorf("taskpkg: %s parameters must not contain an empty name", name)
		}
	}
	for _, name := range keys(c.SortableParams) {
		if c.IgnoredParams[name] {
			return fmt.Errorf("taskpkg: parameter %q declared as both ignored and sortable", name)
		}
		if c.OrderedParams[name] {
			return fmt.Errorf("taskpkg: parameter %q declared as both sortable and ordered", name)
		}
	}
	for name := range c.OrderedParams {
		if c.IgnoredParams[name] {
			return fmt.Errorf("taskpkg: parameter %q declared as both ignored and ordered", name)
		}
	}
	return nil
}

// Identity is the result of classifying one reference.
//
// Key is the canonical identity used for de-duplication: references that name
// the same resource share a Key. RequestURL is the fully resolved URL that can
// be fetched as-is; it preserves the original encoding and any ignored
// parameters and, outside client routing, carries no fragment.
type Identity struct {
	Key        string
	RequestURL string
}

// Identify resolves rawRef against the configured snapshot base and computes
// its identity. rawRef may be a relative reference, a protocol-relative URL or
// an absolute URL.
func (c *SiteConfig) Identify(rawRef string) (*Identity, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	ref, err := url.Parse(rawRef)
	if err != nil {
		return nil, fmt.Errorf("taskpkg: parse reference %q: %w", rawRef, err)
	}
	resolved := c.Base.ResolveReference(ref)

	request := *resolved
	if !c.ClientRouting {
		request.Fragment = ""
		request.RawFragment = ""
	}

	return &Identity{
		Key:        canonicalKey(resolved, c),
		RequestURL: request.String(),
	}, nil
}

// canonicalKey renders the stable identity string for an already resolved URL.
func canonicalKey(u *url.URL, c *SiteConfig) string {
	var b strings.Builder
	b.Grow(len(u.String()) + 16)
	b.WriteString(u.Scheme)
	b.WriteByte(':')
	if u.Host != "" {
		b.WriteString("//")
		if u.User != nil {
			b.WriteString(u.User.String())
			b.WriteByte('@')
		}
		b.WriteString(u.Host)
	}
	b.WriteString(canonicalPath(u.EscapedPath()))
	if query := canonicalQuery(u.RawQuery, c); query != "" {
		b.WriteByte('?')
		b.WriteString(query)
	}
	if c.ClientRouting && (u.Fragment != "" || u.RawFragment != "") {
		b.WriteByte('#')
		b.WriteString(u.EscapedFragment())
	}
	return b.String()
}

// canonicalPath decodes percent-encoded triplets only when they represent an
// unreserved ASCII character (RFC 3986: ALPHA / DIGIT / "-" / "." / "_" /
// "~"). Everything else, including "%2F", is left byte-for-byte intact, so an
// encoded separator can never collapse onto a real path segment.
func canonicalPath(escaped string) string {
	var b strings.Builder
	b.Grow(len(escaped))
	for i := 0; i < len(escaped); i++ {
		if escaped[i] == '%' && i+2 < len(escaped) {
			hi, okHi := hexValue(escaped[i+1])
			lo, okLo := hexValue(escaped[i+2])
			if okHi && okLo {
				decoded := byte(hi<<4 | lo)
				if isUnreservedASCII(decoded) {
					b.WriteByte(decoded)
					i += 2
					continue
				}
			}
		}
		b.WriteByte(escaped[i])
	}
	return b.String()
}

// canonicalQuery transforms only the parameters declared in the config.
// Ignored parameters are removed; sortable parameters are collected into one
// sorted multiset anchored at the position of their first occurrence; ordered
// and undeclared parameters keep their original positions and encoding.
func canonicalQuery(raw string, c *SiteConfig) string {
	if raw == "" {
		return ""
	}
	tokens := strings.Split(raw, "&")
	anchor := -1
	var sortable []string
	for idx, token := range tokens {
		if c.SortableParams[queryKey(token)] {
			if anchor == -1 {
				anchor = idx
			}
			sortable = append(sortable, token)
		}
	}
	sort.Strings(sortable)

	if anchor == -1 {
		kept := tokens[:0:0]
		for _, token := range tokens {
			if !c.IgnoredParams[queryKey(token)] {
				kept = append(kept, token)
			}
		}
		return strings.Join(kept, "&")
	}

	var before, after []string
	for idx, token := range tokens {
		key := queryKey(token)
		switch {
		case c.IgnoredParams[key]:
			continue
		case c.SortableParams[key]:
			if idx == anchor {
				before = append(before, sortable...)
			}
		case idx < anchor:
			before = append(before, token)
		default:
			after = append(after, token)
		}
	}
	return strings.Join(append(before, after...), "&")
}

// queryKey returns the raw (still percent-encoded) name of a query token.
// Configuration names are matched against this raw form, so declared
// transformations never implicitly decode parameter values.
func queryKey(token string) string {
	if idx := strings.IndexByte(token, '='); idx >= 0 {
		return token[:idx]
	}
	return token
}

func isUnreservedASCII(ch byte) bool {
	switch {
	case 'a' <= ch && ch <= 'z', 'A' <= ch && ch <= 'Z', '0' <= ch && ch <= '9':
		return true
	}
	switch ch {
	case '-', '.', '_', '~':
		return true
	}
	return false
}

func hexValue(ch byte) (int, bool) {
	switch {
	case '0' <= ch && ch <= '9':
		return int(ch - '0'), true
	case 'A' <= ch && ch <= 'F':
		return int(ch-'A') + 10, true
	case 'a' <= ch && ch <= 'f':
		return int(ch-'a') + 10, true
	}
	return 0, false
}

func toSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set
}

func keys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	return out
}

// Package psl derives public suffixes and organizational domains from the
// Public Suffix List rules bundled with this module.
package psl

import (
	"bufio"
	_ "embed"
	"errors"
	"io"
	"strings"
	"sync"
)

//go:embed public_suffix_list.dat
var embeddedRules string

// List is a parsed set of Public Suffix List rules.
type List struct {
	exact      map[string]struct{}
	wildcard   map[string]struct{}
	exceptions map[string]struct{}
}

var (
	defaultOnce sync.Once
	defaultList *List
)

// Default returns the list embedded in this package.
func Default() *List {
	defaultOnce.Do(func() {
		list, err := Parse(strings.NewReader(embeddedRules))
		if err != nil {
			panic("psl: invalid embedded rule list: " + err.Error())
		}
		defaultList = list
	})
	return defaultList
}

// Parse reads Public Suffix List rules from r. Comment and empty lines are
// ignored. Both wildcard (*.example) and exception (!www.example) rules are
// supported.
func Parse(r io.Reader) (*List, error) {
	if r == nil {
		return nil, errors.New("psl: nil rule reader")
	}
	list := &List{
		exact:      make(map[string]struct{}),
		wildcard:   make(map[string]struct{}),
		exceptions: make(map[string]struct{}),
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		if err := list.Add(scanner.Text()); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return list, nil
}

// New parses rules from r and is an alias for Parse.
func New(r io.Reader) (*List, error) { return Parse(r) }

// Add adds one rule to the list. It is safe to call Add while holding no other
// lock; callers that build a list concurrently must provide their own locking.
func (l *List) Add(rule string) error {
	rule = strings.TrimSpace(rule)
	if rule == "" || strings.HasPrefix(rule, "//") {
		return nil
	}
	if l.exact == nil {
		l.exact = make(map[string]struct{})
		l.wildcard = make(map[string]struct{})
		l.exceptions = make(map[string]struct{})
	}
	switch {
	case strings.HasPrefix(rule, "!"):
		name, err := normalizeRule(strings.TrimPrefix(rule, "!"))
		if err != nil {
			return err
		}
		l.exceptions[name] = struct{}{}
	case strings.HasPrefix(rule, "*."):
		name, err := normalizeRule(strings.TrimPrefix(rule, "*."))
		if err != nil {
			return err
		}
		l.wildcard[name] = struct{}{}
	default:
		name, err := normalizeRule(rule)
		if err != nil {
			return err
		}
		l.exact[name] = struct{}{}
	}
	return nil
}

func normalizeRule(rule string) (string, error) {
	rule = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(rule)), ".")
	if rule == "" {
		return "", errors.New("psl: empty rule")
	}
	for _, label := range strings.Split(rule, ".") {
		if label == "" {
			return "", errors.New("psl: rule contains an empty label")
		}
	}
	return rule, nil
}

// PublicSuffix returns the public suffix of domain according to the list. The
// empty string is returned for an empty domain.
func (l *List) PublicSuffix(domain string) string {
	_, suffix := l.match(domain)
	return suffix
}

// EffectiveTLDPlusOne returns the organizational domain (public suffix plus
// one label) for domain.
func (l *List) EffectiveTLDPlusOne(domain string) (string, error) {
	org := l.OrganizationalDomain(domain)
	if org == "" {
		return "", errors.New("psl: domain is a public suffix")
	}
	return org, nil
}

// OrganizationalDomain returns the public suffix plus one label. It returns an
// empty string when domain is itself a public suffix (or is empty).
func (l *List) OrganizationalDomain(domain string) string {
	normalized, ok := normalizeDomain(domain)
	if !ok {
		return ""
	}
	_, suffix := l.match(normalized)
	if suffix == "" || suffix == normalized {
		return ""
	}
	labels := strings.Split(normalized, ".")
	suffixLabels := len(strings.Split(suffix, "."))
	if suffixLabels >= len(labels) {
		return ""
	}
	return strings.Join(labels[len(labels)-suffixLabels-1:], ".")
}

// IsPublicSuffix reports whether domain is exactly a public suffix.
func (l *List) IsPublicSuffix(domain string) bool {
	normalized, ok := normalizeDomain(domain)
	if !ok {
		return false
	}
	_, suffix := l.match(normalized)
	return suffix == normalized
}

// PublicSuffix returns the public suffix using the embedded list.
func PublicSuffix(domain string) string { return Default().PublicSuffix(domain) }

// OrganizationalDomain returns the organizational domain using the embedded
// list.
func OrganizationalDomain(domain string) string {
	return Default().OrganizationalDomain(domain)
}

// EffectiveTLDPlusOne is an alias for OrganizationalDomain with an error for
// inputs that have no registrable domain.
func EffectiveTLDPlusOne(domain string) (string, error) {
	return Default().EffectiveTLDPlusOne(domain)
}

// IsPublicSuffix reports whether domain is a public suffix according to the
// embedded list.
func IsPublicSuffix(domain string) bool { return Default().IsPublicSuffix(domain) }

func normalizeDomain(domain string) (string, bool) {
	domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	if domain == "" || len(domain) > 253 {
		return "", false
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || len(label) > 63 {
			return "", false
		}
	}
	return domain, true
}

func (l *List) match(domain string) (rule, suffix string) {
	normalized, ok := normalizeDomain(domain)
	if !ok {
		return "", ""
	}
	labels := strings.Split(normalized, ".")

	// Exception rules are evaluated first. They effectively remove the
	// leftmost label of the exception and make the remainder the suffix.
	exceptionLabels := 0
	for i := 0; i < len(labels); i++ {
		candidate := strings.Join(labels[i:], ".")
		if _, ok := l.exceptions[candidate]; ok && len(labels)-i > exceptionLabels {
			exceptionLabels = len(labels) - i
			rule = "!" + candidate
			if len(labels)-i > 1 {
				suffix = strings.Join(labels[i+1:], ".")
			} else {
				suffix = ""
			}
		}
	}
	if exceptionLabels > 0 {
		return rule, suffix
	}

	best := 0
	for i := 0; i < len(labels); i++ {
		candidate := strings.Join(labels[i:], ".")
		if _, ok := l.exact[candidate]; ok && len(labels)-i > best {
			best = len(labels) - i
			rule = candidate
			suffix = candidate
		}
	}

	// A wildcard rule matches one or more labels below its base.
	for i := 0; i < len(labels)-1; i++ {
		base := strings.Join(labels[i+1:], ".")
		if _, ok := l.wildcard[base]; ok {
			matchedLabels := len(labels) - i
			if matchedLabels > best {
				best = matchedLabels
				rule = "*." + base
				suffix = strings.Join(labels[i:], ".")
			}
		}
	}

	// The implicit default rule is "*".
	if best == 0 {
		return "*", labels[len(labels)-1]
	}
	return rule, suffix
}

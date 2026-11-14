package netsec

import (
	"fmt"
	"strings"
	"unicode"
)

// SourceKind records the trust class of a logical source.
type SourceKind string

const (
	SourcePublic  SourceKind = "public"
	SourcePrivate SourceKind = "private"
)

// Source is one logical module source. Its ID is stable and is used both in
// source-isolated cache keys and in audit records.
//
// Repositories (and their mirrors) are bound to a Source by
// ResolverConfig.Repositories; they are not selected from package metadata.
type Source struct {
	ID   string     `json:"id"`
	Kind SourceKind `json:"kind"`
}

// NamespaceBinding pins all module paths under Namespace to one source.
//
// A namespace matches on module-path segment boundaries. The special
// namespace "*" is an optional default for paths not covered by an explicit
// namespace.
type NamespaceBinding struct {
	Namespace string `json:"namespace"`
	SourceID  string `json:"source_id"`
}

// PolicyConfig is the immutable policy input. Sources retain their declaration
// order; bindings retain theirs. Neither can be changed by module metadata.
type PolicyConfig struct {
	Sources  []Source
	Bindings []NamespaceBinding
}

// Policy is an immutable namespace-to-source map. Construct it with NewPolicy.
// All accessors return copies so callers cannot mutate resolution policy at
// runtime.
type Policy struct {
	sources     []Source
	sourcesByID map[string]Source
	bindings    []NamespaceBinding
	used        map[string]struct{}
	wildcard    string
}

// NewPolicy validates and freezes a namespace policy.
//
// Explicit namespaces may not overlap when they name different sources, even
// when one namespace is more specific than the other. This makes the source
// for every module path unambiguous. The "*" namespace is only a default and
// does not overlap explicit namespaces.
func NewPolicy(cfg PolicyConfig) (*Policy, error) {
	if len(cfg.Bindings) == 0 {
		return nil, fmt.Errorf("%w: at least one namespace binding is required", ErrInvalidPolicy)
	}

	p := &Policy{
		sourcesByID: make(map[string]Source, len(cfg.Sources)),
		used:        make(map[string]struct{}),
	}

	for i, src := range cfg.Sources {
		src.ID = strings.TrimSpace(src.ID)
		if err := validateSource(src); err != nil {
			return nil, fmt.Errorf("%w: source %d: %v", ErrInvalidPolicy, i, err)
		}
		if _, exists := p.sourcesByID[src.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate source ID %q", ErrInvalidPolicy, src.ID)
		}
		p.sources = append(p.sources, src)
		p.sourcesByID[src.ID] = src
	}
	if len(p.sources) == 0 {
		return nil, fmt.Errorf("%w: at least one source is required", ErrInvalidPolicy)
	}

	seenNamespace := make(map[string]string, len(cfg.Bindings))
	for i, binding := range cfg.Bindings {
		ns, err := normalizeNamespace(binding.Namespace)
		if err != nil {
			return nil, fmt.Errorf("%w: binding %d: %v", ErrInvalidPolicy, i, err)
		}
		sourceID := strings.TrimSpace(binding.SourceID)
		if _, ok := p.sourcesByID[sourceID]; !ok {
			return nil, fmt.Errorf("%w: binding %q refers to unknown source %q", ErrInvalidPolicy, ns, sourceID)
		}
		if prior, exists := seenNamespace[ns]; exists {
			return nil, fmt.Errorf("%w: namespace %q is declared more than once (%q and %q)", ErrInvalidPolicy, ns, prior, sourceID)
		}
		seenNamespace[ns] = sourceID
		binding = NamespaceBinding{Namespace: ns, SourceID: sourceID}

		if ns == "*" {
			if p.wildcard != "" {
				return nil, fmt.Errorf("%w: multiple default namespace bindings", ErrInvalidPolicy)
			}
			p.wildcard = sourceID
		} else {
			for _, earlier := range p.bindings {
				if earlier.Namespace == "*" || earlier.SourceID == binding.SourceID {
					continue
				}
				if namespaceOverlaps(earlier.Namespace, binding.Namespace) {
					return nil, fmt.Errorf(
						"%w: overlapping namespaces %q and %q name different sources %q and %q",
						ErrInvalidPolicy, earlier.Namespace, binding.Namespace,
						earlier.SourceID, binding.SourceID,
					)
				}
			}
		}

		p.bindings = append(p.bindings, binding)
		p.used[binding.SourceID] = struct{}{}
	}

	return p, nil
}

func validateSource(src Source) error {
	if src.ID == "" {
		return fmt.Errorf("empty source ID")
	}
	for _, r := range src.ID {
		if unicode.IsControl(r) || unicode.IsSpace(r) || r == '/' || r == '@' {
			return fmt.Errorf("source ID %q contains an invalid character", src.ID)
		}
	}
	switch src.Kind {
	case SourcePublic, SourcePrivate:
		return nil
	default:
		return fmt.Errorf("source %q has invalid kind %q", src.ID, src.Kind)
	}
}

func normalizeNamespace(namespace string) (string, error) {
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		return "", fmt.Errorf("namespace is empty")
	}
	if ns == "*" {
		return ns, nil
	}
	if strings.HasPrefix(ns, "/") || strings.HasSuffix(ns, "/") {
		return "", fmt.Errorf("namespace %q must not start or end with '/'", namespace)
	}
	for _, r := range ns {
		if unicode.IsControl(r) || unicode.IsSpace(r) || r == '\\' || r == '@' {
			return "", fmt.Errorf("namespace %q contains an invalid character", namespace)
		}
	}
	for _, segment := range strings.Split(ns, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("namespace %q has an invalid path segment", namespace)
		}
	}
	return ns, nil
}

func namespaceOverlaps(a, b string) bool {
	return pathHasPrefix(a, b) || pathHasPrefix(b, a)
}

func pathHasPrefix(path, prefix string) bool {
	if path == prefix {
		return true
	}
	return len(path) > len(prefix) && strings.HasPrefix(path, prefix) && path[len(prefix)] == '/'
}

// SourceFor returns the one source pinned to modulePath. It does not consult
// repositories or package metadata.
func (p *Policy) SourceFor(modulePath string) (Source, bool) {
	src, _, ok := p.match(modulePath)
	return src, ok
}

func (p *Policy) match(modulePath string) (Source, string, bool) {
	if p == nil || validateModulePath(modulePath) != nil {
		return Source{}, "", false
	}

	bestNamespace := ""
	bestSource := ""
	for _, binding := range p.bindings {
		if binding.Namespace == "*" {
			continue
		}
		if pathHasPrefix(modulePath, binding.Namespace) && len(binding.Namespace) > len(bestNamespace) {
			bestNamespace = binding.Namespace
			bestSource = binding.SourceID
		}
	}
	if bestNamespace == "" {
		bestNamespace = "*"
		bestSource = p.wildcard
	}
	if bestSource == "" {
		return Source{}, "", false
	}
	src, ok := p.sourcesByID[bestSource]
	return src, bestNamespace, ok
}

// Sources returns a copy of the source declarations in fixed declaration order.
func (p *Policy) Sources() []Source {
	if p == nil {
		return nil
	}
	out := make([]Source, len(p.sources))
	copy(out, p.sources)
	return out
}

// Bindings returns a copy of the normalized namespace bindings in fixed
// declaration order.
func (p *Policy) Bindings() []NamespaceBinding {
	if p == nil {
		return nil
	}
	out := make([]NamespaceBinding, len(p.bindings))
	copy(out, p.bindings)
	return out
}

// Source looks up a declared source by ID.
func (p *Policy) Source(id string) (Source, bool) {
	if p == nil {
		return Source{}, false
	}
	src, ok := p.sourcesByID[id]
	return src, ok
}

func (p *Policy) sourceUsed(id string) bool {
	_, ok := p.used[id]
	return ok
}

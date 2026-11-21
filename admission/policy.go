package admission

// Policy is the static admission policy. It combines write-scope grants,
// subtree denials that tighten grants at deeper paths, and trust
// requirements that restrict which signed sources may back protected
// leaves after reference closure.
type Policy struct {
	// Grants maps a principal to the subtree prefixes it may write.
	Grants map[string][]Path

	// Denials maps a principal to subtree prefixes it must never write,
	// even when a broader grant covers them. This is how protection rules
	// tighten permission at a sub-path.
	Denials map[string][]Path

	// Trusted lists protected vantage paths and the principals whose
	// sources are accepted for every effective leaf under each path.
	// Entries are matched by longest matching prefix. If no entry matches,
	// the leaf is unprotected and any provenance is accepted. Build it
	// with AddTrusted rather than appending raw entries.
	Trusted []TrustRule
}

// TrustRule binds a protected subtree to its trusted principals.
type TrustRule struct {
	Path       Path
	Principals []string
}

// AddTrusted registers a protected subtree.
func (p *Policy) AddTrusted(path Path, principals ...string) {
	p.Trusted = append(p.Trusted, TrustRule{
		Path:       clonePath(path),
		Principals: append([]string(nil), principals...),
	})
}

// CanWrite reports whether principal is allowed to mutate the stored
// terminal at path. A grant on an ancestor covers its descendants, while
// a denial on any ancestor (or the path itself) overrides it.
func (p *Policy) CanWrite(principal string, path Path) bool {
	for _, denied := range p.Denials[principal] {
		if pathPrefix(denied, path) {
			return false
		}
	}
	for _, granted := range p.Grants[principal] {
		if pathPrefix(granted, path) {
			return true
		}
	}
	return false
}

// TrustedAt returns the trusted principals for the longest protected
// prefix covering vantage, or nil when the vantage is unprotected.
func (p *Policy) TrustedAt(vantage Path) []string {
	var best Path
	for _, rule := range p.Trusted {
		if pathPrefix(rule.Path, vantage) && (best == nil || len(rule.Path) > len(best)) {
			best = rule.Path
		}
	}
	if best == nil {
		return nil
	}
	for _, rule := range p.Trusted {
		if pathEqual(rule.Path, best) {
			return rule.Principals
		}
	}
	return nil
}

// IsTrusted reports whether every source in closure is signed by a trusted
// principal for the protected vantage path. Unprotected vantages accept
// any closure.
func (p *Policy) IsTrusted(vantage Path, closure []Source) bool {
	allowed := p.TrustedAt(vantage)
	if allowed == nil {
		return true
	}
	ok := map[string]bool{}
	for _, name := range allowed {
		ok[name] = true
	}
	for _, src := range closure {
		if !ok[src.Principal] {
			return false
		}
	}
	return true
}

package netsec

// matchAny reports whether at least one matcher accepts any of the supplied
// values.
func matchAny(matchers []StringMatch, values []string) bool {
	for _, matcher := range matchers {
		for _, value := range values {
			if matcher.Matches(value) {
				return true
			}
		}
	}
	return false
}

// matchPrincipalField evaluates one include/exclude pair against one kind of
// identity. The booleans distinguish "field not used" (nil) from "field used
// with an empty list" (non-nil, zero length): an empty include list matches
// nothing and therefore denies, while an unused field imposes no constraint.
//
// The security-relevant behaviour is in the exclude branch: when no identity
// of the required kind exists (plaintext request without a client certificate,
// or request without a verified JWT), a negative match cannot succeed. It
// evaluates to false and emits a Warning instead of silently matching, which
// closes the "notPrincipals matches empty identity" bypass.
func matchPrincipalField(include, exclude []StringMatch, values []string, warning Warning) (bool, []Warning) {
	var warnings []Warning

	if include != nil {
		if len(include) == 0 || !matchAny(include, values) {
			return false, warnings
		}
	}

	if exclude != nil {
		if len(values) == 0 {
			warnings = append(warnings, warning)
			return false, warnings
		}
		if matchAny(exclude, values) {
			return false, warnings
		}
	}

	return true, warnings
}

// Matches reports whether the source accepts the request. It returns every
// warning produced while doing so, even when another field ultimately decides
// the result, so plaintext bypass attempts stay visible in the outcome.
func (s Source) Matches(req Request, policyName, ruleName string) (bool, []Warning) {
	var warnings []Warning

	ok, w := matchPrincipalField(
		s.Principals, s.NotPrincipals, req.PeerPrincipals,
		Warning{
			Policy:  policyName,
			Rule:    ruleName,
			Field:   "principals",
			Kind:    "peer",
			Message: "notPrincipals cannot match: request has no peer (mTLS SPIFFE) identity; treating as a non-match",
		},
	)
	if !ok {
		return false, append(warnings, w...)
	}
	warnings = append(warnings, w...)

	ok, w = matchPrincipalField(
		s.RequestPrincipals, s.NotRequestPrincipals, req.RequestPrincipals,
		Warning{
			Policy:  policyName,
			Rule:    ruleName,
			Field:   "requestPrincipals",
			Kind:    "request",
			Message: "notRequestPrincipals cannot match: request has no JWT (iss/sub) identity; treating as a non-match",
		},
	)
	if !ok {
		return false, append(warnings, w...)
	}
	warnings = append(warnings, w...)

	return true, warnings
}

// Matches reports whether the rule accepts the request. A rule without sources
// is an unconditional match (Istio semantics). Otherwise the sources are OR-ed
// and warnings from all evaluated sources are accumulated.
func (r Rule) Matches(req Request, policyName string) (bool, []Warning) {
	if len(r.Sources) == 0 {
		return true, nil
	}

	var warnings []Warning
	for _, source := range r.Sources {
		matched, w := source.Matches(req, policyName, r.Name)
		warnings = append(warnings, w...)
		if matched {
			return true, warnings
		}
	}
	return false, warnings
}

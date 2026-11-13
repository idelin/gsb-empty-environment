package netsec

// Token is the authorization service's view of an issued token's relevant
// claims. Audience ("aud") carries the exact resource URIs the token may be
// used against.
type Token struct {
	Subject  string
	Audience []string
}

// IssueToken issues a token whose audience is exactly the requested
// resources.
//
// Every requested resource must be a fragment-free absolute URI that is
// registered with the authorization service. A resource is accepted only when
// its complete URI matches a registered entry; a prefix or an extended path
// is never valid. The issued audience contains the resources verbatim and
// cannot be broader than what was authorized.
func (r *Registry) IssueToken(subject string, resources []string) (Token, error) {
	if len(resources) == 0 {
		return Token{}, ErrEmptyResource
	}

	audience := make([]string, 0, len(resources))
	seen := make(map[string]struct{}, len(resources))
	for _, resource := range resources {
		if err := r.Allow(resource); err != nil {
			return Token{}, err
		}
		if _, dup := seen[resource]; dup {
			continue
		}
		seen[resource] = struct{}{}
		audience = append(audience, resource)
	}

	return Token{Subject: subject, Audience: audience}, nil
}

// VerifyToken confirms that a token may be used against the requested
// resource.
//
// The request is granted only when the token's audience contains the exact
// requested resource string and that resource is registered. Audience values
// are never treated as prefixes, so a token issued for a narrower resource
// cannot be replayed against a wider API that merely shares the prefix.
func (r *Registry) VerifyToken(token Token, resource string) error {
	if err := r.Allow(resource); err != nil {
		return err
	}

	for _, aud := range token.Audience {
		if aud == resource {
			return nil
		}
	}

	return ErrAudienceDenied
}

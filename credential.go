package netsec

import (
	"context"
	"fmt"
	"strings"
)

// PullScope is one digest-scoped repository permission carried by a bearer
// token.
type PullScope struct {
	Repository string
	Digest     Digest
	Actions    []string
}

// BearerToken is a repository credential together with the audience and
// digest-scoped pull permissions that justify sending it.
type BearerToken struct {
	Value     string
	Audiences []string
	Scopes    []PullScope
}

// Allows reports whether the token may be sent to exactly repo for a pull of
// digest. At least one audience must name the host-fixed repository source (or
// its origin), and at least one scope must name the repository, contain the
// exact digest, and include the "pull" action.
func (t *BearerToken) Allows(repo Repository, digest Digest) bool {
	if t == nil || !validBearerValue(t.Value) {
		return false
	}
	if err := digest.Validate(); err != nil {
		return false
	}
	canonical, err := repo.canonical()
	if err != nil {
		return false
	}
	audienceOK := false
	for _, audience := range t.Audiences {
		audience = strings.TrimSuffix(strings.TrimSpace(audience), "/")
		if audience == canonical.Source() ||
			audience == canonical.Origin ||
			audience == canonical.Origin+"/v2/"+canonical.Name {
			audienceOK = true
			break
		}
		if origin, err := canonicalOrigin(audience, true); err == nil && origin == canonical.Origin {
			audienceOK = true
			break
		}
	}
	if !audienceOK {
		return false
	}
	for _, scope := range t.Scopes {
		if scope.Digest != digest {
			continue
		}
		if scope.Repository != canonical.Name &&
			scope.Repository != canonical.Source() &&
			scope.Repository != canonical.Origin+"/v2/"+canonical.Name &&
			scope.Repository != "repository:"+canonical.Name {
			continue
		}
		for _, action := range scope.Actions {
			if action == "pull" {
				return true
			}
		}
	}
	return false
}

func validBearerValue(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if c <= 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// TokenProvider returns the host's repository token for one digest pull. The
// returned token is only sent if Audience and a scope authorize the exact
// repository source for that digest.
type TokenProvider interface {
	Token(ctx context.Context, repo Repository, digest Digest) (*BearerToken, error)
}

// TokenProviderFunc adapts a function to TokenProvider.
type TokenProviderFunc func(ctx context.Context, repo Repository, digest Digest) (*BearerToken, error)

// Token implements TokenProvider.
func (f TokenProviderFunc) Token(ctx context.Context, repo Repository, digest Digest) (*BearerToken, error) {
	if f == nil {
		return nil, nil
	}
	return f(ctx, repo, digest)
}

func authorizeToken(ctx context.Context, provider TokenProvider, repo Repository, digest Digest) (string, error) {
	if provider == nil {
		return "", nil
	}
	token, err := provider.Token(ctx, repo, digest)
	if err != nil {
		return "", fmt.Errorf("obtain repository token: %w", err)
	}
	if token == nil {
		return "", nil
	}
	if !token.Allows(repo, digest) {
		return "", ErrTokenScope
	}
	return "Bearer " + token.Value, nil
}

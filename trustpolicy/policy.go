package trustpolicy

import "fmt"

// Level is the verification level of a trust policy.
type Level string

const (
	// LevelStrict fails on any signature problem, including expiry and
	// unknown revocation status.
	LevelStrict Level = "strict"
	// LevelPermissive tolerates unknown revocation status only; expired or
	// mis-bound signatures still fail.
	LevelPermissive Level = "permissive"
	// LevelAudit never blocks; every finding is recorded only.
	LevelAudit Level = "audit"
)

func validLevel(l Level) bool {
	switch l {
	case LevelStrict, LevelPermissive, LevelAudit:
		return true
	}
	return false
}

// Policy is a single Notation-style trust policy statement.
type Policy struct {
	Name              string
	RegistryScopes    []string
	VerificationLevel Level
}

// Validate checks the policy's scopes and verification level.
func (p Policy) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("trustpolicy: policy with scopes %v has no name", p.RegistryScopes)
	}
	if !validLevel(p.VerificationLevel) {
		return fmt.Errorf("trustpolicy: policy %q: unknown verification level %q", p.Name, p.VerificationLevel)
	}
	if len(p.RegistryScopes) == 0 {
		return fmt.Errorf("trustpolicy: policy %q: no registry scopes", p.Name)
	}
	for _, scope := range p.RegistryScopes {
		if err := ValidateScope(scope); err != nil {
			return fmt.Errorf("trustpolicy: policy %q: %w", p.Name, err)
		}
	}
	return nil
}

// Store selects the policy governing an image. Construct it with NewStore.
type Store struct {
	policies []Policy
}

// NewStore validates policies and returns a Store. Scope overlap between
// specific scopes is not rejected up front (overlap may be partial); it is
// reported as a configuration error when an image actually matches more than
// one specific scope.
func NewStore(policies []Policy) (*Store, error) {
	for _, p := range policies {
		if err := p.Validate(); err != nil {
			return nil, err
		}
	}
	cp := make([]Policy, len(policies))
	copy(cp, policies)
	return &Store{policies: cp}, nil
}

// Select returns the policy governing imageRef. A specific scope match wins
// over the global "*" scope. If more than one specific scope matches, the
// configuration is ambiguous and an *OverlapError is returned. If nothing
// matches, ErrNoMatchingPolicy is returned and the caller must deny.
func (s *Store) Select(imageRef string) (*Policy, error) {
	repo, err := RepositoryOf(imageRef)
	if err != nil {
		return nil, err
	}
	var matched []int
	global := -1
	for i := range s.policies {
		for _, scope := range s.policies[i].RegistryScopes {
			if scope == GlobalScope {
				if global < 0 {
					global = i
				}
				break
			}
			if scopeMatches(scope, repo) {
				matched = append(matched, i)
				break
			}
		}
	}
	switch {
	case len(matched) > 1:
		names := make([]string, len(matched))
		for j, idx := range matched {
			names[j] = s.policies[idx].Name
		}
		return nil, &OverlapError{Repository: repo, Policies: names}
	case len(matched) == 1:
		return &s.policies[matched[0]], nil
	case global >= 0:
		return &s.policies[global], nil
	}
	return nil, fmt.Errorf("%w: %s", ErrNoMatchingPolicy, repo)
}

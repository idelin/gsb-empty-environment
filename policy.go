package netsec

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"sync/atomic"
)

var automaticGeneration atomic.Uint64

// Principal is an opaque identity produced by a successfully published,
// collision-free policy. Its binding fields are unexported so callers cannot
// manufacture a principal for another provider or configuration generation.
type Principal struct {
	providerID string
	generation uint64
	digest     [32]byte
	name       string
}

// Name returns the cloud principal name.
func (p Principal) Name() string {
	return p.name
}

// String returns the cloud principal name.
func (p Principal) String() string {
	return p.name
}

// ProviderID returns the provider binding of the principal.
func (p Principal) ProviderID() string {
	return p.providerID
}

// Generation returns the configuration generation binding of the principal.
func (p Principal) Generation() uint64 {
	return p.generation
}

// Policy is an immutable mapping published only after a complete collision
// audit or an already-found lexicographically smallest collision refusal.
type Policy struct {
	providerID string
	generation uint64
	digest     [32]byte
	snapshot   *configSnapshot
	transducer *transducer

	cacheMu sync.RWMutex
	cache   map[[32]byte]string
}

// Publish validates, audits, and publishes a provider configuration. It
// refuses publication when a collision is found or when the finite audit
// cannot close within AuditBudget.
func Publish(config ProviderConfig) (*Policy, error) {
	snapshot, err := prepareConfig(config)
	if err != nil {
		return nil, err
	}
	collision, err := auditSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	if collision != nil {
		return nil, &CollisionError{Collision: *collision}
	}

	generation := config.Generation
	if generation == 0 {
		generation = automaticGeneration.Add(1)
	}
	digest := snapshot.digest(generation)
	return &Policy{
		providerID: snapshot.providerID,
		generation: generation,
		digest:     digest,
		snapshot:   snapshot,
		transducer: newTransducer(snapshot),
		cache:      make(map[[32]byte]string),
	}, nil
}

// NewPolicy is an alias for Publish.
func NewPolicy(config ProviderConfig) (*Policy, error) {
	return Publish(config)
}

// Compile is an alias for Publish.
func Compile(config ProviderConfig) (*Policy, error) {
	return Publish(config)
}

// ProviderID returns the provider bound to this policy.
func (p *Policy) ProviderID() string {
	if p == nil {
		return ""
	}
	return p.providerID
}

// Generation returns the immutable configuration generation of this policy.
func (p *Policy) Generation() uint64 {
	if p == nil {
		return 0
	}
	return p.generation
}

// ConfigDigest returns the digest binding a principal to this exact
// configuration and generation.
func (p *Policy) ConfigDigest() [32]byte {
	if p == nil {
		return [32]byte{}
	}
	return p.digest
}

// MapToken calls the host verifier for signature and time validation, then
// validates issuer/audience/azp and maps the immutable claims.
func (p *Policy) MapToken(ctx context.Context, rawToken string, verifier TokenVerifier) (Principal, error) {
	if p == nil || p.snapshot == nil {
		return Principal{}, fmt.Errorf("%w: nil policy", ErrInvalidConfig)
	}
	if verifier == nil {
		return Principal{}, fmt.Errorf("%w: nil verifier", ErrVerification)
	}
	claims, err := verifier(ctx, rawToken)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: %w", ErrVerification, err)
	}
	return p.MapVerifiedClaims(claims)
}

// Map is an alias for MapToken.
func (p *Policy) Map(ctx context.Context, rawToken string, verifier TokenVerifier) (Principal, error) {
	return p.MapToken(ctx, rawToken, verifier)
}

// MapVerifiedClaims maps claims whose signature and time checks the host has
// already completed. Claims are treated as immutable.
func (p *Policy) MapVerifiedClaims(claims Claims) (Principal, error) {
	if p == nil || p.snapshot == nil {
		return Principal{}, fmt.Errorf("%w: nil policy", ErrInvalidConfig)
	}
	tuple, err := p.validateClaims(claims)
	if err != nil {
		return Principal{}, err
	}

	key := p.cacheKey(tuple)
	p.cacheMu.RLock()
	name, ok := p.cache[key]
	p.cacheMu.RUnlock()
	if !ok {
		name, err = p.transducer.transform(tuple)
		if err != nil {
			return Principal{}, err
		}
		p.cacheMu.Lock()
		p.cache[key] = name
		p.cacheMu.Unlock()
	}
	return Principal{
		providerID: p.providerID,
		generation: p.generation,
		digest:     p.digest,
		name:       name,
	}, nil
}

// MapClaims is an alias for MapVerifiedClaims.
func (p *Policy) MapClaims(claims Claims) (Principal, error) {
	return p.MapVerifiedClaims(claims)
}

// MapVerified is an alias for MapVerifiedClaims, accepting a context for
// conventional call sites. The context is not used after host verification.
func (p *Policy) MapVerified(_ context.Context, claims Claims) (Principal, error) {
	return p.MapVerifiedClaims(claims)
}

func (p *Policy) validateClaims(claims Claims) (ClaimTuple, error) {
	issuer, ok := claims.String("iss")
	if !ok {
		return nil, fmt.Errorf("%w: iss is missing or not a string", ErrClaims)
	}
	if issuer != p.snapshot.issuer {
		return nil, fmt.Errorf("%w: iss mismatch", ErrClaims)
	}

	audiences, ok := claims.Strings("aud")
	if !ok || len(audiences) == 0 {
		return nil, fmt.Errorf("%w: aud is missing or malformed", ErrClaims)
	}
	allowedAudience := false
	for _, audience := range audiences {
		if audience == "" {
			return nil, fmt.Errorf("%w: aud contains an empty value", ErrClaims)
		}
		if _, allowed := p.snapshot.audienceSet[audience]; allowed {
			allowedAudience = true
		}
	}
	if !allowedAudience {
		return nil, fmt.Errorf("%w: aud does not match an allowed audience", ErrClaims)
	}

	azpValue, hasAZP := claims.lookup("azp")
	if hasAZP {
		azp, isString := azpValue.(string)
		if !isString || azp == "" {
			return nil, fmt.Errorf("%w: azp is not a non-empty string", ErrClaims)
		}
		if _, allowed := p.snapshot.audienceSet[azp]; !allowed {
			return nil, fmt.Errorf("%w: azp is not an allowed audience", ErrClaims)
		}
	}
	if len(audiences) > 1 && !hasAZP {
		return nil, fmt.Errorf("%w: azp is required when aud has multiple values", ErrClaims)
	}

	tuple := make(ClaimTuple, len(p.snapshot.claimsInAudit))
	for _, claim := range p.snapshot.claimsInAudit {
		source, ok := p.snapshot.claimPatterns.SourceFor(claim)
		if !ok {
			return nil, fmt.Errorf("%w: no claim pattern for %q", ErrClaims, claim)
		}
		value, exists := claims.lookup(source)
		if !exists {
			return nil, fmt.Errorf("%w: claim %q is missing", ErrClaims, claim)
		}
		text, isString := value.(string)
		if !isString {
			return nil, fmt.Errorf("%w: claim %q is not a string", ErrClaims, claim)
		}
		if text == "" {
			return nil, fmt.Errorf("%w: claim %q is empty", ErrClaims, claim)
		}
		if !p.snapshot.languages[claim].contains(text) {
			return nil, fmt.Errorf("%w: claim %q is outside its allowed language", ErrClaims, claim)
		}
		tuple[claim] = text
	}
	return tuple, nil
}

func (p *Policy) cacheKey(tuple ClaimTuple) [32]byte {
	hash := sha256.New()
	writeHashString(hash, p.providerID)
	writeHashUint64(hash, p.generation)
	_, _ = hash.Write(p.digest[:])
	writeHashUint64(hash, uint64(len(p.snapshot.claimsInAudit)))
	for _, claim := range p.snapshot.claimsInAudit {
		writeHashString(hash, string(claim))
		writeHashString(hash, tuple[claim])
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result
}

// PermissionFunc is the authorization callback. It receives only an opaque
// Principal that was produced by a published, collision-free policy and
// validated for this policy generation.
type PermissionFunc func(ctx context.Context, principal Principal) (bool, error)

// PermissionCallback is an alias for PermissionFunc.
type PermissionCallback = PermissionFunc

// Authorize validates the principal binding before invoking the permission
// callback. A Principal from another provider, generation, or configuration
// digest is rejected without calling the callback.
func (p *Policy) Authorize(ctx context.Context, principal Principal, decide PermissionFunc) (bool, error) {
	if p == nil || p.snapshot == nil {
		return false, fmt.Errorf("%w: nil policy", ErrInvalidConfig)
	}
	if principal.name == "" ||
		principal.providerID != p.providerID ||
		principal.generation != p.generation ||
		principal.digest != p.digest {
		return false, ErrPrincipalBinding
	}
	if decide == nil {
		return false, fmt.Errorf("%w: nil permission callback", ErrInvalidConfig)
	}
	return decide(ctx, principal)
}

// CheckPermission is an alias for Authorize.
func (p *Policy) CheckPermission(ctx context.Context, principal Principal, decide PermissionFunc) (bool, error) {
	return p.Authorize(ctx, principal, decide)
}

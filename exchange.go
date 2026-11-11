package jwsbridge

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	ErrTenantMismatch  = errors.New("jwsbridge: token tenant does not match policy")
	ErrIssuer          = errors.New("jwsbridge: untrusted issuer")
	ErrAudience        = errors.New("jwsbridge: audience not accepted")
	ErrExpired         = errors.New("jwsbridge: token expired")
	ErrReplayedToken   = errors.New("jwsbridge: token already redeemed")
	ErrEmptyScopeGrant = errors.New("jwsbridge: scope intersection is empty")
)

// VerifyPolicy pins the expected tenant, issuer and audience for one hop of
// the exchange (outer delegation or inner subject).
type VerifyPolicy struct {
	Tenant   string
	Issuer   string
	Audience string
}

// Verifier validates compact JWS tokens against a KeyStore.
type Verifier struct {
	keys *KeyStore
	now  func() time.Time
}

// NewVerifier builds a verifier; now may be nil to use the wall clock.
func NewVerifier(keys *KeyStore, now func() time.Time) *Verifier {
	if now == nil {
		now = time.Now
	}
	return &Verifier{keys: keys, now: now}
}

// Verify enforces, in order: token structure, algorithm, header key usage,
// tenant binding, registered key usage, signature, issuer, audience and
// expiry.
func (v *Verifier) Verify(token string, policy VerifyPolicy) (Claims, error) {
	header, claims, input, sig, err := parseToken(token)
	if err != nil {
		return Claims{}, err
	}
	if header.Alg != AlgorithmEdDSA {
		return Claims{}, fmt.Errorf("%w: %q", ErrAlgorithm, header.Alg)
	}
	if header.Use != KeyUseSign {
		return Claims{}, fmt.Errorf("%w: header use %q", ErrKeyUse, header.Use)
	}
	if claims.Tenant != policy.Tenant {
		return Claims{}, fmt.Errorf("%w: token tenant %q, want %q", ErrTenantMismatch, claims.Tenant, policy.Tenant)
	}
	rec, err := v.keys.Resolve(policy.Tenant, header.Kid)
	if err != nil {
		return Claims{}, err
	}
	if rec.Use != KeyUseSign {
		return Claims{}, fmt.Errorf("%w: registered use %q for kid %q", ErrKeyUse, rec.Use, header.Kid)
	}
	if !ed25519.Verify(rec.Public, input, sig) {
		return Claims{}, ErrSignature
	}
	if claims.Issuer != policy.Issuer {
		return Claims{}, fmt.Errorf("%w: got %q, want %q", ErrIssuer, claims.Issuer, policy.Issuer)
	}
	if !claims.HasAudience(policy.Audience) {
		return Claims{}, fmt.Errorf("%w: %q not in %v", ErrAudience, policy.Audience, claims.Audience)
	}
	if !v.now().Before(claims.ExpiresAt) {
		return Claims{}, fmt.Errorf("%w: expired at %s", ErrExpired, claims.ExpiresAt)
	}
	return claims, nil
}

// ReplayCache remembers redeemed token IDs until their natural expiry.
type ReplayCache struct {
	mu      sync.Mutex
	entries map[string]time.Time
	now     func() time.Time
}

// NewReplayCache builds a replay cache; now may be nil.
func NewReplayCache(now func() time.Time) *ReplayCache {
	if now == nil {
		now = time.Now
	}
	return &ReplayCache{entries: make(map[string]time.Time), now: now}
}

// Redeem marks key as spent. It returns false if the key was already
// redeemed and has not yet expired.
func (rc *ReplayCache) Redeem(key string, expiresAt time.Time) bool {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	now := rc.now()
	for k, exp := range rc.entries {
		if !now.Before(exp) {
			delete(rc.entries, k)
		}
	}
	if _, spent := rc.entries[key]; spent {
		return false
	}
	rc.entries[key] = expiresAt
	return true
}

// ExchangeConfig pins the broker's trust anchors for the exchange.
type ExchangeConfig struct {
	Tenant           string
	DelegationIssuer string
	SubjectIssuer    string
	Audience         string // audience incoming tokens must carry (the broker)
	LocalAudience    string // audience stamped on the issued credential
	TTL              time.Duration
}

// ExchangeHandler redeems a verified (delegation, subject) token pair for a
// short-lived local credential carrying only the intersected scopes.
type ExchangeHandler struct {
	verifier *Verifier
	issuer   *Signer
	cfg      ExchangeConfig
	replay   *ReplayCache
	now      func() time.Time
	nextID   uint64
	mu       sync.Mutex
}

// NewExchangeHandler wires the handler. The issuer signer must belong to
// cfg.Tenant; its key id identifies the local credential issuer.
func NewExchangeHandler(verifier *Verifier, issuer *Signer, cfg ExchangeConfig, replay *ReplayCache, now func() time.Time) *ExchangeHandler {
	if now == nil {
		now = time.Now
	}
	return &ExchangeHandler{verifier: verifier, issuer: issuer, cfg: cfg, replay: replay, now: now}
}

// Exchange verifies both tokens independently (issuer, audience, key usage,
// tenant, signature, expiry), rejects replays, and mints a local credential
// scoped to the intersection of both grants.
func (h *ExchangeHandler) Exchange(delegationToken, subjectToken string) (string, error) {
	delegation, err := h.verifier.Verify(delegationToken, VerifyPolicy{
		Tenant:   h.cfg.Tenant,
		Issuer:   h.cfg.DelegationIssuer,
		Audience: h.cfg.Audience,
	})
	if err != nil {
		return "", fmt.Errorf("jwsbridge: delegation token: %w", err)
	}
	subject, err := h.verifier.Verify(subjectToken, VerifyPolicy{
		Tenant:   h.cfg.Tenant,
		Issuer:   h.cfg.SubjectIssuer,
		Audience: h.cfg.Audience,
	})
	if err != nil {
		return "", fmt.Errorf("jwsbridge: subject token: %w", err)
	}

	scopes := intersectScopes(delegation.Scopes, subject.Scopes)
	if len(scopes) == 0 {
		return "", ErrEmptyScopeGrant
	}

	// Redeem both token IDs before issuing; either one being a replay
	// aborts the exchange.
	expiry := earlierOf(delegation.ExpiresAt, subject.ExpiresAt)
	for _, claims := range []Claims{delegation, subject} {
		key := claims.Tenant + "|" + claims.Issuer + "|" + claims.ID
		if !h.replay.Redeem(key, claims.ExpiresAt) {
			return "", fmt.Errorf("%w: iss %q jti %q", ErrReplayedToken, claims.Issuer, claims.ID)
		}
	}

	h.mu.Lock()
	h.nextID++
	id := h.nextID
	h.mu.Unlock()

	now := h.now()
	localExp := now.Add(h.cfg.TTL)
	if expiry.Before(localExp) {
		localExp = expiry
	}
	return h.issuer.Sign(Claims{
		Issuer:    h.cfg.Tenant + "/broker",
		Subject:   subject.Subject,
		Audience:  []string{h.cfg.LocalAudience},
		Scopes:    scopes,
		ID:        fmt.Sprintf("local-%d", id),
		ExpiresAt: localExp,
	})
}

func intersectScopes(a, b []string) []string {
	set := make(map[string]struct{}, len(a))
	for _, s := range a {
		set[s] = struct{}{}
	}
	seen := make(map[string]struct{}, len(b))
	var out []string
	for _, s := range b {
		if _, ok := set[s]; !ok {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func earlierOf(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

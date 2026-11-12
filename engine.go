package netsec

import (
	"crypto/rand"
	"errors"
	"sync"
	"time"
)

var (
	// ErrInvalidSignature is returned when a token signature does not verify.
	ErrInvalidSignature = errors.New("netsec: invalid token signature")
	// ErrTokenExpired is returned when a token's earliest expiry has passed.
	ErrTokenExpired = errors.New("netsec: token expired")
	// ErrReplay is returned when a delegated token is exchanged more than once.
	ErrReplay = errors.New("netsec: delegated token already redeemed")
	// ErrHopLimit is returned when no delegation hops remain.
	ErrHopLimit = errors.New("netsec: delegation hop limit reached")
	// ErrCycle is returned when a principal repeats on a delegation path.
	ErrCycle = errors.New("netsec: delegation cycle detected")
	// ErrPresenterMismatch is returned when the presenter is not the token's subject.
	ErrPresenterMismatch = errors.New("netsec: presenter is not the token subject")
	// ErrInvalidTTL is returned for a non-positive requested lifetime.
	ErrInvalidTTL = errors.New("netsec: ttl must be positive")
	// ErrUnknownAncestor is returned when an ancestor digest is not known to the engine.
	ErrUnknownAncestor = errors.New("netsec: unknown ancestor digest")
	// ErrAncestryMismatch is returned when a token's ancestor chain does not
	// match the presented delegation path (cross-chain splicing).
	ErrAncestryMismatch = errors.New("netsec: ancestor chain does not match presented path")
	// ErrPathBroken is returned for malformed or empty delegation paths.
	ErrPathBroken = errors.New("netsec: malformed delegation path")
	// ErrIssuerMismatch is returned when a hop was not issued by the prior subject.
	ErrIssuerMismatch = errors.New("netsec: hop issuer is not the previous token subject")
)

// defaultMaxHops bounds how many delegation hops may follow a bootstrap token.
const defaultMaxHops = 5

// nonceLen is the per-token random nonce size.
const nonceLen = 32

// Registry is the verification surface the engine needs from a key directory.
type Registry interface {
	Verifier
	Has(identity Identity) bool
}

// Engine mints and validates delegation paths for an identity host. It is
// safe for concurrent use: every exchange is serialized so redemption, cycle
// checks and token issuance are atomic.
type Engine struct {
	registry Registry
	now      func() time.Time
	maxHops  int

	mu       sync.Mutex
	issued   map[Digest]*Token
	redeemed map[Digest]struct{}
}

// Option customizes an Engine.
type Option func(*Engine)

// WithClock overrides the time source, mainly for deterministic tests.
func WithClock(clock func() time.Time) Option {
	return func(e *Engine) { e.now = clock }
}

// WithMaxHops overrides the default remaining-hops budget of bootstrap tokens.
func WithMaxHops(hops int) Option {
	return func(e *Engine) { e.maxHops = hops }
}

// NewEngine builds an engine backed by the given verification registry.
func NewEngine(registry Registry, opts ...Option) *Engine {
	e := &Engine{
		registry: registry,
		now:      time.Now,
		maxHops:  defaultMaxHops,
		issued:   make(map[Digest]*Token),
		redeemed: make(map[Digest]struct{}),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// IssueBootstrap mints the root token of a delegation path: a host-origin
// credential handed to the first service subject. Bootstrap tokens are not
// single-use; a host may fan them out into independent delegation branches.
func (e *Engine) IssueBootstrap(signer Signer, subject Identity, ttl time.Duration) (*Token, error) {
	if err := signer.Identity().Validate(); err != nil {
		return nil, err
	}
	if err := subject.Validate(); err != nil {
		return nil, err
	}
	if ttl <= 0 {
		return nil, ErrInvalidTTL
	}
	if !e.registry.Has(signer.Identity()) {
		return nil, ErrUnknownIdentity
	}

	now := e.now()
	token := &Token{
		Version:        tokenVersion,
		Bootstrap:      true,
		Issuer:         signer.Identity(),
		Subject:        subject,
		Ancestors:      nil,
		IssuedAt:       now,
		EarliestExpiry: now.Add(ttl),
		RemainingHops:  e.maxHops,
		Nonce:          newNonce(),
	}
	// The root token's content digest (excluding the ChainID and Signature
	// fields) is the unforgeable chain identifier every descendant pins.
	token.ChainID = token.rootDigest()
	if err := e.sign(signer, token); err != nil {
		return nil, err
	}

	e.mu.Lock()
	e.issued[token.Digest()] = token
	e.mu.Unlock()
	return token, nil
}

// Exchange redeems parent (presented by its current subject) and mints a
// delegated token for next. The child inherits the full ancestor digest
// chain, an earliest expiry no later than the parent's, and one fewer
// remaining hop. A delegated parent may only be exchanged once.
func (e *Engine) Exchange(parent *Token, presenter Signer, next Identity, ttl time.Duration) (*Token, error) {
	if parent == nil {
		return nil, ErrPathBroken
	}
	if err := presenter.Identity().Validate(); err != nil {
		return nil, err
	}
	if err := next.Validate(); err != nil {
		return nil, err
	}
	if ttl <= 0 {
		return nil, ErrInvalidTTL
	}
	if err := parent.validate(); err != nil {
		return nil, err
	}
	if !e.registry.Verify(parent.Issuer, parent.signingBytes(), parent.Signature) {
		return nil, ErrInvalidSignature
	}
	if !presenter.Identity().Equal(parent.Subject) {
		return nil, ErrPresenterMismatch
	}
	if parent.RemainingHops <= 0 {
		return nil, ErrHopLimit
	}
	if !e.registry.Has(presenter.Identity()) || !e.registry.Has(next) {
		return nil, ErrUnknownIdentity
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	now := e.now()
	if !parent.EarliestExpiry.After(now) {
		return nil, ErrTokenExpired
	}

	// Bootstrap tokens are host credentials and may seed multiple branches;
	// every delegated token is single-use.
	if !parent.Bootstrap {
		if _, used := e.redeemed[parent.Digest()]; used {
			return nil, ErrReplay
		}
	}

	chain, err := e.ancestorChain(parent)
	if err != nil {
		return nil, err
	}
	if parent.Bootstrap && parent.ChainID != parent.rootDigest() {
		return nil, ErrAncestryMismatch
	}
	if err := e.checkCycle(chain, parent, next); err != nil {
		return nil, err
	}

	ancestors := make([]Digest, 0, len(parent.Ancestors)+1)
	ancestors = append(ancestors, parent.Ancestors...)
	ancestors = append(ancestors, parent.Digest())

	// The child's lifetime can only shrink relative to every ancestor: a hop
	// can never refresh the inherited deadline.
	earliestExpiry := now.Add(ttl)
	if parent.EarliestExpiry.Before(earliestExpiry) {
		earliestExpiry = parent.EarliestExpiry
	}

	child := &Token{
		Version:        tokenVersion,
		Bootstrap:      false,
		ChainID:        parent.ChainID,
		Issuer:         presenter.Identity(),
		Subject:        next,
		Ancestors:      ancestors,
		IssuedAt:       now,
		EarliestExpiry: earliestExpiry,
		RemainingHops:  parent.RemainingHops - 1,
		Nonce:          newNonce(),
	}
	if err := e.sign(presenter, child); err != nil {
		return nil, err
	}

	if !parent.Bootstrap {
		e.redeemed[parent.Digest()] = struct{}{}
	}
	e.issued[child.Digest()] = child
	return child, nil
}

// VerifyPath validates a complete ordered delegation path from bootstrap to
// leaf, including per-token signatures, contiguous ancestor digests, cycle
// freedom, monotonic deadlines and hop accounting. It rejects paths that
// splice tokens from different chains.
func (e *Engine) VerifyPath(tokens []*Token) error {
	if len(tokens) == 0 {
		return ErrPathBroken
	}
	now := e.now()
	seen := make(map[Identity]struct{}, len(tokens))
	// The bootstrap issuer is the first principal on the path: a later hop
	// back into it closes a loop even though it never appears as a subject.
	seen[tokens[0].Issuer] = struct{}{}

	for i, token := range tokens {
		if err := token.validate(); err != nil {
			return err
		}
		switch {
		case i == 0 && !token.Bootstrap:
			return ErrPathBroken
		case i > 0 && token.Bootstrap:
			return ErrPathBroken
		}
		if !e.registry.Verify(token.Issuer, token.signingBytes(), token.Signature) {
			return ErrInvalidSignature
		}
		if !token.EarliestExpiry.After(now) {
			return ErrTokenExpired
		}
		if token.ChainID != tokens[0].ChainID {
			return ErrAncestryMismatch
		}
		if token.Bootstrap && token.ChainID != token.rootDigest() {
			return ErrAncestryMismatch
		}

		if i > 0 {
			parent := tokens[i-1]

			// Every hop must pin the exact, ordered digest chain presented to
			// it; this is the cross-chain splice check and runs before any
			// semantic interpretation of the path.
			if len(token.Ancestors) != i {
				return ErrAncestryMismatch
			}
			for j := 0; j < i; j++ {
				if token.Ancestors[j] != tokens[j].Digest() {
					return ErrAncestryMismatch
				}
			}

			if !token.Issuer.Equal(parent.Subject) {
				return ErrIssuerMismatch
			}
			if token.EarliestExpiry.After(parent.EarliestExpiry) {
				return ErrAncestryMismatch
			}
			if token.IssuedAt.After(parent.EarliestExpiry) {
				return ErrTokenExpired
			}
			if token.RemainingHops != parent.RemainingHops-1 {
				return ErrHopLimit
			}
		}

		if _, dup := seen[token.Subject]; dup {
			return ErrCycle
		}
		seen[token.Subject] = struct{}{}
	}
	return nil
}

// ancestorChain reconstructs the stored tokens referenced by parent's ancestor
// digests and verifies they form one contiguous chain.
func (e *Engine) ancestorChain(parent *Token) ([]*Token, error) {
	chain := make([]*Token, len(parent.Ancestors))
	for i, digest := range parent.Ancestors {
		ancestor, ok := e.issued[digest]
		if !ok {
			return nil, ErrUnknownAncestor
		}
		if ancestor.ChainID != parent.ChainID {
			return nil, ErrAncestryMismatch
		}
		if len(ancestor.Ancestors) != i {
			return nil, ErrAncestryMismatch
		}
		for j := 0; j < i; j++ {
			if ancestor.Ancestors[j] != parent.Ancestors[j] {
				return nil, ErrAncestryMismatch
			}
		}
		chain[i] = ancestor
	}
	if len(chain) > 0 && !chain[0].Bootstrap {
		return nil, ErrAncestryMismatch
	}
	if len(chain) > 0 && chain[0].ChainID != parent.ChainID {
		return nil, ErrAncestryMismatch
	}
	return chain, nil
}

// checkCycle rejects next when the same tenant+service principal already
// appears in the ancestor chain, the parent, or would re-enter immediately.
func (e *Engine) checkCycle(chain []*Token, parent *Token, next Identity) error {
	for _, ancestor := range chain {
		if ancestor.Subject.Equal(next) {
			return ErrCycle
		}
		if ancestor.Issuer.Equal(next) {
			return ErrCycle
		}
	}
	if parent.Subject.Equal(next) {
		return ErrCycle
	}
	if parent.Issuer.Equal(next) {
		return ErrCycle
	}
	return nil
}

func (e *Engine) sign(signer Signer, token *Token) error {
	signature, err := signer.Sign(token.signingBytes())
	if err != nil {
		return err
	}
	token.Signature = signature
	return nil
}

func newNonce() []byte {
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return nonce
}

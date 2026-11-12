package netsec

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
)

// Hop identifies one host in a forwarding chain. HostKey is the SSH
// wire-format host public key blob. An empty Username means "any user"; an
// empty Hostname is allowed when only the host key identity is available.
type Hop struct {
	Hostname string
	Username string
	HostKey  []byte
}

// Chain is an ordered forwarding path. For a registered constraint the first
// hop may be an origin sentinel with an empty HostKey to represent the host
// running the agent. Observed chains never contain that sentinel.
type Chain []Hop

// KeyChain is an alias for Chain.
type KeyChain = Chain

// DestinationChain is an alias for Chain.
type DestinationChain = Chain

// HostChain is an alias for Chain.
type HostChain = Chain

// HostKeyChain is an alias for Chain.
type HostKeyChain = Chain

// ForwardingChain is an alias for Chain.
type ForwardingChain = Chain

// Path is an alias for Chain.
type Path = Chain

func (h Hop) clone() Hop {
	return Hop{
		Hostname: h.Hostname,
		Username: h.Username,
		HostKey:  copyBytes(h.HostKey),
	}
}

func (c Chain) clone() Chain {
	if c == nil {
		return nil
	}
	out := make(Chain, len(c))
	for i := range c {
		out[i] = c[i].clone()
	}
	return out
}

// WithoutOrigin removes a leading origin sentinel (a hop with no host key).
func (c Chain) WithoutOrigin() Chain {
	if len(c) > 0 && len(c[0].HostKey) == 0 {
		return c[1:].clone()
	}
	return c.clone()
}

// Validate checks chain structure: every real hop needs a host key, the
// origin sentinel may only appear first, and the same host key or hostname may
// not occur twice.
func (c Chain) Validate() error {
	for i, hop := range c {
		if len(hop.HostKey) == 0 {
			if i == 0 && len(c) > 1 {
				continue
			}
			return fmt.Errorf("%w: hop %d has no host key", ErrMalformedMessage, i)
		}
		if err := ValidatePublicKeyBlob(hop.HostKey); err != nil {
			return fmt.Errorf("%w: hop %d host key: %v", ErrMalformedMessage, i, err)
		}
		if strings.IndexByte(hop.Hostname, 0) >= 0 || strings.IndexByte(hop.Username, 0) >= 0 {
			return fmt.Errorf("%w: hop %d contains a NUL byte", ErrMalformedMessage, i)
		}
	}
	if len(c) == 0 {
		return fmt.Errorf("%w: empty forwarding chain", ErrMissingSessionBinding)
	}

	for i := range c {
		if len(c[i].HostKey) == 0 {
			continue
		}
		for j := 0; j < i; j++ {
			if len(c[j].HostKey) == 0 {
				continue
			}
			if equalPublicKeys(c[i].HostKey, c[j].HostKey) {
				return fmt.Errorf("%w: host key at hops %d and %d is repeated", ErrDuplicateNode, j, i)
			}
			if c[i].Hostname != "" && c[j].Hostname != "" &&
				strings.EqualFold(c[i].Hostname, c[j].Hostname) {
				return fmt.Errorf("%w: host %q at hops %d and %d is repeated", ErrDuplicateNode, c[i].Hostname, j, i)
			}
		}
	}
	return nil
}

// MatchAllowed reports whether observed matches one of the registered chains
// exactly. finalUsername, when non-empty, replaces the observed final hop's
// username; this is how the username from the signed userauth request is
// checked against the registered destination.
func matchAllowed(observed Chain, finalUsername string, allowed []Chain) (Chain, bool, error) {
	if len(observed) == 0 {
		return nil, false, fmt.Errorf("%w: no bound forwarding hops", ErrMissingSessionBinding)
	}
	if err := observed.Validate(); err != nil {
		return nil, false, err
	}

	effective := observed.clone()
	if finalUsername != "" {
		if effective[len(effective)-1].Username != "" &&
			effective[len(effective)-1].Username != finalUsername {
			return nil, false, fmt.Errorf("%w: final username %q conflicts with bound username %q",
				ErrDestinationMismatch, finalUsername, effective[len(effective)-1].Username)
		}
		effective[len(effective)-1].Username = finalUsername
	}
	if effective[len(effective)-1].Username == "" {
		return nil, false, fmt.Errorf("%w: destination username is not bound", ErrDestinationMismatch)
	}

	var sawCandidate bool
	for _, pattern := range allowed {
		candidate := pattern.WithoutOrigin()
		if err := candidate.Validate(); err != nil {
			continue
		}
		if len(candidate) != len(effective) {
			continue
		}
		sawCandidate = true
		ok := true
		for i := range candidate {
			if !hopMatches(candidate[i], effective[i]) {
				ok = false
				break
			}
		}
		if ok {
			return candidate, true, nil
		}
	}
	if !sawCandidate {
		return nil, false, fmt.Errorf("%w: no registered chain has the observed length", ErrDestinationMismatch)
	}
	return nil, false, fmt.Errorf("%w: observed host-key chain does not match an allowed path", ErrDestinationMismatch)
}

func hopMatches(pattern, observed Hop) bool {
	if !equalPublicKeys(pattern.HostKey, observed.HostKey) {
		return false
	}
	if pattern.Username != "" && pattern.Username != observed.Username {
		return false
	}
	// Hostnames are checked when both sides carry one. A real session-bind
	// message contains only the host key, so the key remains authoritative.
	if pattern.Hostname != "" && observed.Hostname != "" &&
		!strings.EqualFold(pattern.Hostname, observed.Hostname) {
		return false
	}
	return true
}

type keyEntry struct {
	blob   []byte
	chains []Chain
}

// KeyRegistry stores the allowed destination chains registered for each
// signing key.
type KeyRegistry struct {
	mu   sync.RWMutex
	keys map[[sha256.Size]byte]*keyEntry
}

func NewKeyRegistry() *KeyRegistry {
	return &KeyRegistry{keys: make(map[[sha256.Size]byte]*keyEntry)}
}

// NewRegistry is an alias for NewKeyRegistry.
func NewRegistry() *KeyRegistry {
	return NewKeyRegistry()
}

// RegisterKey replaces the constraints registered for keyBlob with the given
// full source-to-destination chains. A chain may start with an empty origin
// sentinel; all other hops require host keys.
func (r *KeyRegistry) RegisterKey(keyBlob []byte, chains ...Chain) error {
	if r == nil {
		return fmt.Errorf("%w: nil key registry", ErrMalformedMessage)
	}
	if err := ValidatePublicKeyBlob(keyBlob); err != nil {
		return fmt.Errorf("%w: invalid signing key: %v", ErrMalformedMessage, err)
	}
	if len(chains) == 0 {
		return fmt.Errorf("%w: no destination chains supplied", ErrMalformedMessage)
	}

	normalized := make([]Chain, 0, len(chains))
	for i, chain := range chains {
		if err := chain.Validate(); err != nil {
			return fmt.Errorf("chain %d: %w", i, err)
		}
		candidate := chain.WithoutOrigin()
		if err := candidate.Validate(); err != nil {
			return fmt.Errorf("chain %d: %w", i, err)
		}
		normalized = append(normalized, candidate)
	}

	id := sha256.Sum256(keyBlob)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.keys == nil {
		r.keys = make(map[[sha256.Size]byte]*keyEntry)
	}
	r.keys[id] = &keyEntry{blob: copyBytes(keyBlob), chains: normalized}
	return nil
}

// Register is an alias for RegisterKey.
func (r *KeyRegistry) Register(keyBlob []byte, chains ...Chain) error {
	return r.RegisterKey(keyBlob, chains...)
}

// AddKeyConstraints appends chains to an existing registration. If the key is
// not registered, it behaves like RegisterKey.
func (r *KeyRegistry) AddKeyConstraints(keyBlob []byte, chains ...Chain) error {
	if len(chains) == 0 {
		return fmt.Errorf("%w: no destination chains supplied", ErrMalformedMessage)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.keys == nil {
		r.keys = make(map[[sha256.Size]byte]*keyEntry)
	}

	id := sha256.Sum256(keyBlob)
	entry, ok := r.keys[id]
	if !ok || !equalPublicKeys(entry.blob, keyBlob) {
		return r.registerKeyLocked(keyBlob, chains)
	}
	for i, chain := range chains {
		if err := chain.Validate(); err != nil {
			return fmt.Errorf("chain %d: %w", i, err)
		}
		candidate := chain.WithoutOrigin()
		if err := candidate.Validate(); err != nil {
			return fmt.Errorf("chain %d: %w", i, err)
		}
		entry.chains = append(entry.chains, candidate)
	}
	return nil
}

func (r *KeyRegistry) registerKeyLocked(keyBlob []byte, chains []Chain) error {
	if err := ValidatePublicKeyBlob(keyBlob); err != nil {
		return fmt.Errorf("%w: invalid signing key: %v", ErrMalformedMessage, err)
	}
	normalized := make([]Chain, 0, len(chains))
	for i, chain := range chains {
		if err := chain.Validate(); err != nil {
			return fmt.Errorf("chain %d: %w", i, err)
		}
		candidate := chain.WithoutOrigin()
		if err := candidate.Validate(); err != nil {
			return fmt.Errorf("chain %d: %w", i, err)
		}
		normalized = append(normalized, candidate)
	}
	id := sha256.Sum256(keyBlob)
	if r.keys == nil {
		r.keys = make(map[[sha256.Size]byte]*keyEntry)
	}
	r.keys[id] = &keyEntry{blob: copyBytes(keyBlob), chains: normalized}
	return nil
}

func (r *KeyRegistry) lookup(keyBlob []byte) (*keyEntry, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: nil key registry", ErrKeyNotFound)
	}
	if len(keyBlob) == 0 {
		return nil, fmt.Errorf("%w: empty key blob", ErrKeyNotFound)
	}
	id := sha256.Sum256(keyBlob)
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.keys[id]
	if !ok || !equalPublicKeys(entry.blob, keyBlob) {
		return nil, fmt.Errorf("%w: %s", ErrKeyNotFound, shortKeyID(id[:]))
	}
	return &keyEntry{blob: copyBytes(entry.blob), chains: cloneChains(entry.chains)}, nil
}

func cloneChains(in []Chain) []Chain {
	out := make([]Chain, len(in))
	for i := range in {
		out[i] = in[i].clone()
	}
	return out
}

func shortKeyID(id []byte) string {
	if len(id) > 8 {
		id = id[:8]
	}
	return fmt.Sprintf("%x", id)
}

// AllowedChains returns a copy of the chains registered for keyBlob.
func (r *KeyRegistry) AllowedChains(keyBlob []byte) ([]Chain, bool) {
	entry, err := r.lookup(keyBlob)
	if err != nil {
		return nil, false
	}
	return entry.chains, true
}

// UnregisterKey removes a signing key and its constraints.
func (r *KeyRegistry) UnregisterKey(keyBlob []byte) {
	if r == nil || len(keyBlob) == 0 {
		return
	}
	id := sha256.Sum256(keyBlob)
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry, ok := r.keys[id]; ok && equalPublicKeys(entry.blob, keyBlob) {
		delete(r.keys, id)
	}
}

// ValidateForwardChain checks a complete observed chain against the
// constraints registered for keyBlob. It performs structural checks,
// duplicate-node rejection, and exact full-path matching. It does not itself
// verify session-bind signatures; use a Session or Agent for that.
func (r *KeyRegistry) ValidateForwardChain(keyBlob []byte, observed Chain, finalUsername string) error {
	entry, err := r.lookup(keyBlob)
	if err != nil {
		return err
	}
	if len(observed) == 0 {
		return fmt.Errorf("%w: no forwarding hops were bound", ErrMissingSessionBinding)
	}
	_, ok, err := matchAllowed(observed, finalUsername, entry.chains)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w", ErrDestinationMismatch)
	}
	return nil
}

// CheckChain is an alias for ValidateForwardChain.
func (r *KeyRegistry) CheckChain(keyBlob []byte, observed Chain, finalUsername string) error {
	return r.ValidateForwardChain(keyBlob, observed, finalUsername)
}

// ChainsEqual compares two chains byte-for-byte on all identities.
func ChainsEqual(a, b Chain) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Hostname != b[i].Hostname || a[i].Username != b[i].Username ||
			!bytes.Equal(a[i].HostKey, b[i].HostKey) {
			return false
		}
	}
	return true
}

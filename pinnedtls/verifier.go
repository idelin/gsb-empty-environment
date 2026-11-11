package pinnedtls

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Time is the clock interface used for rule validity evaluation. It is
// satisfied by time.Time, so rule fields are ordinary times while tests can
// inject a fixed clock through Verifier options.
type Time = time.Time

// Clock returns the time against which rule windows are evaluated.
type Clock interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

// Config is an immutable pinning configuration. Build one with NewConfig and
// swap it atomically with Verifier.Update; a Config must never be mutated
// after it has been handed to a Verifier.
type Config struct {
	rules []Rule

	// RequirePins keeps the fail-closed default: when true a connection with
	// no active rule is rejected. Set to false only for services that pin some
	// deployments but intentionally leave others unpinned.
	RequirePins bool
}

// NewConfig validates rules and returns an immutable configuration.
func NewConfig(rules []Rule) (*Config, error) {
	seenNames := make(map[string]struct{}, len(rules))
	for index, rule := range rules {
		if rule.Name == "" {
			return nil, fmt.Errorf("pinnedtls: rule %d has empty name", index)
		}
		if _, exists := seenNames[rule.Name]; exists {
			return nil, fmt.Errorf("pinnedtls: duplicate rule name %q", rule.Name)
		}
		seenNames[rule.Name] = struct{}{}

		if !rule.NotAfter.After(rule.NotBefore) {
			return nil, fmt.Errorf("pinnedtls: rule %q has empty or inverted validity window", rule.Name)
		}
		if len(rule.PrimaryPins) == 0 && len(rule.BackupPins) == 0 {
			return nil, fmt.Errorf("pinnedtls: rule %q declares no pins", rule.Name)
		}
		if err := checkDistinctPins(rule); err != nil {
			return nil, fmt.Errorf("pinnedtls: rule %q: %w", rule.Name, err)
		}
	}

	cloned := make([]Rule, len(rules))
	copy(cloned, rules)
	for index := range cloned {
		cloned[index].PrimaryPins = append([]SPKIPin(nil), rules[index].PrimaryPins...)
		cloned[index].BackupPins = append([]SPKIPin(nil), rules[index].BackupPins...)
	}

	return &Config{rules: cloned, RequirePins: true}, nil
}

// WithRequirePins returns a copy with the fail-closed flag changed.
func (c *Config) WithRequirePins(require bool) *Config {
	clone := *c
	clone.RequirePins = require
	return &clone
}

func checkDistinctPins(rule Rule) error {
	seen := make(map[SPKIPin]struct{}, len(rule.PrimaryPins)+len(rule.BackupPins))
	for _, pin := range rule.PrimaryPins {
		seen[pin] = struct{}{}
	}
	for _, pin := range rule.BackupPins {
		if _, exists := seen[pin]; exists {
			return fmt.Errorf("backup pin %s duplicates a primary pin", pin)
		}
		seen[pin] = struct{}{}
	}
	return nil
}

// Option customizes a Verifier at construction.
type Option func(*Verifier)

// WithClock overrides the wall clock, mainly for tests.
func WithClock(clock Clock) Option {
	return func(v *Verifier) {
		if clock != nil {
			v.clock = clock
		}
	}
}

// Verifier holds an immutable Config behind an atomic pointer and implements
// the VerifyConnection callback. Update swaps the whole configuration in one
// atomic step, so concurrent handshakes always evaluate a consistent snapshot
// and never see a half-rotated rule set.
type Verifier struct {
	config atomic.Pointer[Config]
	clock  Clock

	mu    sync.Mutex
	cache *sessionCache
}

// NewVerifier creates a verifier starting with initialConfig.
func NewVerifier(initialConfig *Config, opts ...Option) (*Verifier, error) {
	if initialConfig == nil {
		return nil, errors.New("pinnedtls: initial config must not be nil")
	}
	verifier := &Verifier{clock: wallClock{}}
	for _, opt := range opts {
		opt(verifier)
	}
	verifier.config.Store(initialConfig)
	return verifier, nil
}

// Update atomically replaces the active configuration. It does not affect
// handshakes already in progress, which keep evaluating the snapshot they
// started with.
func (v *Verifier) Update(next *Config) error {
	if next == nil {
		return errors.New("pinnedtls: replacement config must not be nil")
	}
	v.config.Store(next)
	return nil
}

// Current returns the active immutable configuration.
func (v *Verifier) Current() *Config { return v.config.Load() }

// VerifyConnection is assigned to tls.Config.VerifyConnection by Bind. crypto/tls
// invokes it only after the default certificate-chain and hostname verification
// has succeeded, including on TLS 1.2 and TLS 1.3 session resumption, where the
// chains are restored from the cached session state.
func (v *Verifier) VerifyConnection(state tls.ConnectionState) error {
	now := v.clock.Now()
	active := v.activeRules(now)

	if len(state.VerifiedChains) == 0 {
		return &PinError{
			ServerName: state.ServerName,
			DidResume:  state.DidResume,
			Unwrapped:  ErrEmptyChain,
		}
	}

	chainHashes := flattenChainHashes(state.VerifiedChains)

	if len(active) == 0 {
		if v.Current().RequirePins {
			return &PinError{
				ServerName:  state.ServerName,
				ChainHashes: chainHashes,
				DidResume:   state.DidResume,
				Unwrapped:   ErrNoActiveRule,
			}
		}
		return nil
	}

	ruleNames := make([]string, 0, len(active))
	for _, rule := range active {
		ruleNames = append(ruleNames, rule.Name)
		if ruleMatchesAny(rule, chainHashes) {
			return nil
		}
	}

	return &PinError{
		ServerName:  state.ServerName,
		RuleNames:   ruleNames,
		ChainHashes: chainHashes,
		DidResume:   state.DidResume,
		Unwrapped:   ErrNoMatch,
	}
}

func (v *Verifier) activeRules(now time.Time) []Rule {
	cfg := v.Current()
	active := make([]Rule, 0, len(cfg.rules))
	for _, rule := range cfg.rules {
		if rule.activeAt(now) {
			active = append(active, rule)
		}
	}
	return active
}

func flattenChainHashes(chains [][]*x509.Certificate) []SPKIPin {
	var hashes []SPKIPin
	for _, chain := range chains {
		for _, cert := range chain {
			hashes = append(hashes, SPKISum(cert))
		}
	}
	return hashes
}

// ruleMatchesAny reports whether any hash on the verified chains is pinned by
// the rule. Any certificate position may match: cross-signing means a given
// key may appear at different chain positions depending on which root the
// platform verifier selected, and intermediate pins remain valid across leaf
// rotation.
func ruleMatchesAny(rule Rule, chainHashes []SPKIPin) bool {
	for _, hash := range chainHashes {
		if rule.containsPin(hash) {
			return true
		}
	}
	return false
}

// Bind wires the verifier into a tls.Config and returns a session cache bound
// to this verifier. The returned cache (also assigned to config.ClientSessionCache)
// enables pin-aware resumption handling through EvictSession and the Dialer.
//
// Standard verification stays enabled: InsecureSkipVerify must remain false so
// that VerifyConnection only runs on chains that already pass system-root and
// hostname checks.
func (v *Verifier) Bind(config *tls.Config) (tls.ClientSessionCache, error) {
	if config == nil {
		return nil, errors.New("pinnedtls: tls.Config must not be nil")
	}
	if config.InsecureSkipVerify {
		return nil, errors.New("pinnedtls: InsecureSkipVerify is incompatible with pinning; system chain verification must stay enabled")
	}

	cache := newSessionCache(tls.NewLRUClientSessionCache(32))
	v.mu.Lock()
	v.cache = cache
	v.mu.Unlock()

	config.VerifyConnection = v.VerifyConnection
	config.ClientSessionCache = cache
	return cache, nil
}

// EvictSession removes any cached resumption state for serverName. It is used
// after a resumption fails pinning so the next connection performs a full
// handshake against the current pins.
func (v *Verifier) EvictSession(serverName string) {
	v.mu.Lock()
	cache := v.cache
	v.mu.Unlock()
	if cache != nil {
		cache.Evict(serverName)
	}
}

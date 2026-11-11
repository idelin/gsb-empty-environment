package sshchannel

import (
	"errors"
	"sync"
)

// ErrDenied is returned (directly or wrapped) when a channel open request is
// rejected by policy.
var ErrDenied = errors.New("sshchannel: channel open denied by policy")

// Target identifies the pinned destination of a channel.
type Target interface {
	// Host is the dial hostname or literal IP address.
	Host() string
	// Port is the dial port.
	Port() uint16
}

// Rule is one static allow-list entry. It implements Target so a rule can be
// handed straight to a Dialer.
type Rule struct {
	HostName string
	PortNum  uint16
}

func (r Rule) Host() string { return r.HostName }
func (r Rule) Port() uint16 { return r.PortNum }

// Authorizer decides whether a single channel may open and returns an opaque
// Grant that stays bound to this channel for its whole lifetime.
type Authorizer interface {
	Authorize(host string, port uint16) (*Grant, error)
}

// RevocationChecker re-validates a previously issued grant. The host calls it
// again after the asynchronous dial succeeds and before the channel is
// admitted, so a revocation racing with an in-flight dial cannot be bypassed.
type RevocationChecker interface {
	Revoked(grant *Grant) error
}

// Policy combines the two faces the multiplexer needs.
type Policy interface {
	Authorizer
	RevocationChecker
}

// Grant is the per-channel authorization ticket. Every grant carries the
// generation of the policy that issued it and a unique serial, so bulk
// revocations (generation bump) and single-channel revocations are both
// possible without touching unrelated channels.
type Grant struct {
	Host       string
	Port       uint16
	Generation uint64
	Serial     uint64

	policy *RulePolicy
}

// Target returns the destination pinned at authorization time.
func (g *Grant) Target() Target {
	return Rule{HostName: g.Host, PortNum: g.Port}
}

// Revoke invalidates this single grant. It is safe to call multiple times and
// on grants from a superseded generation.
func (g *Grant) Revoke() {
	g.policy.revoke(g.Serial)
}

// RulePolicy is a generation-aware allow-list Policy. Replacing the rule set
// via SetRules bumps the generation and thereby invalidates every grant issued
// under earlier generations.
type RulePolicy struct {
	mu         sync.RWMutex
	rules      map[string]Rule
	generation uint64
	nextSerial uint64
	revoked    map[uint64]struct{}
	grants     map[uint64]*Grant
}

// NewRulePolicy builds a policy from the given allow-list.
func NewRulePolicy(rules []Rule) *RulePolicy {
	p := &RulePolicy{
		rules:   make(map[string]Rule),
		revoked: make(map[uint64]struct{}),
		grants:  make(map[uint64]*Grant),
	}
	p.replaceLocked(rules)
	return p
}

func ruleKey(host string, port uint16) string {
	return Rule{HostName: host, PortNum: port}.key()
}

func (r Rule) key() string {
	return r.HostName + "/" + itoaPort(r.PortNum)
}

// Generation reports the current policy generation.
func (p *RulePolicy) Generation() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.generation
}

// Authorize implements Authorizer.
func (p *RulePolicy) Authorize(host string, port uint16) (*Grant, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.rules[ruleKey(host, port)]; !ok {
		return nil, ErrDenied
	}
	p.nextSerial++
	grant := &Grant{
		Host:       host,
		Port:       port,
		Generation: p.generation,
		Serial:     p.nextSerial,
		policy:     p,
	}
	p.grants[grant.Serial] = grant
	return grant, nil
}

// Grants returns a snapshot of every grant issued and still remembered by
// the policy. It is primarily intended for host-side introspection and tests.
func (p *RulePolicy) Grants() []*Grant {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*Grant, 0, len(p.grants))
	for _, grant := range p.grants {
		out = append(out, grant)
	}
	return out
}

// Revoked implements RevocationChecker.
func (p *RulePolicy) Revoked(grant *Grant) error {
	if grant == nil || grant.policy != p {
		return ErrDenied
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	if grant.Generation != p.generation {
		return ErrDenied
	}
	if _, dead := p.revoked[grant.Serial]; dead {
		return ErrDenied
	}
	if _, ok := p.rules[ruleKey(grant.Host, grant.Port)]; !ok {
		return ErrDenied
	}
	return nil
}

// SetRules replaces the allow-list and bumps the generation, invalidating all
// grants issued previously.
func (p *RulePolicy) SetRules(rules []Rule) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.replaceLocked(rules)
}

func (p *RulePolicy) replaceLocked(rules []Rule) {
	next := make(map[string]Rule, len(rules))
	for _, rule := range rules {
		next[rule.key()] = rule
	}
	p.rules = next
	p.generation++
}

// RevokeGeneration invalidates every grant of the given generation by bumping
// to a strictly higher generation.
func (p *RulePolicy) RevokeAll() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.generation++
	return p.generation
}

func (p *RulePolicy) revoke(serial uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.revoked[serial] = struct{}{}
}

package netsec

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

const defaultTargetSDK = 28

type configNode struct {
	parent    *configNode
	cleartext *bool
	trust     *TrustAnchors
	pins      *PinSet
	domains   []domainPattern
	children  []*configNode
	depth     int
}

type domainPattern struct {
	name              string
	includeSubdomains bool
}

type domainRule struct {
	node    *configNode
	domain  string
	include bool
	order   int
}

type resolvedConfig struct {
	cleartext bool
	trust     TrustAnchors
	pins      *PinSet
}

// Evaluate computes the effective policy for host.
func (c *Config) Evaluate(host string, opts Options) (Policy, error) {
	normalizedHost, err := normalizeHost(host)
	if err != nil {
		return Policy{}, err
	}
	opts = normalizeOptions(opts)

	node := c.base
	var matched *domainRule
	if rule := c.match(normalizedHost); rule != nil {
		node = rule.node
		matched = rule
	}

	effective := resolve(node, opts.TargetSDK)
	policy := Policy{
		Host:                      normalizedHost,
		CleartextTrafficPermitted: effective.cleartext,
		TrustAnchors:              cloneTrustAnchors(effective.trust),
	}
	if matched != nil {
		policy.MatchedDomain = matched.domain
	}

	if opts.Debug && c.debug != nil && c.debug.trust != nil {
		policy.TrustAnchors = mergeTrustAnchors(policy.TrustAnchors, *c.debug.trust)
		policy.DebugOverridesApplied = true
	}

	if effective.pins != nil {
		policy.PinSet = clonePinSet(effective.pins)
		if effective.pins.Expired(opts.Now) {
			policy.PinSetExpired = true
		} else if len(effective.pins.Pins) > 0 {
			policy.Pinning = true
			policy.Pins = clonePins(effective.pins.Pins)
		}
	}
	return policy, nil
}

// Resolve is an alias for Evaluate.
func (c *Config) Resolve(host string, opts Options) (Policy, error) {
	return c.Evaluate(host, opts)
}

// PolicyFor is an alias for Evaluate.
func (c *Config) PolicyFor(host string, opts Options) (Policy, error) {
	return c.Evaluate(host, opts)
}

func normalizeOptions(opts Options) Options {
	if opts.TargetSDK <= 0 {
		opts.TargetSDK = defaultTargetSDK
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	} else {
		opts.Now = opts.Now.UTC()
	}
	return opts
}

func (c *Config) collectRules(node *configNode) {
	if node.parent != nil {
		node.depth = node.parent.depth + 1
	}
	for _, domain := range node.domains {
		c.rules = append(c.rules, domainRule{
			node:    node,
			domain:  domain.name,
			include: domain.includeSubdomains,
			order:   len(c.rules),
		})
	}
	for _, child := range node.children {
		c.collectRules(child)
	}
}

func (c *Config) match(host string) *domainRule {
	var best *domainRule
	for i := range c.rules {
		rule := &c.rules[i]
		if !rule.matches(host) {
			continue
		}
		if best == nil || rule.moreSpecificThan(best, host) {
			best = rule
		}
	}
	return best
}

func (r *domainRule) matches(host string) bool {
	if host == r.domain {
		return true
	}
	return r.include && strings.HasSuffix(host, "."+r.domain)
}

func (r *domainRule) moreSpecificThan(other *domainRule, host string) bool {
	if len(r.domain) != len(other.domain) {
		return len(r.domain) > len(other.domain)
	}
	rExact := host == r.domain
	otherExact := host == other.domain
	if rExact != otherExact {
		return rExact
	}
	if r.node.depth != other.node.depth {
		return r.node.depth > other.node.depth
	}
	return r.order > other.order
}

func resolve(node *configNode, targetSDK int) resolvedConfig {
	effective := resolvedConfig{
		cleartext: defaultCleartextPermitted(targetSDK),
		trust:     defaultTrustAnchors(targetSDK),
	}

	chain := make([]*configNode, 0, 4)
	for current := node; current != nil; current = current.parent {
		chain = append(chain, current)
	}
	for i := len(chain) - 1; i >= 0; i-- {
		current := chain[i]
		if current.cleartext != nil {
			effective.cleartext = *current.cleartext
		}
		if current.trust != nil {
			effective.trust = cloneTrustAnchors(*current.trust)
		}
		if current.pins != nil {
			effective.pins = current.pins
		}
	}
	return effective
}

func defaultCleartextPermitted(targetSDK int) bool {
	return targetSDK < 28
}

func defaultTrustAnchors(targetSDK int) TrustAnchors {
	trust := TrustAnchors{System: true}
	if targetSDK < 24 {
		trust.User = true
	}
	return trust
}

func mergeTrustAnchors(base, overrides TrustAnchors) TrustAnchors {
	merged := cloneTrustAnchors(base)
	merged.System = merged.System || overrides.System
	merged.User = merged.User || overrides.User
	for _, source := range overrides.Custom {
		merged.Custom = appendUnique(merged.Custom, source)
	}
	return merged
}

func cloneTrustAnchors(trust TrustAnchors) TrustAnchors {
	trust.Custom = append([]string(nil), trust.Custom...)
	return trust
}

func clonePins(pins []Pin) []Pin {
	return append([]Pin(nil), pins...)
}

func clonePinSet(pins *PinSet) *PinSet {
	if pins == nil {
		return nil
	}
	cloned := &PinSet{Pins: clonePins(pins.Pins)}
	if pins.Expiration != nil {
		expiration := *pins.Expiration
		cloned.Expiration = &expiration
	}
	return cloned
}

func parseDomain(raw xmlDomain) (domainPattern, error) {
	domain, err := normalizeDomain(strings.TrimSpace(raw.Value))
	if err != nil {
		return domainPattern{}, err
	}
	return domainPattern{
		name:              domain,
		includeSubdomains: raw.IncludeSubdomains.Set && raw.IncludeSubdomains.Value,
	}, nil
}

func normalizeHost(raw string) (string, error) {
	host := strings.TrimSpace(raw)
	if host == "" {
		return "", fmt.Errorf("netsec: host is empty")
	}
	if strings.Contains(host, "://") {
		parsed, err := url.Parse(host)
		if err != nil {
			return "", fmt.Errorf("netsec: invalid host %q: %w", raw, err)
		}
		host = parsed.Hostname()
	} else if strings.Contains(host, "/") {
		return "", fmt.Errorf("netsec: invalid host %q", raw)
	} else if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	}
	return normalizeDomain(host)
}

func normalizeDomain(raw string) (string, error) {
	domain := strings.TrimSpace(raw)
	domain = strings.TrimSuffix(domain, ".")
	if len(domain) >= 2 && domain[0] == '[' && domain[len(domain)-1] == ']' {
		domain = domain[1 : len(domain)-1]
	}
	domain = strings.ToLower(domain)
	if domain == "" {
		return "", fmt.Errorf("netsec: domain is empty")
	}
	if strings.ContainsAny(domain, " \t\r\n/\\@*") {
		return "", fmt.Errorf("netsec: invalid domain %q", raw)
	}
	return domain, nil
}

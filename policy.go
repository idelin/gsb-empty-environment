package netsec

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
)

// Config controls referral trust, forwarding, transport security, and audit.
type Config struct {
	// TrustedDomains lists DNS suffixes whose hosts are inside the current
	// trust domain. A bare entry is matched exactly or as a label suffix:
	// "example.com" matches "example.com" and "dc1.example.com" but not
	// "example.com.attacker.test".
	TrustedDomains []string

	// TrustedHosts lists additional exact hosts (including IP literals) that
	// are trusted. No DNS resolution is performed.
	TrustedHosts []string

	// AllowedPorts optionally overrides the ports permitted for each scheme.
	// When a scheme has no entry, only its registered default port is allowed
	// (389 for ldap and 636 for ldaps).
	AllowedPorts map[Scheme][]int

	// AllowCredentialForwarding permits simple-bind credentials to be sent to
	// a target that is both trusted and using an allowed port. It is false by
	// default.
	AllowCredentialForwarding bool

	// AllowAnonymousFollow permits following a referral with an anonymous
	// simple bind when credentials may not be forwarded. It is false by
	// default.
	AllowAnonymousFollow bool

	// MaxReferralDepth is the maximum number of referral hops. Zero selects
	// the default of 5.
	MaxReferralDepth int

	// AllowCleartext permits plaintext LDAP. Without it, ldap:// URLs are
	// upgraded with StartTLS before any bind is sent. It never permits a
	// downgrade from LDAPS or StartTLS to cleartext.
	AllowCleartext bool

	// Forest names the local directory forest. When set, referrals to hosts
	// that map to a different forest (or to no known forest) are rejected
	// unless AllowCrossForest is true.
	Forest string

	// DomainForests maps DNS suffixes to forest names. The longest matching
	// suffix wins.
	DomainForests map[string]string

	// AllowCrossForest explicitly permits referrals across forest boundaries.
	AllowCrossForest bool

	// TLSConfig supplies roots and other TLS settings. ServerName is always
	// replaced with the referral host, and InsecureSkipVerify is rejected.
	TLSConfig *tls.Config

	// Timeout bounds dialing, TLS negotiation, and LDAP operations. Zero uses
	// 10 seconds.
	Timeout time.Duration

	// Dialer optionally replaces the built-in LDAP transport. The follower
	// still performs all trust checks before calling Dial.
	Dialer Dialer

	// Audit receives referral decisions and bind events.
	Audit AuditSink
}

const defaultMaxReferralDepth = 5

type forestRule struct {
	domain string
	forest string
}

// Follower evaluates and follows LDAP referrals according to Config.
type Follower struct {
	cfg            Config
	trustedHosts   map[string]struct{}
	trustedDomains []string
	ports          map[Scheme]map[int]struct{}
	forestRules    []forestRule
	dialer         Dialer
}

// NewFollower validates cfg and constructs a Follower. The returned Follower
// is safe for concurrent use; per-request state is kept on the call stack.
func NewFollower(cfg Config) (*Follower, error) {
	if cfg.MaxReferralDepth < 0 {
		return nil, fmt.Errorf("netsec: MaxReferralDepth must not be negative")
	}
	if cfg.MaxReferralDepth == 0 {
		cfg.MaxReferralDepth = defaultMaxReferralDepth
	}
	if cfg.Timeout < 0 {
		return nil, fmt.Errorf("netsec: Timeout must not be negative")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	cfg.Forest = strings.TrimSpace(cfg.Forest)
	if cfg.TLSConfig != nil {
		if cfg.TLSConfig.InsecureSkipVerify {
			return nil, fmt.Errorf("netsec: InsecureSkipVerify is not allowed")
		}
		cfg.TLSConfig = cfg.TLSConfig.Clone()
	}

	f := &Follower{
		cfg:          cfg,
		trustedHosts: make(map[string]struct{}),
		ports:        make(map[Scheme]map[int]struct{}),
	}

	for _, host := range cfg.TrustedHosts {
		normalized, err := NormalizeHost(host)
		if err != nil {
			return nil, fmt.Errorf("netsec: invalid trusted host %q: %w", host, err)
		}
		f.trustedHosts[normalized] = struct{}{}
	}

	seenDomains := make(map[string]struct{})
	for _, domain := range cfg.TrustedDomains {
		normalized, err := normalizeDomain(domain)
		if err != nil {
			return nil, fmt.Errorf("netsec: invalid trusted domain %q: %w", domain, err)
		}
		if _, ok := seenDomains[normalized]; ok {
			continue
		}
		seenDomains[normalized] = struct{}{}
		f.trustedDomains = append(f.trustedDomains, normalized)
	}
	// Longest suffix first makes intent obvious and keeps matching cheap.
	sort.Slice(f.trustedDomains, func(i, j int) bool {
		return len(f.trustedDomains[i]) > len(f.trustedDomains[j])
	})

	for _, scheme := range []Scheme{SchemeLDAP, SchemeLDAPS} {
		entries, ok := cfg.AllowedPorts[scheme]
		if !ok || len(entries) == 0 {
			entries = []int{DefaultPort(scheme)}
		}
		set := make(map[int]struct{}, len(entries))
		for _, port := range entries {
			if port < 1 || port > 65535 {
				return nil, fmt.Errorf("netsec: invalid port %d for %s", port, scheme)
			}
			set[port] = struct{}{}
		}
		f.ports[scheme] = set
	}

	seenForests := make(map[string]string)
	for domain, forest := range cfg.DomainForests {
		normalized, err := normalizeDomain(domain)
		if err != nil {
			return nil, fmt.Errorf("netsec: invalid forest domain %q: %w", domain, err)
		}
		forest = strings.TrimSpace(forest)
		if forest == "" {
			return nil, fmt.Errorf("netsec: empty forest for domain %q", domain)
		}
		if previous, ok := seenForests[normalized]; ok && previous != forest {
			return nil, fmt.Errorf("netsec: domain %q maps to multiple forests", domain)
		}
		seenForests[normalized] = forest
		f.forestRules = append(f.forestRules, forestRule{domain: normalized, forest: forest})
	}
	sort.Slice(f.forestRules, func(i, j int) bool {
		return len(f.forestRules[i].domain) > len(f.forestRules[j].domain)
	})

	if cfg.Dialer == nil {
		f.dialer = &LDAPDialer{
			TLSConfig:      cfg.TLSConfig,
			Timeout:        cfg.Timeout,
			AllowCleartext: cfg.AllowCleartext,
		}
	} else {
		f.dialer = cfg.Dialer
	}
	return f, nil
}

func normalizeDomain(domain string) (string, error) {
	domain = strings.TrimSpace(domain)
	domain = strings.TrimPrefix(domain, ".")
	domain = strings.TrimSuffix(domain, ".")
	if domain == "" {
		return "", fmt.Errorf("empty domain")
	}
	if strings.ContainsAny(domain, "*:/@?#[] \t\r\n") {
		return "", fmt.Errorf("wildcards, ports, and URL syntax are not allowed")
	}
	normalized, err := NormalizeHost(domain)
	if err != nil {
		return "", err
	}
	if net.ParseIP(normalized) != nil {
		return "", fmt.Errorf("IP addresses belong in TrustedHosts, not TrustedDomains")
	}
	return normalized, nil
}

// Action is the policy outcome for a referral.
type Action int

const (
	// FollowWithCredentials rebinds with the caller's credentials.
	FollowWithCredentials Action = iota
	// FollowAnonymous follows with an anonymous simple bind.
	FollowAnonymous
	// DoNotFollow refuses the referral.
	DoNotFollow
)

func (a Action) String() string {
	switch a {
	case FollowWithCredentials:
		return "follow-with-credentials"
	case FollowAnonymous:
		return "follow-anonymous"
	case DoNotFollow:
		return "do-not-follow"
	default:
		return "unknown"
	}
}

// Decision is the result of evaluating one referral.
type Decision struct {
	Action             Action
	Target             Target
	TrustedHost        bool
	PortAllowed        bool
	CredentialsAllowed bool
	CrossForest        bool
	TargetForest       string
	Reason             string
	Err                error
}

// Followable reports whether the referral may be contacted.
func (d Decision) Followable() bool { return d.Action != DoNotFollow }

// EvalState supplies chain context to Evaluate.
type EvalState struct {
	// From is the endpoint that returned the referral. Nil means this is the
	// initial connection.
	From *Target
	// Depth is the number of referral hops already followed. A direct
	// referral has Depth 1.
	Depth int
	// Visited contains Target keys already present on the current chain.
	Visited map[string]struct{}
}

// Evaluate applies trust, port, forest, downgrade, depth, and cycle policy to
// a parsed referral without contacting it.
func (f *Follower) Evaluate(target Referral, state EvalState) Decision {
	if target.Scheme != SchemeLDAP && target.Scheme != SchemeLDAPS {
		return Decision{
			Action: DoNotFollow,
			Target: Target{
				Scheme: target.Scheme,
				Host:   target.Host,
				Port:   target.Port,
				DN:     target.DN,
			},
			Reason: "unsupported referral scheme",
			Err:    ErrUnsupportedScheme,
		}
	}
	mode := f.modeFor(target.Scheme)
	decision := Decision{
		Action: DoNotFollow,
		Target: Target{
			Scheme: target.Scheme,
			Host:   target.Host,
			Port:   target.Port,
			DN:     target.DN,
			Mode:   mode,
		},
	}

	if state.Depth > f.cfg.MaxReferralDepth {
		decision.Reason = "maximum referral depth exceeded"
		decision.Err = ErrMaxReferralDepth
		return decision
	}
	if state.Visited != nil {
		if _, ok := state.Visited[decision.Target.Key()]; ok {
			decision.Reason = "referral target was already visited"
			decision.Err = ErrReferralLoop
			return decision
		}
	}

	fromEncrypted := state.From != nil && (state.From.Mode == TLSModeLDAPS || state.From.Mode == TLSModeStartTLS)
	if fromEncrypted && mode == TLSModeNone {
		decision.Reason = "cleartext referral would downgrade an encrypted connection"
		decision.Err = ErrCleartextDowngrade
		return decision
	}

	decision.TrustedHost = f.hostTrusted(target.Host)
	decision.PortAllowed = f.portAllowed(target.Scheme, target.Port)

	sourceForest := f.cfg.Forest
	if state.From != nil {
		if forest := f.forestFor(state.From.Host); forest != "" {
			sourceForest = forest
		}
	} else if sourceForest == "" {
		sourceForest = f.forestFor(target.Host)
	}
	targetForest := f.forestFor(target.Host)
	if targetForest == "" && f.cfg.Forest != "" && len(f.forestRules) == 0 && decision.TrustedHost {
		targetForest = f.cfg.Forest
	}
	decision.TargetForest = targetForest
	if sourceForest != "" || targetForest != "" {
		decision.CrossForest = sourceForest != targetForest
	}
	if decision.CrossForest && !f.cfg.AllowCrossForest {
		decision.Reason = "referral crosses a directory forest boundary"
		decision.Err = ErrCrossForest
		return decision
	}

	if !decision.TrustedHost {
		if f.cfg.AllowAnonymousFollow {
			decision.Action = FollowAnonymous
			decision.Reason = "anonymous follow: host is outside the trust domain"
			return decision
		}
		decision.Reason = "host is outside the trust domain"
		decision.Err = ErrUntrustedHost
		return decision
	}
	if !decision.PortAllowed {
		if f.cfg.AllowAnonymousFollow {
			decision.Action = FollowAnonymous
			decision.Reason = "anonymous follow: port is not allowed for credential forwarding"
			return decision
		}
		decision.Reason = "port is not allowed"
		decision.Err = ErrPortNotAllowed
		return decision
	}

	decision.CredentialsAllowed = f.cfg.AllowCredentialForwarding
	if decision.CredentialsAllowed {
		decision.Action = FollowWithCredentials
		decision.Reason = "trusted host and allowed port"
		return decision
	}
	if f.cfg.AllowAnonymousFollow {
		decision.Action = FollowAnonymous
		decision.Reason = "anonymous follow: credential forwarding is disabled"
		return decision
	}

	decision.Reason = "credentials may not be forwarded and anonymous follow is disabled"
	decision.Err = ErrCredentialsNotAllowed
	return decision
}

func (f *Follower) modeFor(scheme Scheme) TLSMode {
	switch scheme {
	case SchemeLDAPS:
		return TLSModeLDAPS
	case SchemeLDAP:
		if f.cfg.AllowCleartext {
			return TLSModeNone
		}
		return TLSModeStartTLS
	default:
		return TLSModeNone
	}
}

func (f *Follower) hostTrusted(host string) bool {
	if _, ok := f.trustedHosts[host]; ok {
		return true
	}
	for _, domain := range f.trustedDomains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func (f *Follower) portAllowed(scheme Scheme, port int) bool {
	allowed, ok := f.ports[scheme]
	if !ok {
		return false
	}
	_, ok = allowed[port]
	return ok
}

func (f *Follower) forestFor(host string) string {
	for _, rule := range f.forestRules {
		if host == rule.domain || strings.HasSuffix(host, "."+rule.domain) {
			return rule.forest
		}
	}
	return ""
}

func (f *Follower) record(ctx context.Context, event AuditEvent) {
	if f.cfg.Audit == nil {
		return
	}
	if event.Time.IsZero() {
		event.Time = time.Now()
	}
	f.cfg.Audit.Record(ctx, event)
}

func eventForDecision(source, from string, depth int, decision Decision) AuditEvent {
	event := AuditEvent{
		Time:                time.Now(),
		Kind:                "referral-decision",
		Source:              source,
		From:                from,
		Target:              decision.Target.String(),
		Host:                decision.Target.Host,
		Port:                decision.Target.Port,
		Scheme:              decision.Target.Scheme,
		Transport:           decision.Target.Mode,
		DN:                  decision.Target.DN,
		Followable:          decision.Followable(),
		Action:              decision.Action.String(),
		Reason:              decision.Reason,
		Depth:               depth,
		Forest:              decision.TargetForest,
		CrossForest:         decision.CrossForest,
		CredentialForwarded: decision.Action == FollowWithCredentials,
	}
	return event
}

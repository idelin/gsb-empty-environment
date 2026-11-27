package rpz

import "net/netip"

// Request is the resolver observation evaluated against the policy zones.
type Request struct {
	// ClientIP is the recursive resolver client source address.
	ClientIP netip.Addr
	// QName is the original queried domain name (FQDN or relative).
	QName string
	// CNAMEChain holds every CNAME target in answer order, starting with the
	// target of the original QNAME. The original QName is always checked
	// first even when this slice is populated.
	CNAMEChain []string
	// AnswerIPs lists every A/AAAA address appearing in the answer section.
	AnswerIPs []netip.Addr
	// NameServers lists the authoritative name-server hosts discovered while
	// resolving the QNAME/CNAME chain. Hosts with too few dots are ignored
	// according to MinNSDots.
	NameServers []NSHost
}

// NSHost pairs an authoritative name-server host name with its addresses.
type NSHost struct {
	Name string
	IPs  []netip.Addr
}

// Verdict is the policy outcome for a request.
type Verdict struct {
	// Hit reports whether any rule matched. When false, the resolver must
	// return the original answer untouched.
	Hit bool
	// PassThrough reports an explicit PASSTHRU match: evaluation of all lower
	// priority zones was skipped and the original answer must be returned.
	PassThrough bool
	// Rule is the winning rule, populated when Hit is true.
	Rule *Rule
	// MatchedName is the QNAME-chain name or NS host name that triggered.
	MatchedName string
	// MatchedIP is the client/NS/answer address that triggered an IP rule.
	MatchedIP netip.Addr
}

// Option configures an Engine.
type Option func(*Engine)

// WithMinNSDots sets the minimum number of dots required for an
// authoritative name-server host to be eligible for NSDNAME/NSIP triggers.
// Hosts whose dot count is below the threshold are ignored. The default (2)
// protects root and top-level-domain authorities from accidental policy hits;
// set it to 0 to evaluate every name-server host.
func WithMinNSDots(dots int) Option {
	return func(e *Engine) { e.minNSDots = dots }
}

// Engine evaluates requests against an ordered list of compiled policy zones.
type Engine struct {
	zones     []*Zone
	minNSDots int
}

// NewEngine builds an engine. Zones are evaluated in order: the first zone
// producing any hit decides the query, and a PASSTHRU hit stops all further
// evaluation.
func NewEngine(zones []*Zone, options ...Option) *Engine {
	engine := &Engine{zones: zones, minNSDots: 2}
	for _, option := range options {
		option(engine)
	}
	return engine
}

// Decide applies the policy zones to the request. A nil verdict is never
// returned; callers inspect Verdict.Hit / Verdict.PassThrough.
func (e *Engine) Decide(request Request) Verdict {
	for _, zone := range e.zones {
		if verdict, ok := e.evaluateZone(zone, request); ok {
			return verdict
		}
	}
	return Verdict{}
}

// evaluateZone applies the fixed intra-zone trigger order
// CLIENT-IP > QNAME > NSDNAME > NSIP > IP.
func (e *Engine) evaluateZone(zone *Zone, request Request) (Verdict, bool) {
	if rule := zone.clientIP.Lookup(request.ClientIP); rule != nil {
		return e.verdictFromIP(rule, request.ClientIP, ""), true
	}

	if verdict, ok := e.evaluateQNAME(zone, request); ok {
		return verdict, true
	}

	if verdict, ok := e.evaluateNS(zone, request); ok {
		return verdict, true
	}

	for _, ip := range request.AnswerIPs {
		if rule := zone.answerIP.Lookup(ip); rule != nil {
			return e.verdictFromIP(rule, ip, ""), true
		}
	}

	return Verdict{}, false
}

// evaluateQNAME walks the original name then every CNAME target in order; the
// first hop with a hit wins, mirroring a resolver's processing order.
func (e *Engine) evaluateQNAME(zone *Zone, request Request) (Verdict, bool) {
	names := make([]string, 0, len(request.CNAMEChain)+1)
	names = append(names, request.QName)
	names = append(names, request.CNAMEChain...)

	for _, name := range names {
		labels := SplitLabels(name)
		if len(labels) == 0 {
			continue
		}
		if rule := zone.lookupNameInZone(name, zone.qnameExact, zone.qnameWild); rule != nil {
			verdict := Verdict{Hit: true, Rule: rule, MatchedName: JoinLabels(labels)}
			if rule.Action == ActionPASSTHRU {
				verdict.PassThrough = true
			}
			return verdict, true
		}
	}
	return Verdict{}, false
}

// evaluateNS applies NSDNAME before NSIP and enforces min-ns-dots on the host
// name label count.
func (e *Engine) evaluateNS(zone *Zone, request Request) (Verdict, bool) {
	for _, host := range request.NameServers {
		labels := SplitLabels(host.Name)
		if dottedCount(labels) < e.minNSDots {
			continue
		}
		if rule := zone.lookupNameInZone(host.Name, zone.nsdnameExact, zone.nsdnameWild); rule != nil {
			verdict := Verdict{Hit: true, Rule: rule, MatchedName: JoinLabels(labels)}
			if rule.Action == ActionPASSTHRU {
				verdict.PassThrough = true
			}
			return verdict, true
		}
	}

	for _, host := range request.NameServers {
		if dottedCount(SplitLabels(host.Name)) < e.minNSDots {
			continue
		}
		for _, ip := range host.IPs {
			if rule := zone.nsIP.Lookup(ip); rule != nil {
				return e.verdictFromIP(rule, ip, host.Name), true
			}
		}
	}
	return Verdict{}, false
}

func (e *Engine) verdictFromIP(rule *Rule, ip netip.Addr, hostName string) Verdict {
	verdict := Verdict{Hit: true, Rule: rule, MatchedIP: ip, MatchedName: hostName}
	if rule.Action == ActionPASSTHRU {
		verdict.PassThrough = true
	}
	return verdict
}

// Package rpz implements a Response Policy Zone (RPZ) compiler and policy
// decision engine for recursive DNS resolvers.
//
// The package is deliberately transport agnostic: it consumes normalized
// resolver observations (client address, queried name plus every CNAME hop,
// answer addresses and authoritative name-server hosts) and returns a
// policy verdict. It supports the five RPZ trigger kinds
// (CLIENT-IP, QNAME, NSDNAME, NSIP, IP), the five special policy actions
// encoded as CNAME targets (NXDOMAIN, NODATA, PASSTHRU, DROP, TCP-ONLY),
// local-data replacement (A/AAAA/TXT/CNAME), recursive wildcard matching,
// strict multi-zone / multi-trigger precedence and BIND-style reverse CIDR
// owner encoding including IPv6 "zz" zero-nibble compression.
package rpz

// TriggerKind identifies one of the five RPZ trigger types.
type TriggerKind int

const (
	// TriggerQNAME fires on the original queried name and every CNAME target.
	TriggerQNAME TriggerKind = iota
	// TriggerClientIP fires on the client source address.
	TriggerClientIP
	// TriggerNSDNAME fires on authoritative name-server host names.
	TriggerNSDNAME
	// TriggerNSIP fires on authoritative name-server addresses.
	TriggerNSIP
	// TriggerIP fires on A/AAAA addresses present in the answer section.
	TriggerIP
)

// String returns the canonical RPZ suffix label for the trigger.
func (k TriggerKind) String() string {
	switch k {
	case TriggerClientIP:
		return "rpz-client-ip"
	case TriggerNSDNAME:
		return "rpz-nsdname"
	case TriggerNSIP:
		return "rpz-nsip"
	case TriggerIP:
		return "rpz-ip"
	default:
		return "qname"
	}
}

// Action is the policy decision attached to a matched rule.
type Action int

const (
	// ActionNXDOMAIN answers with NXDOMAIN (CNAME . in RPZ encoding).
	ActionNXDOMAIN Action = iota
	// ActionNODATA answers with NODATA/no-error-empty (CNAME *.).
	ActionNODATA
	// ActionPASSTHRU disables RPZ processing and returns the original answer.
	ActionPASSTHRU
	// ActionDROP drops the query without any response (CNAME rpz-drop.).
	ActionDROP
	// ActionTCPOnly forces a truncated UDP reply so the client retries over TCP.
	ActionTCPOnly
	// ActionLocalData replaces the answer with the rule's local records.
	ActionLocalData
)

// String returns the human-readable action name.
func (a Action) String() string {
	switch a {
	case ActionNXDOMAIN:
		return "NXDOMAIN"
	case ActionNODATA:
		return "NODATA"
	case ActionPASSTHRU:
		return "PASSTHRU"
	case ActionDROP:
		return "DROP"
	case ActionTCPOnly:
		return "TCP-ONLY"
	default:
		return "LOCAL-DATA"
	}
}

// Record is one local-data resource record attached to a rule.
type Record struct {
	// Type is one of A, AAAA, CNAME or TXT.
	Type string
	// Value is the textual RDATA (IP literal, domain name or TXT string).
	Value string
	// TTL is the record time-to-live in seconds.
	TTL uint32
}

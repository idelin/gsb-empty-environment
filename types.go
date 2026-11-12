package netsec

// Action identifies the AuthorizationPolicy action. Evaluation order across
// actions is fixed: CUSTOM, then DENY, then ALLOW.
type Action int

const (
	// ActionAllow is the default action when Action is left as the zero value.
	ActionAllow Action = iota
	// ActionDeny rejects a request as soon as one of its rules matches.
	ActionDeny
	// ActionCustom delegates the decision to a registered external authorizer.
	ActionCustom
)

func (a Action) String() string {
	switch a {
	case ActionDeny:
		return "DENY"
	case ActionCustom:
		return "CUSTOM"
	default:
		return "ALLOW"
	}
}

// Warning records a situation where a negative principal match could not be
// evaluated because the request carried no identity of the required kind.
// The most important case is plaintext traffic: notPrincipals must NOT match
// an empty peer identity, otherwise "allow everyone except service X" turns
// into "allow unauthenticated callers".
type Warning struct {
	// Policy is the Name of the policy that produced the warning.
	Policy string
	// Rule is the Name of the rule that produced the warning.
	Rule string
	// Field is "principals" or "requestPrincipals".
	Field string
	// Kind is "peer" (mTLS SPIFFE identity) or "request" (JWT identity).
	Kind string
	// Message is a human-readable explanation.
	Message string
}

// Source models the from clause of an AuthorizationPolicy rule. A source
// matches when every populated field matches; an empty source matches all
// requests. Include and exclude lists use AND semantics, mirroring Istio:
// the request must match at least one include entry and no exclude entry.
type Source struct {
	// Principals / NotPrincipals match the peer identity, i.e. the SPIFFE ID
	// taken from the verified client certificate (Request.PeerPrincipals).
	Principals    []StringMatch
	NotPrincipals []StringMatch
	// RequestPrincipals / NotRequestPrincipals match the JWT identity, built
	// from the verified token's iss and sub (Request.RequestPrincipals).
	// They never consult the mTLS certificate and vice versa.
	RequestPrincipals    []StringMatch
	NotRequestPrincipals []StringMatch
}

// Rule is one entry in a policy's rules list. A rule matches when at least
// one of its sources matches; a rule without sources matches every request.
type Rule struct {
	Name    string
	Sources []Source
	// Provider selects the external authorizer for CUSTOM policies.
	Provider string
}

// Policy is the importable representation of an AuthorizationPolicy bound to
// a workload. The workload key is supplied when the policy is registered with
// the Evaluator rather than stored on the policy itself.
type Policy struct {
	Name   string
	Action Action
	Rules  []Rule
	// Provider is used by provider-less CUSTOM policies and as the default
	// provider for rules that do not name one.
	Provider string
}

// Request is the authenticated identity material extracted from one inbound
// request before policy evaluation. All identity extraction (certificate
// verification, JWT verification) is assumed to have happened upstream.
type Request struct {
	// Workload identifies the destination workload the policies are bound to.
	Workload string
	// PeerPrincipals holds SPIFFE IDs from the verified client certificate.
	// It is empty for plaintext requests that did not complete mTLS.
	PeerPrincipals []string
	// RequestPrincipals holds identities of verified JWTs in the canonical
	// "iss/sub" form. Use JWTPrincipal to build them.
	RequestPrincipals []string
}

// JWTPrincipal builds the canonical request principal from a verified JWT.
// Istio represents request principals as "<issuer>/<subject>".
func JWTPrincipal(issuer, subject string) string {
	return issuer + "/" + subject
}

// CustomAuthorizer is the hook behind CUSTOM action policies. The returned
// allow flag decides the request; returning false denies it. The authorizer
// receives the same request and the matched rule so it can inspect the
// provider name.
type CustomAuthorizer func(req Request, rule Rule) (allow bool)

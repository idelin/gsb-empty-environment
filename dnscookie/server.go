package dnscookie

import (
	"net/netip"
)

// ServerAction is the outcome of the server-side cookie policy state
// machine for one UDP query.
type ServerAction int

const (
	// ActionAnswer means the query carries a valid Server Cookie (or
	// cookies are not required for it); the caller should send its
	// normal answer.
	ActionAnswer ServerAction = iota
	// ActionBadCookie means the Server Cookie is missing or invalid;
	// Decision.Response carries a BADCOOKIE (RCODE 23) reply with a
	// freshly minted Server Cookie so legitimate clients can retry.
	ActionBadCookie
	// ActionTruncated means the response would exceed the UDP size
	// budget without a valid cookie; Decision.Response is a minimal
	// TC=1 reply forcing the client onto TCP.
	ActionTruncated
	// ActionDrop means the query is malformed; send nothing.
	ActionDrop
)

func (a ServerAction) String() string {
	switch a {
	case ActionAnswer:
		return "ANSWER"
	case ActionBadCookie:
		return "BADCOOKIE"
	case ActionTruncated:
		return "TRUNCATED"
	case ActionDrop:
		return "DROP"
	}
	return "UNKNOWN"
}

// ServerDecision is the policy verdict for one UDP query.
type ServerDecision struct {
	Action ServerAction
	// Response is the wire reply for ActionBadCookie and
	// ActionTruncated; nil for ActionAnswer and ActionDrop.
	Response []byte
	// ServerCookie is the freshly minted cookie attached to BADCOOKIE
	// responses.
	ServerCookie []byte
	// Reason explains the decision for logging.
	Reason string
}

// ServerPolicy implements the RFC 7873/9018 server-side anti-abuse
// state machine: it refuses to send amplifying UDP answers to clients
// that have not proven their source address with a valid Server
// Cookie.
type ServerPolicy struct {
	Keys *KeyRing

	// MaxUDPResponseBytes caps UDP answers. Queries without a valid
	// Server Cookie whose answer would exceed this budget get TC=1.
	// RFC 9018 recommends 1232; zero selects that default.
	MaxUDPResponseBytes int

	// AllowUncookiedQueries permits small answers to queries that
	// carry no COOKIE option at all (legacy clients). If false, every
	// UDP query without a valid Server Cookie is truncated.
	AllowUncookiedQueries bool
}

// NewServerPolicy returns a policy with RFC 9018 defaults.
func NewServerPolicy(keys *KeyRing) *ServerPolicy {
	return &ServerPolicy{
		Keys:                  keys,
		MaxUDPResponseBytes:   1232,
		AllowUncookiedQueries: true,
	}
}

func (p *ServerPolicy) maxUDP() int {
	if p.MaxUDPResponseBytes <= 0 {
		return 1232
	}
	return p.MaxUDPResponseBytes
}

// HandleUDP classifies one UDP query from clientIP whose full answer
// would be answerLen bytes, and returns the policy decision.
func (p *ServerPolicy) HandleUDP(query []byte, clientIP netip.Addr, answerLen int) ServerDecision {
	m, err := ParseMessage(query)
	if err != nil {
		return ServerDecision{Action: ActionDrop, Reason: "unparseable query: " + err.Error()}
	}

	truncated := func(reason string) ServerDecision {
		return ServerDecision{
			Action:   ActionTruncated,
			Response: m.NewResponse(ResponseOptions{Truncated: true}),
			Reason:   reason,
		}
	}
	badCookie := func(reason string) ServerDecision {
		fresh, err := p.Keys.NewServerCookie(m.ClientCookie, clientIP)
		if err != nil {
			return ServerDecision{Action: ActionDrop, Reason: "cannot mint cookie: " + err.Error()}
		}
		return ServerDecision{
			Action: ActionBadCookie,
			Response: m.NewResponse(ResponseOptions{
				RCODE:        RCodeBadCookie,
				ServerCookie: fresh,
			}),
			ServerCookie: fresh,
			Reason:       reason,
		}
	}

	// State 1: no COOKIE option at all (legacy client). Never send a
	// large, amplifying answer to an unverified source address.
	if !m.HasCookieOption {
		if p.AllowUncookiedQueries && answerLen <= p.maxUDP() {
			return ServerDecision{Action: ActionAnswer, Reason: "legacy query, small answer"}
		}
		return truncated("no COOKIE option; forcing TCP")
	}

	// State 2: malformed Client Cookie length.
	if len(m.ClientCookie) != ClientCookieLen {
		return ServerDecision{
			Action:   ActionDrop,
			Response: m.NewResponse(ResponseOptions{RCODE: RCodeFormErr}),
			Reason:   "malformed client cookie length",
		}
	}

	// State 3: Client Cookie present but no Server Cookie yet.
	if len(m.ServerCookie) == 0 {
		return badCookie("missing server cookie")
	}

	// State 4: Server Cookie present; verify hash, timestamp window
	// and source-IP binding against current and previous secrets.
	if err := p.Keys.VerifyServerCookie(m.ServerCookie, m.ClientCookie, clientIP); err != nil {
		return badCookie("invalid server cookie: " + err.Error())
	}

	// State 5: verified client. Still cap oversized UDP answers.
	if answerLen > p.maxUDP() {
		return truncated("answer exceeds UDP budget; forcing TCP")
	}
	return ServerDecision{Action: ActionAnswer, Reason: "valid server cookie"}
}

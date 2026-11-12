package netsec

import "strings"

// Direction identifies the kind of XMPP stream on which a stanza arrived.
type Direction uint8

const (
	// ClientToServer is a stream from a connected client.
	ClientToServer Direction = iota
	// ServerToServer is a stream from another XMPP server.
	ServerToServer
)

// Stream contains the security context of a stanza's input stream.
//
// For a client stream, BoundJID is empty until resource binding succeeds and
// otherwise holds the full JID assigned to the connected resource.
//
// For a server stream, AuthenticatedDomains and ValidatedDomains hold the
// domains established by SASL, TLS, or dialback.  HostedDomains lists the
// domains routed by this server.
type Stream struct {
	Direction Direction

	BoundJID string

	AuthenticatedDomains []string
	ValidatedDomains     []string
	HostedDomains        []string
}

// Action tells the stanza entry point what to do with a stanza.
type Action uint8

const (
	// ActionAccept means that the stanza may be routed.
	ActionAccept Action = iota
	// ActionDrop means that the stanza must be silently discarded.
	ActionDrop
	// ActionCloseStream means that the stream must be closed with the
	// indicated stream error condition.
	ActionCloseStream
)

// ConditionInvalidFrom is the RFC 6120 stream error condition used when a
// stanza's from address is not authorized by the stream.
const ConditionInvalidFrom = "invalid-from"

// Reasons explain a non-accept decision and are useful for logging.
const (
	ReasonNotBound             = "not-bound"
	ReasonInvalidBoundJID      = "invalid-bound-jid"
	ReasonMalformedFrom        = "malformed-from"
	ReasonFromMismatch         = "from-mismatch"
	ReasonMissingFrom          = "missing-from"
	ReasonFromDomainNotTrusted = "from-domain-not-authenticated"
	ReasonMissingTo            = "missing-to"
	ReasonMalformedTo          = "malformed-to"
	ReasonToDomainNotHosted    = "to-domain-not-hosted"
	ReasonUnknownDirection     = "unknown-direction"
)

// Decision is the result of validating one stanza.  Stanza is the input
// stanza with a from address added when the client omitted one.
type Decision struct {
	Action Action
	Stanza Stanza

	// Condition is set when Action is ActionCloseStream.
	Condition string
	// Reason describes why the stanza was dropped or rejected.
	Reason string
}

// Accepted reports whether the stanza may be routed.
func (d Decision) Accepted() bool {
	return d.Action == ActionAccept
}

// Err converts a decision into an error suitable for a stream handler.  It
// returns nil for accepted stanzas and ErrDropped for silently dropped ones.
func (d Decision) Err() error {
	switch d.Action {
	case ActionAccept:
		return nil
	case ActionDrop:
		return ErrDropped
	default:
		return &StreamError{Condition: d.Condition}
	}
}

// NewClientStream creates a client-stream context.  A non-empty boundJID
// indicates that resource binding has completed.
func NewClientStream(boundJID string) Stream {
	return Stream{Direction: ClientToServer, BoundJID: boundJID}
}

// NewServerStream creates a server-to-server context.  The domains in
// authenticated and validated are both trusted for stanza from addresses.
func NewServerStream(authenticated, validated, hosted []string) Stream {
	return Stream{
		Direction:            ServerToServer,
		AuthenticatedDomains: authenticated,
		ValidatedDomains:     validated,
		HostedDomains:        hosted,
	}
}

// Validate applies the RFC 6120 stanza source and destination checks to st.
//
// Client streams:
//   - Before resource binding, every stanza is dropped.
//   - A missing from is replaced with the bound full JID.
//   - A present from must be the bound full JID or its bare JID.  Otherwise
//     the stream is closed with invalid-from.
//
// Server streams:
//   - A present from must have a domain authenticated or validated on the
//     stream.  Otherwise the stream is closed with invalid-from.
//   - A to address whose domain is not hosted locally is dropped.
func (s Stream) Validate(st Stanza) Decision {
	switch s.Direction {
	case ClientToServer:
		return s.validateClient(st)
	case ServerToServer:
		return s.validateServer(st)
	default:
		return drop(st, ReasonUnknownDirection)
	}
}

// ValidateXML parses one XML stanza and applies the stream checks to it.
func (s Stream) ValidateXML(data []byte) (Decision, error) {
	st, err := ParseStanza(data)
	if err != nil {
		return Decision{}, err
	}
	return s.Validate(st), nil
}

func (s Stream) validateClient(st Stanza) Decision {
	if s.BoundJID == "" {
		return drop(st, ReasonNotBound)
	}
	bound, err := ParseJID(s.BoundJID)
	if err != nil {
		return drop(st, ReasonInvalidBoundJID)
	}

	if st.From == "" {
		st.From = s.BoundJID
		return accept(st)
	}

	from, err := ParseJID(st.From)
	if err != nil {
		return invalidFrom(st, ReasonMalformedFrom)
	}
	if from.Resource == "" {
		if !from.sameBare(bound) {
			return invalidFrom(st, ReasonFromMismatch)
		}
		return accept(st)
	}
	if !from.sameFull(bound) {
		return invalidFrom(st, ReasonFromMismatch)
	}
	return accept(st)
}

func (s Stream) validateServer(st Stanza) Decision {
	if st.From == "" {
		return invalidFrom(st, ReasonMissingFrom)
	}
	from, err := ParseJID(st.From)
	if err != nil || from.Domain == "" {
		return invalidFrom(st, ReasonMalformedFrom)
	}
	if !s.trustsDomain(from.Domain) {
		return invalidFrom(st, ReasonFromDomainNotTrusted)
	}

	if st.To == "" {
		return drop(st, ReasonMissingTo)
	}
	to, err := ParseJID(st.To)
	if err != nil || to.Domain == "" {
		return drop(st, ReasonMalformedTo)
	}
	if !domainIn(to.Domain, s.HostedDomains) {
		return drop(st, ReasonToDomainNotHosted)
	}
	return accept(st)
}

func (s Stream) trustsDomain(domain string) bool {
	return domainIn(domain, s.AuthenticatedDomains) ||
		domainIn(domain, s.ValidatedDomains)
}

func domainIn(domain string, domains []string) bool {
	for _, candidate := range domains {
		if strings.EqualFold(strings.TrimSpace(candidate), domain) {
			return true
		}
	}
	return false
}

func accept(st Stanza) Decision {
	return Decision{Action: ActionAccept, Stanza: st}
}

func drop(st Stanza, reason string) Decision {
	return Decision{Action: ActionDrop, Stanza: st, Reason: reason}
}

func invalidFrom(st Stanza, reason string) Decision {
	return Decision{
		Action:    ActionCloseStream,
		Stanza:    st,
		Condition: ConditionInvalidFrom,
		Reason:    reason,
	}
}

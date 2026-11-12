package msglang

// Shared ranked alphabet labels for the wire encoding of a handshake frame.
const (
	LabelFrame     = "frame"    // frame(body, context)
	LabelBody      = "body"     // body(subject, nonce, payload)
	LabelSubject   = "subject"  // typed principal leaf
	LabelNonce     = "nonce"    // typed nonce leaf
	LabelPayload   = "payload"  // typed byte-string leaf
	LabelContext   = "context"  // byte-string leaf carried by the frame
	LabelAckBody   = "ack_body" // distinct body symbol used by the safe variant
	LabelGrantBody = "grant_body"
)

// State names used by the two phase automata.
const (
	ackStateSubject   = "ack:subject"
	ackStateNonce     = "ack:nonce"
	ackStatePayload   = "ack:payload"
	ackStateBody      = "ack:body"
	ackStateContext   = "ack:context"
	ackStateFrame     = "ack:frame"
	grantStateSubject = "grant:subject"
	grantStateNonce   = "grant:nonce"
	grantStatePayload = "grant:payload"
	grantStateBody    = "grant:body"
	grantStateContext = "grant:context"
	grantStateFrame   = "grant:frame"
)

// AmbiguousAcknowledge returns the acknowledgment-phase automaton for the
// vulnerable design: it uses the generic body label, which has the exact same
// encoding shape as the authorization phase.
func AmbiguousAcknowledge() *Automaton {
	return &Automaton{
		Name: "acknowledge",
		States: []string{
			ackStateSubject, ackStateNonce, ackStatePayload,
			ackStateBody, ackStateContext, ackStateFrame,
		},
		Final: []string{ackStateFrame},
		Leaves: []LeafRule{
			{Label: LabelSubject, Kind: Principal, State: ackStateSubject},
			{Label: LabelNonce, Kind: Nonce, State: ackStateNonce},
			{Label: LabelPayload, Kind: ByteString, State: ackStatePayload},
			{Label: LabelContext, Kind: ByteString, State: ackStateContext},
		},
		Rules: []Rule{
			{
				Label: LabelBody,
				Child: []string{ackStateSubject, ackStateNonce, ackStatePayload},
				State: ackStateBody,
			},
			{
				Label: LabelFrame,
				Child: []string{ackStateBody, ackStateContext},
				State: ackStateFrame,
			},
		},
	}
}

// AmbiguousAuthorization returns the authorization-phase automaton for the
// vulnerable design. Its grammar shape is identical to the acknowledgment
// phase, so the common language is non-empty.
func AmbiguousAuthorization() *Automaton {
	return &Automaton{
		Name: "authorization",
		States: []string{
			grantStateSubject, grantStateNonce, grantStatePayload,
			grantStateBody, grantStateContext, grantStateFrame,
		},
		Final: []string{grantStateFrame},
		Leaves: []LeafRule{
			{Label: LabelSubject, Kind: Principal, State: grantStateSubject},
			{Label: LabelNonce, Kind: Nonce, State: grantStateNonce},
			{Label: LabelPayload, Kind: ByteString, State: grantStatePayload},
			{Label: LabelContext, Kind: ByteString, State: grantStateContext},
		},
		Rules: []Rule{
			{
				Label: LabelBody,
				Child: []string{grantStateSubject, grantStateNonce, grantStatePayload},
				State: grantStateBody,
			},
			{
				Label: LabelFrame,
				Child: []string{grantStateBody, grantStateContext},
				State: grantStateFrame,
			},
		},
	}
}

// SafeAcknowledge returns the acknowledgment-phase automaton of the hardened
// design, which uses a phase-specific body label.
func SafeAcknowledge() *Automaton {
	ack := AmbiguousAcknowledge()
	ack.Name = "acknowledge-safe"
	for i := range ack.Rules {
		if ack.Rules[i].Label == LabelBody {
			ack.Rules[i].Label = LabelAckBody
		}
	}
	ack.compiled = nil
	return ack
}

// SafeAuthorization returns the authorization-phase automaton of the hardened
// design, which uses a phase-specific body label.
func SafeAuthorization() *Automaton {
	grant := AmbiguousAuthorization()
	grant.Name = "authorization-safe"
	for i := range grant.Rules {
		if grant.Rules[i].Label == LabelBody {
			grant.Rules[i].Label = LabelGrantBody
		}
	}
	grant.compiled = nil
	return grant
}

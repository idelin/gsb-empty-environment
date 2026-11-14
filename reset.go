package netsec

import "crypto/subtle"

// ResetReason classifies why a packet was or was not accepted as a stateless
// reset. The inspection order matches the receiver-side requirements:
// length -> fixed bit -> short header -> known-header decryption -> token.
type ResetReason string

const (
	ResetAccepted      ResetReason = "accepted"
	ResetTooShort      ResetReason = "too_short"
	ResetBadFixedBit   ResetReason = "fixed_bit_clear"
	ResetLongHeader    ResetReason = "long_header"
	ResetDecryptable   ResetReason = "known_short_header_decrypted"
	ResetNoProbe       ResetReason = "decrypt_probe_required"
	ResetNoToken       ResetReason = "no_matching_token"
	ResetWrongRoute    ResetReason = "wrong_route_partition"
	ResetAmbiguous     ResetReason = "ambiguous_token_collision"
	ResetAlreadyClosed ResetReason = "connection_already_closed"
	ResetLogFailure    ResetReason = "audit_log_failure"
)

// ResetOptions controls stateless reset detection.
//
// Probe must return true when packet can be successfully authenticated and
// decrypted with a known short-header key. Passing a probe is mandatory unless
// CallerVerifiedUndecryptable is set because the QUIC stack already attempted
// decryption and failed. A nil probe without that assertion fails closed.
type ResetOptions struct {
	Route                       RoutePartition
	Probe                       func(packet []byte) bool
	CallerVerifiedUndecryptable bool
}

// ResetDecision reports the strict inspection result. Witness is non-nil only
// when a state transition or security-relevant ambiguous collision event was
// audited.
type ResetDecision struct {
	Matched     bool
	Closed      bool
	Reason      ResetReason
	Connection  string
	CID         []byte
	CIDSequence uint64
	Generation  uint64
	Route       RoutePartition
	Witness     *AuditWitness
	Close       *CloseResult
	Err         error
}

// DetectReset inspects an undecryptable short packet and compares its trailing
// 16 bytes against tokens that are live for this connection and routing
// partition. It never treats malformed, decryptable, cross-partition, or
// colliding packets as a reset.
func (m *Manager) DetectReset(packet []byte, opts ResetOptions) ResetDecision {
	decision := ResetDecision{Route: opts.Route}
	m.mu.Lock()
	defer m.mu.Unlock()

	// 1. Minimum size. RFC 9000 requires at least 21 bytes so the trailing
	// 16-byte token plus header can exist.
	if len(packet) < MinStatelessResetPacketSize {
		decision.Reason = ResetTooShort
		return decision
	}
	// 2. Fixed bit must be set.
	if packet[0]&0x40 == 0 {
		decision.Reason = ResetBadFixedBit
		return decision
	}
	// 3. Reset packets use the short-header form (header form bit clear).
	if packet[0]&0x80 != 0 {
		decision.Reason = ResetLongHeader
		return decision
	}
	// 4. A packet that decrypts with a known short-header key is a normal
	//    packet, even if its tail happens to collide with a token.
	if opts.Probe != nil {
		if opts.Probe(packet) {
			decision.Reason = ResetDecryptable
			return decision
		}
	} else if !opts.CallerVerifiedUndecryptable {
		decision.Reason = ResetNoProbe
		return decision
	}
	// Expire due quarantine entries before consulting the token index, so a
	// token whose retention deadline has passed can never match.
	if err := m.purgeExpiredLocked(); err != nil {
		decision.Reason = ResetLogFailure
		decision.Err = err
		return decision
	}

	// 5. Constant-time comparison of the trailing token. Map lookup locates
	//    candidates, but every candidate comparison is constant time and a
	//    collision is treated as ambiguous (no connection is closed).
	tail := packet[len(packet)-StatelessResetTokenSize:]
	var token ResetToken
	copy(token[:], tail)
	list := m.tokens[token]
	if len(list) == 0 {
		decision.Reason = ResetNoToken
		return decision
	}
	var candidates []*cidRecord
	sawOtherRoute := false
	for _, rec := range list {
		if rec.state == CIDExpired || rec.state == CIDNone {
			continue
		}
		if subtle.ConstantTimeCompare(rec.token[:], tail) != 1 {
			continue
		}
		if rec.route != opts.Route {
			sawOtherRoute = true
			continue
		}
		candidates = append(candidates, rec)
	}
	if len(candidates) == 0 {
		if sawOtherRoute {
			decision.Reason = ResetWrongRoute
		} else {
			decision.Reason = ResetNoToken
		}
		return decision
	}
	if len(candidates) != 1 {
		// A token collision must not be allowed to close an unrelated
		// connection. Fail closed and retain an audit witness.
		w := m.newWitnessLocked(AuditResetAmbiguous, nil, CIDNone, CIDNone, "multiple live CIDs share the trailing token")
		w.Route = opts.Route
		w = m.resealWitnessLocked(w)
		if err := m.commitLocked(w, nil); err != nil {
			decision.Reason = ResetLogFailure
			decision.Err = err
			return decision
		}
		decision.Reason = ResetAmbiguous
		decision.Witness = &w
		return decision
	}

	rec := candidates[0]
	decision.Matched = true
	decision.Connection = m.label
	decision.CID = append([]byte(nil), rec.cid...)
	decision.CIDSequence = rec.sequence
	decision.Generation = rec.generation
	decision.Route = rec.route

	w := m.newWitnessLocked(AuditResetAccepted, rec, rec.state, rec.state, "valid stateless reset token")
	closedBefore := m.closed
	if err := m.commitLocked(w, nil); err != nil {
		decision.Reason = ResetLogFailure
		decision.Err = err
		return decision
	}
	decision.Witness = &w
	cr, err := m.closeLocked("stateless reset received")
	decision.Close = &cr
	if err != nil {
		decision.Closed = m.closed
		decision.Reason = ResetLogFailure
		decision.Err = err
		return decision
	}
	if !closedBefore || cr.CIDsClosed > 0 {
		decision.Closed = true
		decision.Reason = ResetAccepted
	} else {
		// The token belongs to an already closed old connection. Matching it
		// is correct, but it cannot affect any unrelated new connection.
		decision.Reason = ResetAlreadyClosed
	}
	return decision
}

// DetectResetOnly is a convenience for stacks whose decryption path has
// already established that the packet is undecryptable.
func (m *Manager) DetectResetOnly(packet []byte, route RoutePartition) ResetDecision {
	return m.DetectReset(packet, ResetOptions{Route: route, CallerVerifiedUndecryptable: true})
}

// BuildResetPacket constructs a minimal short-header packet ending in token.
// It is useful to the QUIC stack's tests and to peers implementing stateless
// reset without exposing internal token structures.
func BuildResetPacket(header []byte, token ResetToken) []byte {
	h := make([]byte, 0, len(header)+StatelessResetTokenSize)
	h = append(h, header...)
	h = append(h, token[:]...)
	if len(h) < MinStatelessResetPacketSize {
		pad := make([]byte, MinStatelessResetPacketSize-len(h))
		pad[0] = 0x40
		h = append(pad, h...)
	}
	if h[0]&0x40 == 0 {
		h[0] |= 0x40
	}
	h[0] &^= 0x80
	return h
}

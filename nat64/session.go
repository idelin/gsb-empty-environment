package nat64

// newSession registers a flow in both lookup directions and holds the BIB
// reference. For TCP the caller supplies the initial state; UDP/ICMP use
// StateNone. cachedSYN is only set for V4_INIT sessions.
func (e *Engine) newSession(
	proto uint8,
	v6 V6SessionKey,
	v4 V4SessionKey,
	state TCPState,
	b *BIBEntry,
	cachedSYN []byte,
) *Session {
	now := e.now()
	s := &Session{
		Proto:       proto,
		V6:          v6,
		V4:          v4,
		State:       state,
		Created:     now,
		LastUsed:    now,
		CachedV4SYN: cachedSYN,
	}
	s.ExpiresAt = e.sessionDeadline(s, now)
	e.sessV6[v6] = s
	e.sessV4[v4] = s
	e.holdBIB(b)
	return s
}

func (e *Engine) touch(s *Session) {
	now := e.now()
	s.LastUsed = now
	// V4_INIT keeps the fixed 6-second hold rather than being extended by
	// retransmitted SYNs.
	if s.State != StateV4Init {
		s.ExpiresAt = e.sessionDeadline(s, now)
	}
}

// closeSession removes both directions, releases the BIB reference (which
// deletes the binding when the last flow goes away) and, for an expired
// V4_INIT hold, emits the RFC 6146 ICMPv4 unreachable.
func (e *Engine) closeSession(s *Session, reason string) {
	delete(e.sessV6, s.V6)
	delete(e.sessV4, s.V4)

	// The binding is always identified by the initiator's IPv6 tuple.
	// For a V4_INIT hold s.V4.Src is the remote peer, so the IPv4-side key
	// cannot be used; the stable V6 key works for every state.
	b := e.lookupBIBV6(s.Proto, s.V6.Dst, s.V6.DPort)
	if b == nil {
		b = e.lookupBIBV6(s.Proto, s.V6.Src, s.V6.SPort)
	}

	if reason == "v4init-timeout" {
		e.stats.V4InitTimeouts++
		e.sendV4Unreachable13(s, b)
	}

	if b != nil {
		e.releaseBIB(b)
	}
	e.stats.SessionsClosed++
}

// sweep reaps every expired session. V4_INIT expirations trigger the ICMPv4
// Code 13 response; other expirations simply cascade into BIB cleanup.
func (e *Engine) sweep() {
	now := e.now()
	var dead []*Session
	for _, s := range e.sessV4 {
		if !s.ExpiresAt.After(now) {
			dead = append(dead, s)
		}
	}
	for _, s := range dead {
		reason := "timeout"
		if s.State == StateV4Init {
			reason = "v4init-timeout"
		}
		e.closeSession(s, reason)
	}
}

// Sweep is the exported maintenance entry point; callers driving their own
// clock invoke it after advancing time.
func (e *Engine) Sweep() { e.sweep() }

// SessionCount reports the number of live sessions.
func (e *Engine) SessionCount() int { return len(e.sessV4) }

// BIBCount reports the number of live BIB entries.
func (e *Engine) BIBCount() int { return len(e.bibV6) }

// findReverseSession matches inbound return traffic whose five tuple is the
// reverse of the stored outbound tuple (remote -> pool). The mapped port and
// remote port pin the binding uniquely, so distinct EIM flows cannot match.
func (e *Engine) findReverseSession(proto uint8, inbound V4SessionKey) *Session {
	for _, s := range e.sessV4 {
		if s.Proto != proto || s.State == StateV4Init {
			continue
		}
		if s.V4.Src == inbound.Dst && s.V4.Dst == inbound.Src &&
			s.V4.SPort == inbound.DPort && s.V4.DPort == inbound.SPort {
			return s
		}
	}
	return nil
}

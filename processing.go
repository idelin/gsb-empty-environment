package conntrack

import "time"

// validFlags rejects illegal flag combinations before any state lookup:
// SYN+RST, SYN+FIN and RST+FIN are never legal TCP segments.
func validFlags(flags uint8) bool {
	if flags&FlagSYN != 0 && flags&FlagRST != 0 {
		return false
	}
	if flags&FlagSYN != 0 && flags&FlagFIN != 0 {
		return false
	}
	if flags&FlagRST != 0 && flags&FlagFIN != 0 {
		return false
	}
	return true
}

// process runs the state machine for one segment. self is the endpoint the
// segment is addressed to, peer the endpoint that sent it.
func (c *Conn) process(p Packet, self, peer *endpoint, now time.Time) Verdict {
	peer.lastSeen = now

	switch c.state {
	case StateSynSent:
		return c.processSynSent(p, self, peer)
	case StateSynRcvd:
		return c.processSynRcvd(p, self, peer)
	case StateTimeWait:
		return c.processTimeWait(p, self, peer)
	default:
		return c.processSynchronized(p, self, peer)
	}
}

// processSynSent covers the period after the first SYN and before any SYN
// from the peer: ordinary SYN/SYN-ACK exchange, simultaneous open and SYN
// retransmission.
func (c *Conn) processSynSent(p Packet, self, peer *endpoint) Verdict {
	switch {
	case p.Has(FlagRST):
		// RFC 5961: in a non-synchronized state an RST is only acceptable
		// when it acknowledges the outstanding SYN exactly.
		if p.Has(FlagACK) && p.Ack == self.sndNxt {
			return VerdictAccept
		}
		return VerdictDrop

	case p.Has(FlagSYN):
		if p.Has(FlagACK) {
			// SYN-ACK may only arrive at the initiator and must
			// acknowledge its SYN exactly.
			initiator, _ := c.endpoints()
			if self != initiator || p.Ack != initiator.sndNxt {
				return VerdictDrop
			}
			c.registerSyn(p, self, peer)
			self.synAcked = true
			c.state = StateSynRcvd
			return VerdictAccept
		}
		// A bare SYN from the peer is a simultaneous open. A bare SYN from
		// the initiator itself is a retransmission of its opening SYN.
		if peer.synSeen {
			if p.Seq != peer.isn {
				return VerdictDrop
			}
			return VerdictAccept
		}
		c.registerSyn(p, self, peer)
		c.state = StateSynRcvd
		return VerdictAccept

	default:
		// Nothing may cross the connection before both SYNs are seen.
		return VerdictDrop
	}
}

// processSynRcvd handles the interval after both SYNs were exchanged while
// the handshake ACK is outstanding.
func (c *Conn) processSynRcvd(p Packet, self, peer *endpoint) Verdict {
	switch {
	case p.Has(FlagRST):
		// RFC 5961: exact match resets, in-window draws a challenge ACK.
		return c.rstVerdict(p, self)

	case p.Has(FlagSYN):
		// Only a retransmitted SYN/SYN-ACK with the original sequence is
		// legal; a retransmit must not renegotiate the window scale.
		if !peer.synSeen || p.Seq != peer.isn {
			return VerdictDrop
		}
		if p.Has(FlagACK) && p.Ack != self.sndNxt {
			return VerdictDrop
		}
		if p.Has(FlagACK) {
			self.synAcked = true
		}
		if c.handshakeComplete() {
			c.state = StateEstablished
		}
		return VerdictAccept

	default:
		if !p.Has(FlagACK) || p.Ack != self.sndNxt {
			return VerdictDrop
		}
		self.synAcked = true
		// The Window field advertises the sender's (peer's) receive window.
		peer.rcvWnd = peer.scaledWindow(p.Window)

		if !c.handshakeComplete() {
			// Data cannot be delivered until both directions are established.
			if p.Payload > 0 || p.Has(FlagFIN) {
				return VerdictDrop
			}
			return VerdictAccept
		}
		c.state = StateEstablished
		if p.Payload > 0 || p.Has(FlagFIN) {
			return c.processSynchronized(p, self, peer)
		}
		return VerdictAccept
	}
}

// handshakeComplete reports whether both SYNs have been acknowledged.
func (c *Conn) handshakeComplete() bool {
	return c.side[0].synAcked && c.side[1].synAcked
}

// processSynchronized handles ESTABLISHED and every half-close state.
func (c *Conn) processSynchronized(p Packet, self, peer *endpoint) Verdict {
	switch {
	case p.Has(FlagRST):
		return c.rstVerdict(p, self)

	case p.Has(FlagSYN):
		// RFC 5961: a SYN on an established connection never resets it.
		// An in-window SYN draws a challenge ACK, an out-of-window one is
		// dropped.
		if c.segmentInWindow(p, self) {
			return VerdictChallengeACK
		}
		return VerdictDrop

	case p.Has(FlagFIN):
		return c.processFin(p, self, peer)

	default:
		return c.processData(p, self, peer)
	}
}

// rstVerdict implements the RFC 5961 RST policy:
//   - sequence exactly at rcvNxt: accept the reset and tear down;
//   - sequence inside the receive window: challenge ACK, connection kept;
//   - sequence outside the window: drop.
func (c *Conn) rstVerdict(p Packet, self *endpoint) Verdict {
	if p.Seq == self.rcvNxt {
		c.state = StateUnknown
		return VerdictAccept
	}
	if seqInClosedWindow(p.Seq, self.rcvNxt, self.rcvWnd) {
		return VerdictChallengeACK
	}
	return VerdictDrop
}

// processData validates ACK and sequence numbers for ordinary segments and
// advances the tracked window.
func (c *Conn) processData(p Packet, self, peer *endpoint) Verdict {
	// An ACK number outside the receiver's own send window is invalid and
	// draws a challenge ACK.
	if p.Has(FlagACK) && !c.ackAcceptable(p.Ack, self) {
		return VerdictChallengeACK
	}

	end := p.Seq + uint32(p.Payload)
	if !segmentAcceptable(p.Seq, end, self.rcvNxt, self.rcvWnd, p.Payload) {
		return VerdictDrop
	}

	peer.rcvWnd = peer.scaledWindow(p.Window)

	// Track the highest sequence the peer has sent, even for in-window but
	// out-of-order segments. Only the in-order prefix advances rcvNxt.
	if seqGT(end, peer.sndNxt) {
		peer.sndNxt = end
	}
	if p.Payload > 0 && seqLE(p.Seq, self.rcvNxt) && seqGT(end, self.rcvNxt) {
		self.rcvNxt = end
	}

	if p.Has(FlagACK) {
		c.noteAck(p.Ack, self)
	}
	return VerdictAccept
}

// processFin handles FIN segments in synchronized states, including FIN
// retransmissions and data piggybacked on the FIN.
func (c *Conn) processFin(p Packet, self, peer *endpoint) Verdict {
	if p.Has(FlagACK) && !c.ackAcceptable(p.Ack, self) {
		return VerdictChallengeACK
	}

	length := p.Payload + 1 // the FIN consumes one sequence number
	end := p.Seq + uint32(length)
	if peer.finSeen {
		// Retransmitted FIN: its sequence only has to fall inside the
		// still-open receive window, even though rcvNxt has moved past it.
		if !seqInRecentWindow(p.Seq, self.rcvNxt, self.rcvWnd) {
			return VerdictDrop
		}
	} else if !segmentAcceptable(p.Seq, end, self.rcvNxt, self.rcvWnd, length) {
		return VerdictDrop
	}

	peer.rcvWnd = peer.scaledWindow(p.Window)

	if seqGT(end, peer.sndNxt) {
		peer.sndNxt = end
	}

	if peer.finSeen {
		// Retransmitted FIN: accept without changing state again.
		if p.Has(FlagACK) {
			c.noteAck(p.Ack, self)
		}
		return VerdictAccept
	}

	dataEnd := p.Seq + uint32(p.Payload)
	if p.Payload > 0 && seqLE(p.Seq, self.rcvNxt) && seqGT(dataEnd, self.rcvNxt) {
		self.rcvNxt = dataEnd
	}

	// The FIN is only consumed once the stream up to it is in order.
	if dataEnd != self.rcvNxt {
		if p.Has(FlagACK) {
			c.noteAck(p.Ack, self)
		}
		return VerdictAccept
	}

	self.rcvNxt = end
	peer.finSeen = true
	if p.Has(FlagACK) {
		c.noteAck(p.Ack, self)
	}
	c.onFin(peer)
	return VerdictAccept
}

// processTimeWait accepts only retransmissions of the final FIN and pure
// acknowledgments; new data or control attempts are refused.
func (c *Conn) processTimeWait(p Packet, self, peer *endpoint) Verdict {
	switch {
	case p.Has(FlagRST):
		return c.rstVerdict(p, self)
	case p.Has(FlagSYN):
		if c.segmentInWindow(p, self) {
			return VerdictChallengeACK
		}
		return VerdictDrop
	case p.Has(FlagFIN):
		// A retransmitted last FIN must land exactly on the FIN sequence.
		if p.Seq != self.rcvNxt-1 {
			return VerdictDrop
		}
		return VerdictAccept
	default:
		if p.Payload > 0 {
			return VerdictDrop
		}
		if p.Has(FlagACK) && c.ackAcceptable(p.Ack, self) {
			return VerdictAccept
		}
		return VerdictDrop
	}
}

// segmentInWindow reports whether the start of a control segment lies within
// the receiver's closed receive window.
func (c *Conn) segmentInWindow(p Packet, self *endpoint) bool {
	return seqInClosedWindow(p.Seq, self.rcvNxt, self.rcvWnd)
}

// ackAcceptable validates an acknowledgment against the send space of the
// endpoint the segment is addressed to: it must acknowledge something after
// its ISN and not beyond its next sequence.
func (c *Conn) ackAcceptable(ack uint32, sender *endpoint) bool {
	// Distance from the ISN in modular space: a valid ACK of the SYN is
	// isn+1 (distance 1); the largest valid ACK equals sndNxt. Using
	// distances keeps the check correct across sequence wrap-around.
	d := ack - sender.isn
	return d > 0 && d <= sender.sndNxt-sender.isn
}

// noteAck records that data up to ack has been acknowledged. acknowledged is
// the endpoint whose sent data the ACK covers (the receiver of the segment).
func (c *Conn) noteAck(ack uint32, acknowledged *endpoint) {
	if acknowledged.synSeen && !acknowledged.synAcked && ack-acknowledged.isn >= 1 {
		acknowledged.synAcked = true
	}
	if acknowledged.finSeen && !acknowledged.finAcked && ack == acknowledged.sndNxt {
		acknowledged.finAcked = true
	}
	c.advanceCloseState()
}

// registerSyn records a newly observed SYN (or SYN-ACK) sent by peer and
// received by self.
func (c *Conn) registerSyn(p Packet, self, peer *endpoint) {
	peer.synSeen = true
	peer.isn = p.Seq
	peer.sndNxt = p.Seq + 1
	if p.HasWScale {
		ws := p.WScale
		if ws < 0 {
			ws = 0
		}
		if ws > 14 {
			ws = 14
		}
		peer.wscale = uint8(ws)
	}
	// self now expects the peer's first data byte after the SYN.
	self.rcvNxt = p.Seq + 1

	// rcvWnd stores the scaled window used for all post-handshake checks.
	// A bare SYN's window is sent unscaled, but once the scale factor is
	// known the same raw value is interpreted at the negotiated scale.
	peer.rcvWnd = uint32(p.Window) << peer.wscale
}

// onFin runs the connection-level transition when a new FIN from sender has
// been accepted in order.
func (c *Conn) onFin(sender *endpoint) {
	initiator, _ := c.endpoints()
	switch c.state {
	case StateEstablished:
		if sender == initiator {
			c.state = StateFinWait1
		} else {
			c.state = StateCloseWait
		}
	case StateFinWait1:
		if sender != c.finSender() {
			c.state = StateClosing
		}
	case StateFinWait2:
		if sender != c.finSender() {
			c.state = StateTimeWait
		}
	case StateCloseWait:
		if sender == c.finSender() {
			c.state = StateLastAck
		}
	}
	c.advanceCloseState()
}

// finSender returns the endpoint whose FIN drove the current close state.
func (c *Conn) finSender() *endpoint {
	initiator, responder := c.endpoints()
	switch c.state {
	case StateFinWait1, StateFinWait2:
		return initiator
	case StateCloseWait, StateLastAck:
		return responder
	default:
		return nil
	}
}

// advanceCloseState moves the state machine forward once ACKs catch up with
// outstanding FINs.
func (c *Conn) advanceCloseState() {
	initiator, responder := c.endpoints()
	switch c.state {
	case StateFinWait1:
		switch {
		case initiator.finAcked && responder.finSeen:
			c.state = StateTimeWait
		case initiator.finAcked:
			c.state = StateFinWait2
		case responder.finSeen:
			c.state = StateClosing
		}
	case StateClosing:
		if initiator.finAcked && responder.finAcked {
			c.state = StateTimeWait
		}
	case StateLastAck:
		if initiator.finAcked && responder.finAcked {
			c.state = StateTimeWait
		}
	}
}

package sctpaddr

import (
	"crypto/subtle"
	"net/netip"
)

// Dispatch is the entry point invoked by the packet distribution path for
// every inbound SCTP datagram of the association. srcAddr is the transport
// source address of the received packet and is used to refuse deletion of the
// address the peer is currently sending from.
//
// The screening order is deliberate: parse, verification tag, AUTH HMAC,
// strict serial window, and only then the state-changing operation. A dropped
// packet yields KindDropped with a non-nil Err and never a response, so
// unauthenticated traffic cannot influence the association.
func (m *Manager) Dispatch(pkt []byte, srcAddr netip.Addr) Result {
	hdr, chunks, err := ParsePacket(pkt)
	if err != nil {
		return Result{Kind: KindDropped, Err: err}
	}

	// RFC 4960 6.2: every chunk other than INIT carries the tag of the
	// association it belongs to; a mismatched tag is silently discarded.
	if chunks[0].Type != ChunkINIT && hdr.VerificationTag != m.localTag {
		return Result{Kind: KindDropped, Err: ErrBadTag}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// AUTH is only legal as the last chunk of a datagram (RFC 4895 5.1);
	// leading with it can never authenticate anything and is rejected.
	if chunks[0].Type == ChunkAUTH {
		return Result{Kind: KindDropped, Err: ErrMissingAUTH}
	}

	switch chunks[0].Type {
	case ChunkASCONF:
		return m.handleASCONF(hdr, chunks, srcAddr.Unmap())
	case ChunkHeartbeat:
		return m.handleHeartbeatRequest(hdr, chunks)
	case ChunkHeartbeatACK:
		return m.handleHeartbeatACK(chunks)
	default:
		return Result{Kind: KindIgnored}
	}
}

func (m *Manager) handleASCONF(hdr CommonHeader, chunks []Chunk, srcAddr netip.Addr) Result {
	// Exactly one ASCONF followed by one trailing AUTH is accepted. Multiple
	// AUTH chunks, AUTH before the ASCONF, or trailing chunks all fail.
	if len(chunks) != 2 || chunks[1].Type != ChunkAUTH {
		return Result{Kind: KindDropped, Err: ErrMissingAUTH}
	}
	if err := verifyAUTH(m.hmacKey, hdr, chunks); err != nil {
		return Result{Kind: KindDropped, Err: err}
	}

	serial, op, err := parseASCONF(chunks[0])
	if err != nil {
		return Result{Kind: KindDropped, Err: err}
	}

	// The serial must be the exact next expected value. A smaller value is a
	// replay (or a stale duplicate) and a larger one is a gap/jump: both are
	// refused and the counter is left untouched.
	switch {
	case serial < m.expected:
		if serial+1 == m.expected {
			return Result{Kind: KindDropped, Err: ErrReplayedASCONF}
		}
		return Result{Kind: KindDropped, Err: ErrStaleASCONF}
	case serial > m.expected:
		return Result{Kind: KindDropped, Err: ErrFutureASCONF}
	}

	operation, addr, cause, ok := m.applyOperation(op, srcAddr)
	m.expected++

	ack := buildASCONFACK(serial, cause, ok)
	respHdr := CommonHeader{SrcPort: m.localPort, DstPort: hdr.SrcPort, VerificationTag: m.peerTag}
	response, err := signPacket(m.hmacKey, m.hmacID, respHdr, []Chunk{ack})
	if err != nil {
		return Result{Kind: KindDropped, Err: err}
	}
	return Result{
		Kind:     KindASCONFACK,
		Op:       operation,
		Address:  addr,
		Accepted: ok,
		Cause:    cause,
		Response: response,
	}
}

// applyOperation mutates the address set and reports the RFC 6525 error cause
// to place in the ASCONF ACK. The serial has already been validated when this
// runs, so every authenticated request gets an explicit success or failure.
func (m *Manager) applyOperation(op tlv, srcAddr netip.Addr) (Operation, netip.Addr, uint16, bool) {
	var operation Operation
	switch op.typ {
	case ParamASCONFAddIP:
		operation = OpAddIP
	case ParamASCONFDeleteIP:
		operation = OpDeleteIP
	case ParamASCONFSetPrimary:
		operation = OpSetPrimary
	default:
		return 0, netip.Addr{}, CauseUnrecognizedParam, false
	}

	addr, err := innerAddress(op)
	if err != nil {
		return operation, netip.Addr{}, CauseUnrecognizedParam, false
	}

	switch operation {
	case OpAddIP:
		return m.opAddIP(addr)
	case OpDeleteIP:
		return m.opDeleteIP(addr, srcAddr)
	case OpSetPrimary:
		return m.opSetPrimary(addr)
	default:
		return operation, addr, CauseUnrecognizedParam, false
	}
}

func (m *Manager) opAddIP(addr netip.Addr) (Operation, netip.Addr, uint16, bool) {
	if _, ok := m.addrs[addr]; ok {
		// Re-adding an address is idempotent; an unconfirmed one stays
		// unconfirmed until a fresh Probe/HEARTBEAT ACK cycle completes.
		return OpAddIP, addr, 0, true
	}
	m.addrs[addr] = &addrState{confirmed: false}
	return OpAddIP, addr, 0, true
}

func (m *Manager) opDeleteIP(addr, srcAddr netip.Addr) (Operation, netip.Addr, uint16, bool) {
	_, ok := m.addrs[addr]
	if !ok {
		return OpDeleteIP, addr, CauseUnrecognizedParam, false
	}
	// RFC 6525 5.2: removing the sole remaining address is refused.
	if len(m.addrs) == 1 {
		return OpDeleteIP, addr, CauseDeleteLastAddr, false
	}
	// RFC 6525 5.3: the source address of the packet carrying the request may
	// not be removed, since the peer is actively using it.
	if addr == srcAddr {
		return OpDeleteIP, addr, CauseDeleteSourceAddr, false
	}

	delete(m.addrs, addr)
	delete(m.pending, addr)
	if m.primary == addr {
		m.primary = m.failoverPrimary()
	}
	return OpDeleteIP, addr, 0, true
}

func (m *Manager) opSetPrimary(addr netip.Addr) (Operation, netip.Addr, uint16, bool) {
	st, ok := m.addrs[addr]
	if !ok {
		return OpSetPrimary, addr, CauseUnrecognizedParam, false
	}
	// A freshly added address is only a candidate primary after its
	// HEARTBEAT nonce has been echoed back, proving reachability.
	if !st.confirmed {
		return OpSetPrimary, addr, CauseOperationRefused, false
	}
	m.primary = addr
	return OpSetPrimary, addr, 0, true
}

// failoverPrimary chooses a confirmed replacement for a deleted primary; if no
// confirmed address remains it falls back to the smallest surviving address so
// the association always has a deterministic primary.
func (m *Manager) failoverPrimary() netip.Addr {
	var confirmed netip.Addr
	var fallback netip.Addr
	for addr, st := range m.addrs {
		if st.confirmed {
			if !confirmed.IsValid() || addr.Less(confirmed) {
				confirmed = addr
			}
		} else if !fallback.IsValid() || addr.Less(fallback) {
			fallback = addr
		}
	}
	if confirmed.IsValid() {
		m.primary = confirmed
	} else {
		m.primary = fallback
	}
	return m.primary
}

func (m *Manager) handleHeartbeatRequest(hdr CommonHeader, chunks []Chunk) Result {
	if len(chunks) != 1 {
		return Result{Kind: KindDropped, Err: ErrMalformed}
	}
	nonce, err := parseHeartbeatInfo(chunks[0])
	if err != nil {
		return Result{Kind: KindDropped, Err: err}
	}
	ack := buildHeartbeat(ChunkHeartbeatACK, nonce)
	respHdr := CommonHeader{SrcPort: m.localPort, DstPort: hdr.SrcPort, VerificationTag: m.peerTag}
	return Result{Kind: KindHeartbeatACK, Response: marshalPacket(respHdr, ack)}
}

func (m *Manager) handleHeartbeatACK(chunks []Chunk) Result {
	if len(chunks) != 1 {
		return Result{Kind: KindDropped, Err: ErrMalformed}
	}
	nonce, err := parseHeartbeatInfo(chunks[0])
	if err != nil {
		return Result{Kind: KindDropped, Err: err}
	}

	for addr, probe := range m.pending {
		if subtle.ConstantTimeCompare(nonce, probe.nonce) == 1 {
			delete(m.pending, addr)
			if st, ok := m.addrs[addr]; ok {
				st.confirmed = true
			}
			return Result{Kind: KindConfirmed, Op: OpAddIP, Address: addr, Accepted: true}
		}
	}
	// An unknown or superseded nonce is ignored rather than actioned.
	return Result{Kind: KindIgnored}
}

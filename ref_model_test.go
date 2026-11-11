package conntrack

import (
	"net/netip"
)

// This file contains an independent reference implementation of the same
// filtering policy. It deliberately uses a different representation:
// sequence numbers are expanded into an int64 "absolute" sequence space,
// window math never overflows 32 bits, and none of the production helpers in
// sequence.go are used. Differential tests in differential_test.go feed both
// implementations identical generated traces and require identical verdicts.

type refState int

const (
	refUnknown refState = iota
	refSynSent
	refSynRcvd
	refEstablished
	refFinWait1
	refFinWait2
	refCloseWait
	refClosing
	refLastAck
	refTimeWait
)

type refEndpoint struct {
	isn      int64 // absolute sequence of its SYN
	sndNxt   int64 // absolute next sequence to send
	rcvNxt   int64 // absolute next expected inbound sequence
	rcvWnd   int64 // scaled receive window
	wscale   int
	synSeen  bool
	synAcked bool
	finSeen  bool
	finAcked bool
	// epoch anchors uint32 -> int64 expansion.
	epoch uint32
	base  int64
}

// abs expands a raw sequence number into int64 space relative to the
// endpoint's epoch.
func (e *refEndpoint) abs(seq uint32) int64 {
	return e.base + int64(int32(seq-e.epoch))
}

type refConn struct {
	state     refState
	side      [2]*refEndpoint
	initiator int
}

type refTable struct {
	conns map[ConnKey]*refConn
}

func newRefTable() *refTable {
	return &refTable{conns: map[ConnKey]*refConn{}}
}

func refValid(flags uint8) bool {
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

func refSideOf(k ConnKey, ip netip.Addr, port uint16) int {
	if ip == k.IP0 && port == k.Port0 {
		return 0
	}
	if ip == k.IP1 && port == k.Port1 {
		return 1
	}
	return -1
}

func (t *refTable) process(p Packet) Verdict {
	if !refValid(p.Flags) || p.Payload < 0 {
		return VerdictDrop
	}
	k := key(p)
	c, ok := t.conns[k]
	if !ok {
		if p.Flags != FlagSYN {
			return VerdictDrop
		}
		c = t.refNewConn(k, p)
		t.conns[k] = c
		return VerdictAccept
	}
	s := refSideOf(k, p.DstIP, p.DstPort)
	if s < 0 {
		return VerdictDrop
	}
	self := c.side[s]
	peer := c.side[s^1]
	v := c.refProcess(p, self, peer)
	if c.state == refUnknown {
		delete(t.conns, k)
	}
	return v
}

func (t *refTable) refNewConn(k ConnKey, p Packet) *refConn {
	c := &refConn{state: refSynSent, side: [2]*refEndpoint{{}, {}}}
	ini := 0
	if p.SrcIP == k.IP1 && p.SrcPort == k.Port1 {
		ini = 1
	}
	c.initiator = ini
	sender := c.side[ini]
	receiver := c.side[ini^1]

	sender.synSeen = true
	sender.isn = int64(p.Seq)
	sender.sndNxt = int64(p.Seq) + 1
	sender.epoch = p.Seq
	sender.base = int64(p.Seq)
	receiver.epoch = p.Seq
	receiver.base = int64(p.Seq)
	receiver.rcvNxt = int64(p.Seq) + 1
	if p.HasWScale {
		ws := p.WScale
		if ws < 0 {
			ws = 0
		}
		if ws > 14 {
			ws = 14
		}
		sender.wscale = ws
	}
	sender.rcvWnd = int64(p.Window) << sender.wscale
	return c
}

// refRegisterSyn mirrors registerSyn using absolute sequence math.
func (c *refConn) refRegisterSyn(p Packet, self, peer *refEndpoint) {
	peer.synSeen = true
	peer.isn = peer.abs(p.Seq)
	peer.sndNxt = peer.isn + 1
	peer.epoch = p.Seq
	peer.base = peer.isn
	if p.HasWScale {
		ws := p.WScale
		if ws < 0 {
			ws = 0
		}
		if ws > 14 {
			ws = 14
		}
		peer.wscale = ws
	}
	peer.rcvWnd = int64(p.Window) << peer.wscale
	self.epoch = p.Seq
	self.base = peer.isn
	self.rcvNxt = peer.isn + 1
}

func (c *refConn) refProcess(p Packet, self, peer *refEndpoint) Verdict {
	switch c.state {
	case refSynSent:
		return c.refSynSent(p, self, peer)
	case refSynRcvd:
		return c.refSynRcvd(p, self, peer)
	case refTimeWait:
		return c.refTimeWait(p, self, peer)
	default:
		return c.refSynchronized(p, self, peer)
	}
}

func (c *refConn) refSynSent(p Packet, self, peer *refEndpoint) Verdict {
	switch {
	case p.Flags&FlagRST != 0:
		// During the handshake the ACK comparison is on raw sequence
		// values: the receiver's own send space may not have been
		// initialized yet, so do not expand through self.epoch.
		if p.Flags&FlagACK != 0 && uint32(self.sndNxt) == p.Ack {
			return VerdictAccept
		}
		return VerdictDrop
	case p.Flags&FlagSYN != 0:
		if p.Flags&FlagACK != 0 {
			ini := c.side[c.initiator]
			if self != ini || uint32(ini.sndNxt) != p.Ack {
				return VerdictDrop
			}
			c.refRegisterSyn(p, self, peer)
			self.synAcked = true
			c.state = refSynRcvd
			return VerdictAccept
		}
		if peer.synSeen {
			if peer.abs(p.Seq) != peer.isn {
				return VerdictDrop
			}
			return VerdictAccept
		}
		c.refRegisterSyn(p, self, peer)
		c.state = refSynRcvd
		return VerdictAccept
	default:
		return VerdictDrop
	}
}

func (c *refConn) refSynRcvd(p Packet, self, peer *refEndpoint) Verdict {
	switch {
	case p.Flags&FlagRST != 0:
		return c.refRst(p, self)
	case p.Flags&FlagSYN != 0:
		if !peer.synSeen || peer.abs(p.Seq) != peer.isn {
			return VerdictDrop
		}
		if p.Flags&FlagACK != 0 && uint32(self.sndNxt) != p.Ack {
			return VerdictDrop
		}
		if p.Flags&FlagACK != 0 {
			self.synAcked = true
		}
		if c.refHandshakeDone() {
			c.state = refEstablished
		}
		return VerdictAccept
	default:
		if p.Flags&FlagACK == 0 || uint32(self.sndNxt) != p.Ack {
			return VerdictDrop
		}
		self.synAcked = true
		peer.rcvWnd = int64(p.Window) << peer.wscale
		if !c.refHandshakeDone() {
			if p.Payload > 0 || p.Flags&FlagFIN != 0 {
				return VerdictDrop
			}
			return VerdictAccept
		}
		c.state = refEstablished
		if p.Payload > 0 || p.Flags&FlagFIN != 0 {
			return c.refSynchronized(p, self, peer)
		}
		return VerdictAccept
	}
}

func (c *refConn) refHandshakeDone() bool {
	return c.side[0].synAcked && c.side[1].synAcked
}

func (c *refConn) refSynchronized(p Packet, self, peer *refEndpoint) Verdict {
	switch {
	case p.Flags&FlagRST != 0:
		return c.refRst(p, self)
	case p.Flags&FlagSYN != 0:
		if c.refInClosedWindow(self.abs(p.Seq), self) {
			return VerdictChallengeACK
		}
		return VerdictDrop
	case p.Flags&FlagFIN != 0:
		return c.refFin(p, self, peer)
	default:
		return c.refData(p, self, peer)
	}
}

func (c *refConn) refRst(p Packet, self *refEndpoint) Verdict {
	seq := self.abs(p.Seq)
	if seq == self.rcvNxt {
		c.state = refUnknown
		return VerdictAccept
	}
	if c.refInClosedWindow(seq, self) {
		return VerdictChallengeACK
	}
	return VerdictDrop
}

func (c *refConn) refInClosedWindow(seq int64, self *refEndpoint) bool {
	return seq >= self.rcvNxt && seq <= self.rcvNxt+self.rcvWnd
}

// refAcceptable is the int64 rendering of segmentAcceptable.
func refAcceptable(seq, end, rcvNxt, rcvWnd int64, length int) bool {
	if length == 0 {
		return seq >= rcvNxt && seq <= rcvNxt+rcvWnd
	}
	right := rcvNxt + rcvWnd
	return seq < right && end > rcvNxt
}

func (c *refConn) refAckOK(ack int64, sender *refEndpoint) bool {
	d := ack - sender.isn
	return d > 0 && d <= sender.sndNxt-sender.isn
}

func (c *refConn) refData(p Packet, self, peer *refEndpoint) Verdict {
	if p.Flags&FlagACK != 0 && !c.refAckOK(self.abs(p.Ack), self) {
		return VerdictChallengeACK
	}
	seq := self.abs(p.Seq)
	end := seq + int64(p.Payload)
	if !refAcceptable(seq, end, self.rcvNxt, self.rcvWnd, p.Payload) {
		return VerdictDrop
	}
	peer.rcvWnd = int64(p.Window) << peer.wscale
	if end > peer.sndNxt {
		peer.sndNxt = end
	}
	if p.Payload > 0 && seq <= self.rcvNxt && end > self.rcvNxt {
		self.rcvNxt = end
	}
	if p.Flags&FlagACK != 0 {
		c.refNoteAck(self.abs(p.Ack), self)
	}
	return VerdictAccept
}

func (c *refConn) refFin(p Packet, self, peer *refEndpoint) Verdict {
	if p.Flags&FlagACK != 0 && !c.refAckOK(self.abs(p.Ack), self) {
		return VerdictChallengeACK
	}
	length := p.Payload + 1
	seq := self.abs(p.Seq)
	end := seq + int64(length)
	if peer.finSeen {
		if !(seq >= self.rcvNxt-self.rcvWnd && seq <= self.rcvNxt+self.rcvWnd) {
			return VerdictDrop
		}
	} else if !refAcceptable(seq, end, self.rcvNxt, self.rcvWnd, length) {
		return VerdictDrop
	}
	peer.rcvWnd = int64(p.Window) << peer.wscale
	if end > peer.sndNxt {
		peer.sndNxt = end
	}
	if peer.finSeen {
		if p.Flags&FlagACK != 0 {
			c.refNoteAck(self.abs(p.Ack), self)
		}
		return VerdictAccept
	}
	dataEnd := seq + int64(p.Payload)
	if p.Payload > 0 && seq <= self.rcvNxt && dataEnd > self.rcvNxt {
		self.rcvNxt = dataEnd
	}
	if dataEnd != self.rcvNxt {
		if p.Flags&FlagACK != 0 {
			c.refNoteAck(self.abs(p.Ack), self)
		}
		return VerdictAccept
	}
	self.rcvNxt = end
	peer.finSeen = true
	if p.Flags&FlagACK != 0 {
		c.refNoteAck(self.abs(p.Ack), self)
	}
	c.refOnFin(peer)
	return VerdictAccept
}

func (c *refConn) refTimeWait(p Packet, self, peer *refEndpoint) Verdict {
	switch {
	case p.Flags&FlagRST != 0:
		return c.refRst(p, self)
	case p.Flags&FlagSYN != 0:
		if c.refInClosedWindow(self.abs(p.Seq), self) {
			return VerdictChallengeACK
		}
		return VerdictDrop
	case p.Flags&FlagFIN != 0:
		if self.abs(p.Seq) != self.rcvNxt-1 {
			return VerdictDrop
		}
		return VerdictAccept
	default:
		if p.Payload > 0 {
			return VerdictDrop
		}
		if p.Flags&FlagACK != 0 && c.refAckOK(self.abs(p.Ack), self) {
			return VerdictAccept
		}
		return VerdictDrop
	}
}

func (c *refConn) refNoteAck(ack int64, who *refEndpoint) {
	if who.synSeen && !who.synAcked && ack-who.isn >= 1 {
		who.synAcked = true
	}
	if who.finSeen && !who.finAcked && ack == who.sndNxt {
		who.finAcked = true
	}
	c.refAdvanceClose()
}

func (c *refConn) refOnFin(sender *refEndpoint) {
	ini := c.side[c.initiator]
	switch c.state {
	case refEstablished:
		if sender == ini {
			c.state = refFinWait1
		} else {
			c.state = refCloseWait
		}
	case refFinWait1:
		if sender != c.refFinSender() {
			c.state = refClosing
		}
	case refFinWait2:
		if sender != c.refFinSender() {
			c.state = refTimeWait
		}
	case refCloseWait:
		if sender == c.refFinSender() {
			c.state = refLastAck
		}
	}
	c.refAdvanceClose()
}

func (c *refConn) refFinSender() *refEndpoint {
	ini := c.side[c.initiator]
	resp := c.side[c.initiator^1]
	switch c.state {
	case refFinWait1, refFinWait2:
		return ini
	case refCloseWait, refLastAck:
		return resp
	default:
		return nil
	}
}

func (c *refConn) refAdvanceClose() {
	ini := c.side[c.initiator]
	resp := c.side[c.initiator^1]
	switch c.state {
	case refFinWait1:
		switch {
		case ini.finAcked && resp.finSeen:
			c.state = refTimeWait
		case ini.finAcked:
			c.state = refFinWait2
		case resp.finSeen:
			c.state = refClosing
		}
	case refClosing:
		if ini.finAcked && resp.finAcked {
			c.state = refTimeWait
		}
	case refLastAck:
		if ini.finAcked && resp.finAcked {
			c.state = refTimeWait
		}
	}
}

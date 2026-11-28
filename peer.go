package wireguard

import (
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"time"

	"net/netip"
)

// Config configures a single peer relationship.
type Config struct {
	LocalPrivateKey [32]byte
	RemotePublicKey [32]byte
	PresharedKey    [32]byte

	// Suite overrides the default X25519/BLAKE2s/ChaCha20-Poly1305 suite.
	Suite Suite
	// Rand supplies randomness. It defaults to crypto/rand.Reader.
	Rand io.Reader
	// Now supplies the clock. It defaults to time.Now.
	Now func() time.Time
	// Limits overrides the four session bounds. Nil means DefaultLimits.
	Limits *Limits
}

// Peer implements one endpoint of a WireGuard peer relationship. A Peer may
// both initiate and respond to handshakes.
type Peer struct {
	mu sync.Mutex

	suite  Suite
	rand   io.Reader
	now    func() time.Time
	limits Limits

	local  KeyPair
	remote [32]byte
	psk    [32]byte

	current        *session
	previous       *session
	next           *session
	pending        *pendingHandshake
	sentResponse   *sentResponse
	expiredCurrent bool

	timestamps     timestampChecker
	sentTimestamps timestampGenerator
	cookies        cookieStore
	cookieChecker  cookieChecker
	underLoad      bool
}

type sentResponse struct {
	localIndex uint32
	packet     []byte
	mac1       [16]byte
}

// NewPeer validates the configuration and creates a peer.
func NewPeer(cfg Config) (*Peer, error) {
	if err := validatePeerPublic(cfg.RemotePublicKey); err != nil {
		return nil, err
	}
	suite := cfg.Suite
	if suite == nil {
		suite = DefaultSuite{}
	}
	localPublic, err := suite.PublicKey(cfg.LocalPrivateKey)
	if err != nil {
		return nil, err
	}
	local := KeyPair{Private: cfg.LocalPrivateKey, Public: localPublic}
	if err := validatePeerPublic(local.Public); err != nil {
		return nil, err
	}
	r := cfg.Rand
	if r == nil {
		r = defaultRand
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	limits := DefaultLimits()
	if cfg.Limits != nil {
		limits = cfg.Limits.normalized()
	}

	p := &Peer{
		suite:  suite,
		rand:   r,
		now:    now,
		limits: limits,
		local:  local,
		remote: cfg.RemotePublicKey,
		psk:    cfg.PresharedKey,
	}
	if err := p.cookieChecker.refresh(r, now(), limits.CookieRefreshTime); err != nil {
		return nil, err
	}
	return p, nil
}

// SetUnderLoad marks this peer as overloaded. While set, handshake
// initiations without a valid MAC2 receive a Cookie Reply instead of
// performing the expensive Diffie-Hellman operations.
func (p *Peer) SetUnderLoad(v bool) {
	p.mu.Lock()
	p.underLoad = v
	p.mu.Unlock()
}

// UnderLoad reports the current load flag.
func (p *Peer) UnderLoad() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.underLoad
}

// LocalPublicKey returns this peer's static public key.
func (p *Peer) LocalPublicKey() [32]byte {
	return p.local.Public
}

// RemotePublicKey returns the configured remote static public key.
func (p *Peer) RemotePublicKey() [32]byte {
	return p.remote
}

// Initiate builds a fresh Handshake Initiation. If a cookie was previously
// obtained from the remote peer, MAC2 is filled in automatically.
func (p *Peer) Initiate() ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.initiateLocked()
}

func (p *Peer) initiateLocked() ([]byte, error) {
	now := p.now()
	p.expireSlotsLocked(now)

	ephemeral, err := p.suite.NewKeyPair(p.rand)
	if err != nil {
		return nil, err
	}
	index, err := p.newIndexLocked()
	if err != nil {
		return nil, err
	}
	timestamp := p.sentTimestamps.Next(now)
	pkt, pending, err := buildInitiation(p.suite, p.local, p.remote, p.psk, ephemeral, index, timestamp)
	if err != nil {
		return nil, err
	}
	p.applyCookieMAC2(pkt, initiationMAC2Offset)
	p.pending = pending
	return pkt, nil
}

// Action describes the protocol action produced by HandlePacket.
type Action uint8

const (
	// ActionNone indicates no protocol reply is needed.
	ActionNone Action = iota
	// ActionHandshakeResponse means Reply contains a Handshake Response.
	ActionHandshakeResponse
	// ActionCookieReply means Reply contains a Cookie Reply.
	ActionCookieReply
	// ActionCookieStored means a Cookie Reply was accepted.
	ActionCookieStored
	// ActionSessionEstablished means an initiator promoted a response to
	// Current.
	ActionSessionEstablished
	// ActionTransport means Payload contains decrypted transport data.
	ActionTransport
)

// Result is returned by HandlePacket.
type Result struct {
	Action       Action
	Reply        []byte
	Payload      []byte
	SessionIndex uint32
	// Confirmed is true when this transport packet atomically promoted a
	// responder's Next slot to Current.
	Confirmed bool
	// RekeyNeeded reports that the sender should start a new handshake while
	// continuing to use the old key.
	RekeyNeeded bool
}

// HandlePacket processes one inbound WireGuard datagram. For initiation and
// cookie actions, Reply must be sent back to source. For transport packets,
// Payload contains the authenticated plaintext.
func (p *Peer) HandlePacket(pkt []byte, source netip.AddrPort) (*Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	kind := ClassifyMessage(pkt)
	switch kind {
	case KindHandshakeInitiation:
		return p.handleInitiationLocked(pkt, source)
	case KindHandshakeResponse:
		return p.handleResponseLocked(pkt, source)
	case KindCookieReply:
		return p.handleCookieReplyLocked(pkt)
	case KindTransport:
		return p.handleTransportLocked(pkt)
	default:
		return nil, ErrInvalidMessage
	}
}

func (p *Peer) handleInitiationLocked(pkt []byte, source netip.AddrPort) (*Result, error) {
	msg, err := parseHandshakeInitiation(pkt)
	if err != nil {
		return nil, err
	}
	if !checkMAC1(p.suite, pkt, initiationMAC1Offset, p.local.Public) {
		return nil, ErrAuthentication
	}

	src := sourceKey(source)
	now := p.now()
	p.expireSlotsLocked(now)
	if p.underLoad {
		valid, err := p.cookieChecker.validMAC2(p.suite, src, pkt, initiationMAC2Offset, now, p.limits.CookieRefreshTime, p.rand)
		if err != nil {
			return nil, err
		}
		if !valid {
			if err := p.cookieChecker.refresh(p.rand, now, p.limits.CookieRefreshTime); err != nil {
				return nil, err
			}
			cookie := p.cookieChecker.cookieFor(p.suite, src)
			reply, err := buildCookieReply(p.suite, p.local.Public, msg.Sender, cookie, msg.MAC1, p.rand)
			if err != nil {
				return nil, err
			}
			return &Result{Action: ActionCookieReply, Reply: reply, SessionIndex: msg.Sender}, nil
		}
	}

	inbound, err := consumeInitiation(p.suite, p.local, p.remote, msg)
	if err != nil {
		return nil, err
	}
	if !p.timestamps.Accept(inbound.timestamp) {
		return nil, ErrReplay
	}

	ephemeral, err := p.suite.NewKeyPair(p.rand)
	if err != nil {
		return nil, err
	}
	index, err := p.newIndexLocked()
	if err != nil {
		return nil, err
	}
	response, sendKey, recvKey, err := buildResponse(p.suite, p.local, p.psk, inbound, ephemeral, index, msg.Sender)
	if err != nil {
		return nil, err
	}
	p.applyCookieMAC2(response, responseMAC2Offset)
	var responseMAC [16]byte
	copy(responseMAC[:], response[responseMAC1Offset:responseMAC2Offset])
	p.sentResponse = &sentResponse{
		localIndex: index,
		packet:     append([]byte(nil), response...),
		mac1:       responseMAC,
	}

	next := &session{
		localIndex:  index,
		remoteIndex: msg.Sender,
		sendKey:     sendKey,
		recvKey:     recvKey,
		createdAt:   now,
		initiator:   false,
	}
	if p.next != nil {
		p.next.destroy()
	}
	p.next = next

	return &Result{
		Action:       ActionHandshakeResponse,
		Reply:        response,
		SessionIndex: index,
	}, nil
}

func (p *Peer) handleResponseLocked(pkt []byte, source netip.AddrPort) (*Result, error) {
	msg, err := parseHandshakeResponse(pkt)
	if err != nil {
		return nil, err
	}
	if !checkMAC1(p.suite, pkt, responseMAC1Offset, p.local.Public) {
		return nil, ErrAuthentication
	}
	if p.pending == nil || msg.Receiver != p.pending.localIndex {
		return nil, ErrUnexpectedHandshake
	}
	now := p.now()
	if p.underLoad {
		src := sourceKey(source)
		valid, err := p.cookieChecker.validMAC2(p.suite, src, pkt, responseMAC2Offset, now, p.limits.CookieRefreshTime, p.rand)
		if err != nil {
			return nil, err
		}
		if !valid {
			if err := p.cookieChecker.refresh(p.rand, now, p.limits.CookieRefreshTime); err != nil {
				return nil, err
			}
			cookie := p.cookieChecker.cookieFor(p.suite, src)
			reply, err := buildCookieReply(p.suite, p.local.Public, msg.Sender, cookie, msg.MAC1, p.rand)
			if err != nil {
				return nil, err
			}
			return &Result{Action: ActionCookieReply, Reply: reply, SessionIndex: msg.Sender}, nil
		}
	}

	sendKey, recvKey, err := consumeResponse(p.suite, p.local, p.remote, p.psk, p.pending, msg)
	if err != nil {
		return nil, err
	}
	p.expireSlotsLocked(now)
	current := &session{
		localIndex:  p.pending.localIndex,
		remoteIndex: msg.Sender,
		sendKey:     sendKey,
		recvKey:     recvKey,
		createdAt:   now,
		initiator:   true,
	}
	if p.previous != nil {
		p.previous.destroy()
	}
	p.previous = p.current
	p.current = current
	p.expiredCurrent = false
	p.pending = nil
	return &Result{Action: ActionSessionEstablished, SessionIndex: current.localIndex}, nil
}

func (p *Peer) handleCookieReplyLocked(pkt []byte) (*Result, error) {
	msg, err := parseCookieReply(pkt)
	if err != nil {
		return nil, err
	}
	key := cookieKey(p.suite, p.remote)

	if p.pending != nil && msg.Receiver == p.pending.localIndex {
		plaintext, err := p.suite.OpenX(key, msg.Nonce, msg.Cookie[:], p.pending.mac1[:])
		if err != nil || len(plaintext) != 16 {
			return nil, ErrAuthentication
		}
		var cookie [16]byte
		copy(cookie[:], plaintext)
		p.cookies.set(cookie)
		return &Result{Action: ActionCookieStored}, nil
	}

	if p.sentResponse != nil && msg.Receiver == p.sentResponse.localIndex {
		plaintext, err := p.suite.OpenX(key, msg.Nonce, msg.Cookie[:], p.sentResponse.mac1[:])
		if err != nil || len(plaintext) != 16 {
			return nil, ErrAuthentication
		}
		var cookie [16]byte
		copy(cookie[:], plaintext)
		p.cookies.set(cookie)
		resend := append([]byte(nil), p.sentResponse.packet...)
		tag := p.suite.MAC(cookie[:], resend[:responseMAC2Offset])
		copy(resend[responseMAC2Offset:], tag[:])
		p.sentResponse.packet = resend
		return &Result{
			Action:       ActionHandshakeResponse,
			Reply:        append([]byte(nil), resend...),
			SessionIndex: p.sentResponse.localIndex,
		}, nil
	}
	return nil, ErrUnexpectedHandshake
}

func (p *Peer) handleTransportLocked(pkt []byte) (*Result, error) {
	msg, err := parseTransport(pkt)
	if err != nil {
		return nil, err
	}
	now := p.now()
	sess, slot := p.lookupSessionLocked(msg.Receiver)
	if sess == nil {
		return nil, ErrUnknownSession
	}
	if sess.rejectExpired(now, p.limits) {
		p.destroySlotLocked(slot)
		if slot == slotCurrent {
			p.expiredCurrent = true
		}
		return nil, ErrSessionExpired
	}
	if !sess.replay.Validate(msg.Counter) {
		return nil, ErrReplayWindow
	}

	var nonce [12]byte
	binary.LittleEndian.PutUint64(nonce[4:12], msg.Counter)
	plaintext, err := p.suite.Open(sess.recvKey, nonce, msg.Body, transportAD(pkt))
	if err != nil {
		return nil, ErrAuthentication
	}
	if msg.Counter >= p.limits.RejectAfterMessages {
		p.destroySlotLocked(slot)
		return nil, ErrSessionExpired
	}
	if !sess.replay.Update(msg.Counter) {
		return nil, ErrReplayWindow
	}

	confirmed := false
	if slot == slotNext {
		oldPrevious := p.previous
		p.previous = p.current
		p.current = p.next
		p.next = nil
		p.expiredCurrent = false
		if oldPrevious != nil {
			oldPrevious.destroy()
		}
		p.sentResponse = nil
		confirmed = true
	}
	p.expireInactiveLocked(now)

	return &Result{
		Action:       ActionTransport,
		Payload:      plaintext,
		SessionIndex: msg.Receiver,
		Confirmed:    confirmed,
		RekeyNeeded:  p.current != nil && p.current.rekeyNeeded(now, p.limits),
	}, nil
}

// SendResult is returned by Send.
type SendResult struct {
	Packet      []byte
	RekeyNeeded bool
}

// Send encrypts one transport packet. An empty payload produces a keepalive
// packet, which still counts as the responder confirmation if it is the first
// packet received on a Next session.
func (p *Peer) Send(payload []byte) (*SendResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.now()
	p.expireInactiveLocked(now)
	if p.current == nil {
		if p.next != nil {
			return nil, ErrHandshakeUnconfirmed
		}
		if p.expiredCurrent {
			p.expiredCurrent = false
			return nil, ErrSessionExpired
		}
		return nil, ErrNoSession
	}
	sess := p.current
	if sess.rejectExpired(now, p.limits) {
		p.destroySlotLocked(slotCurrent)
		p.expiredCurrent = true
		return nil, ErrSessionExpired
	}

	counter := sess.sendCounter
	rekey := sess.rekeyNeeded(now, p.limits) || counter+1 >= p.limits.RekeyAfterMessages
	var nonce [12]byte
	binary.LittleEndian.PutUint64(nonce[4:12], counter)

	header := marshalTransport(sess.remoteIndex, counter, nil)
	body, err := p.suite.Seal(sess.sendKey, nonce, payload, header)
	if err != nil {
		return nil, err
	}
	pkt := append(header, body...)
	sess.sendCounter++
	return &SendResult{Packet: pkt, RekeyNeeded: rekey}, nil
}

// Encrypt is a convenience wrapper around Send. Use Send when the caller needs
// the RekeyNeeded flag.
func (p *Peer) Encrypt(payload []byte) ([]byte, error) {
	result, err := p.Send(payload)
	if err != nil {
		return nil, err
	}
	return result.Packet, nil
}

// Decrypt authenticates and decrypts a transport packet.
func (p *Peer) Decrypt(pkt []byte) ([]byte, error) {
	if ClassifyMessage(pkt) != KindTransport {
		return nil, ErrInvalidMessage
	}
	result, err := p.HandlePacket(pkt, netip.AddrPort{})
	if err != nil {
		return nil, err
	}
	if result.Action != ActionTransport {
		return nil, ErrInvalidMessage
	}
	return result.Payload, nil
}

// NeedsRekey reports whether the current key has crossed a soft limit or was
// destroyed by a hard limit, in which case a new handshake is required.
func (p *Peer) NeedsRekey() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	if p.current != nil && p.current.rejectExpired(now, p.limits) {
		p.destroySlotLocked(slotCurrent)
		p.expiredCurrent = true
		return true
	}
	if p.current == nil {
		return false
	}
	return p.current.rekeyNeeded(now, p.limits)
}

// NeedsHandshake reports whether no current key exists or whether the current
// key has crossed a soft rekey limit.
func (p *Peer) NeedsHandshake() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	if p.current != nil && p.current.rejectExpired(now, p.limits) {
		p.destroySlotLocked(slotCurrent)
		p.expiredCurrent = true
	}
	return p.current == nil || p.current.rekeyNeeded(now, p.limits)
}

// Expire destroys every slot that has crossed RejectAfterTime or the reject
// message counter. Call it from a timer for eager key erasure; all packet
// paths also perform the same check lazily.
func (p *Peer) Expire() {
	p.mu.Lock()
	p.expireSlotsLocked(p.now())
	p.mu.Unlock()
}

// Sessions returns an immutable snapshot of the three key slots.
func (p *Peer) Sessions() SessionSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expireSlotsLocked(p.now())
	return SessionSnapshot{
		Current:  infoOf(p.current),
		Previous: infoOf(p.previous),
		Next:     infoOf(p.next),
	}
}

// Close destroys all session keys and pending handshake state.
func (p *Peer) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range []*session{p.current, p.previous, p.next} {
		if s != nil {
			s.destroy()
		}
	}
	p.current, p.previous, p.next, p.pending, p.sentResponse = nil, nil, nil, nil, nil
	p.expiredCurrent = false
}

type slot int

const (
	slotNone slot = iota
	slotCurrent
	slotPrevious
	slotNext
)

func (p *Peer) lookupSessionLocked(index uint32) (*session, slot) {
	if p.current != nil && p.current.localIndex == index {
		return p.current, slotCurrent
	}
	if p.previous != nil && p.previous.localIndex == index {
		return p.previous, slotPrevious
	}
	if p.next != nil && p.next.localIndex == index {
		return p.next, slotNext
	}
	return nil, slotNone
}

func (p *Peer) destroySlotLocked(s slot) {
	var target **session
	switch s {
	case slotCurrent:
		target = &p.current
	case slotPrevious:
		target = &p.previous
	case slotNext:
		target = &p.next
	default:
		return
	}
	if *target != nil {
		(*target).destroy()
		*target = nil
	}
}

func (p *Peer) expireSlotsLocked(now time.Time) {
	if p.current != nil && p.current.rejectExpired(now, p.limits) {
		p.destroySlotLocked(slotCurrent)
		p.expiredCurrent = true
	}
	if p.previous != nil && p.previous.rejectExpired(now, p.limits) {
		p.destroySlotLocked(slotPrevious)
	}
	if p.next != nil && p.next.rejectExpired(now, p.limits) {
		p.destroySlotLocked(slotNext)
		p.sentResponse = nil
	}
}

func (p *Peer) expireInactiveLocked(now time.Time) {
	if p.previous != nil && p.previous.rejectExpired(now, p.limits) {
		p.destroySlotLocked(slotPrevious)
	}
	if p.next != nil && p.next.rejectExpired(now, p.limits) {
		p.destroySlotLocked(slotNext)
		p.sentResponse = nil
	}
}

func (p *Peer) newIndexLocked() (uint32, error) {
	for attempts := 0; attempts < 64; attempts++ {
		var raw [4]byte
		if _, err := io.ReadFull(p.rand, raw[:]); err != nil {
			return 0, err
		}
		index := binary.LittleEndian.Uint32(raw[:])
		if index == 0 {
			continue
		}
		if p.current != nil && p.current.localIndex == index {
			continue
		}
		if p.previous != nil && p.previous.localIndex == index {
			continue
		}
		if p.next != nil && p.next.localIndex == index {
			continue
		}
		if p.pending != nil && p.pending.localIndex == index {
			continue
		}
		return index, nil
	}
	return 0, errors.New("wireguard: unable to allocate session index")
}

func (p *Peer) applyCookieMAC2(pkt []byte, offset int) {
	cookie, ok := p.cookies.get()
	if !ok {
		return
	}
	tag := p.suite.MAC(cookie[:], pkt[:offset])
	copy(pkt[offset:offset+16], tag[:])
}

func sourceKey(source netip.AddrPort) [18]byte {
	var out [18]byte
	ip := source.Addr().As16()
	copy(out[0:16], ip[:])
	binary.BigEndian.PutUint16(out[16:18], source.Port())
	return out
}

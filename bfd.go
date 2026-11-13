package netsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"math/big"
	"sync"
	"time"
)

// MACLen is the length of the truncated HMAC-SHA256 carried by control packets.
const MACLen = 16

// domainLabel is a fixed domain-separation label mixed into every MAC.
const domainLabel = "netsec/q0175:bfd-control:v1"

// State is the BFD session state.
type State uint8

const (
	StateDown State = iota
	StateInit
	StateUp
)

func (s State) String() string {
	switch s {
	case StateDown:
		return "Down"
	case StateInit:
		return "Init"
	case StateUp:
		return "Up"
	default:
		return "Unknown"
	}
}

// DownReason explains why the routing callback was fired.
type DownReason int

const (
	DownByControl DownReason = iota
	DownByTimeout
)

func (r DownReason) String() string {
	switch r {
	case DownByControl:
		return "control"
	case DownByTimeout:
		return "timeout"
	default:
		return "unknown"
	}
}

// Clock reports monotonic time in nanoseconds.
type Clock interface {
	Now() int64
}

// VirtualClock is a manually advanced Clock for deterministic tests.
type VirtualClock struct {
	mu  sync.Mutex
	now int64
}

func NewVirtualClock(start int64) *VirtualClock {
	return &VirtualClock{now: start}
}

func (c *VirtualClock) Now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *VirtualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now += int64(d)
}

// Identity is the immutable session identity bound at creation time. It is
// covered by the MAC of every control packet.
type Identity struct {
	LocalAddr  string
	RemoteAddr string
	IfaceID    uint32
	LocalDisc  uint32
	SessionGen uint64
}

// Policy bounds negotiated parameters of accepted control packets.
type Policy struct {
	MinInterval   uint64
	MaxInterval   uint64
	MaxDetectMult uint8
}

// ControlPacket is a wire-level BFD control packet as modeled by this
// package. MAC holds the HMAC-SHA256 truncated to MACLen bytes.
type ControlPacket struct {
	State      State
	Flags      uint8
	DetectMult uint8
	MyDisc     uint32
	YourDisc   uint32
	TxInterval uint64
	RxInterval uint64
	AuthKeyGen uint32
	AuthSeq    uint64
	MAC        [MACLen]byte
}

// Config configures a Session.
type Config struct {
	Identity
	// LocalMinRx is the local minimum receive interval in nanoseconds.
	LocalMinRx uint64
	Policy     Policy
	// Keys maps key generation to the shared secret.
	Keys           map[uint32][]byte
	CurrentKeyGen  uint32
	PreviousKeyGen uint32
	// HasPreviousKey enables short-term acceptance of the previous key
	// generation during rotation.
	HasPreviousKey bool
	Clock          Clock
	// OnDown is invoked at most once per transition into StateDown, only
	// for an authenticated Down control packet or a genuine timer expiry.
	OnDown func(reason DownReason)
}

var (
	ErrUnknownKeyGen     = errors.New("bfd: unknown or retired key generation")
	ErrBadMAC            = errors.New("bfd: control packet MAC mismatch")
	ErrSeqReplay         = errors.New("bfd: auth sequence number replayed or too old")
	ErrDiscBinding       = errors.New("bfd: discriminator binding violation")
	ErrParamBounds       = errors.New("bfd: negotiated parameter outside policy bounds")
	ErrOldKeyStateChange = errors.New("bfd: previous key generation may only maintain state")
	ErrInvalidConfig     = errors.New("bfd: invalid session configuration")
)

// seqWindow is a 64-packet anti-replay window over strictly increasing
// 64-bit sequence numbers.
type seqWindow struct {
	seen    bool
	highest uint64
	bitmap  uint64 // bit i set => (highest - i) was seen; bit 0 tracks highest itself
}

func (w *seqWindow) check(seq uint64) bool {
	if !w.seen {
		return true
	}
	if seq > w.highest {
		return true
	}
	diff := w.highest - seq
	if diff >= 64 {
		return false
	}
	return w.bitmap&(uint64(1)<<diff) == 0
}

func (w *seqWindow) commit(seq uint64) {
	if !w.seen {
		w.seen = true
		w.highest = seq
		w.bitmap = 1
		return
	}
	if seq > w.highest {
		shift := seq - w.highest
		if shift >= 64 {
			w.bitmap = 1
		} else {
			w.bitmap = (w.bitmap << shift) | 1
		}
		w.highest = seq
		return
	}
	w.bitmap |= uint64(1) << (w.highest - seq)
}

func writeStr(b *bytes.Buffer, s string) {
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], uint32(len(s)))
	b.Write(tmp[:])
	b.WriteString(s)
}

// macBlob serializes the domain label, session identity and every packet
// field (except the MAC itself) in a fixed order.
func macBlob(id *Identity, p *ControlPacket) []byte {
	var b bytes.Buffer
	b.WriteString(domainLabel)
	writeStr(&b, id.LocalAddr)
	writeStr(&b, id.RemoteAddr)
	var tmp [8]byte
	put := func(v uint64) {
		binary.BigEndian.PutUint64(tmp[:], v)
		b.Write(tmp[:])
	}
	put(uint64(id.IfaceID))
	put(uint64(id.LocalDisc))
	put(id.SessionGen)
	put(uint64(p.State))
	put(uint64(p.Flags))
	put(uint64(p.DetectMult))
	put(uint64(p.MyDisc))
	put(uint64(p.YourDisc))
	put(p.TxInterval)
	put(p.RxInterval)
	put(uint64(p.AuthKeyGen))
	put(p.AuthSeq)
	return b.Bytes()
}

func computeMAC(key []byte, id *Identity, p *ControlPacket) [MACLen]byte {
	h := hmac.New(sha256.New, key)
	h.Write(macBlob(id, p))
	sum := h.Sum(nil)
	var out [MACLen]byte
	copy(out[:], sum[:MACLen])
	return out
}

// SignControl computes and installs the truncated HMAC-SHA256 for a packet.
// It is used by the legitimate peer (and by tests) to build valid packets.
func SignControl(key []byte, id Identity, p *ControlPacket) {
	p.MAC = computeMAC(key, &id, p)
}

// Session is a single secured BFD session.
type Session struct {
	id            Identity
	localMinRx    uint64
	policy        Policy
	keys          map[uint32][]byte
	currentKeyGen uint32
	prevKeyGen    uint32
	hasPrevKey    bool
	clock         Clock
	onDown        func(reason DownReason)

	mu          sync.Mutex
	state       State
	established bool
	remoteDisc  uint32
	remoteTx    uint64
	remoteMinRx uint64
	detectMult  uint8
	lastAuth    int64
	hasAuth     bool
	windows     map[uint32]*seqWindow
	notified    bool
}

func NewSession(cfg Config) (*Session, error) {
	if cfg.Clock == nil {
		return nil, ErrInvalidConfig
	}
	if cfg.Policy.MaxDetectMult == 0 || cfg.Policy.MinInterval > cfg.Policy.MaxInterval {
		return nil, ErrInvalidConfig
	}
	if _, ok := cfg.Keys[cfg.CurrentKeyGen]; !ok {
		return nil, ErrInvalidConfig
	}
	if cfg.HasPreviousKey {
		if _, ok := cfg.Keys[cfg.PreviousKeyGen]; !ok {
			return nil, ErrInvalidConfig
		}
	}
	keys := make(map[uint32][]byte, len(cfg.Keys))
	for gen, k := range cfg.Keys {
		keys[gen] = k
	}
	return &Session{
		id:            cfg.Identity,
		localMinRx:    cfg.LocalMinRx,
		policy:        cfg.Policy,
		keys:          keys,
		currentKeyGen: cfg.CurrentKeyGen,
		prevKeyGen:    cfg.PreviousKeyGen,
		hasPrevKey:    cfg.HasPreviousKey,
		clock:         cfg.Clock,
		onDown:        cfg.OnDown,
		state:         StateDown,
		windows:       make(map[uint32]*seqWindow),
	}, nil
}

// State returns the current session state.
func (s *Session) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// HandlePacket authenticates and applies one received control packet. Any
// rejection leaves the session completely unchanged.
func (s *Session) HandlePacket(p *ControlPacket) error {
	s.mu.Lock()
	notify, err := s.handleLocked(p)
	s.mu.Unlock()
	if notify && s.onDown != nil {
		s.onDown(DownByControl)
	}
	return err
}

func (s *Session) handleLocked(p *ControlPacket) (notify bool, err error) {
	key, ok := s.keys[p.AuthKeyGen]
	acceptedGen := p.AuthKeyGen == s.currentKeyGen ||
		(s.hasPrevKey && p.AuthKeyGen == s.prevKeyGen)
	if !ok || !acceptedGen {
		return false, ErrUnknownKeyGen
	}
	isPrevGen := s.hasPrevKey && p.AuthKeyGen == s.prevKeyGen &&
		p.AuthKeyGen != s.currentKeyGen

	// Authentication first: MAC, then the anti-replay window check. The
	// window is committed only after every other check succeeds, so a
	// rejected packet consumes no sequence numbers.
	want := computeMAC(key, &s.id, p)
	if subtle.ConstantTimeCompare(want[:], p.MAC[:]) != 1 {
		return false, ErrBadMAC
	}
	win := s.windows[p.AuthKeyGen]
	if win == nil {
		win = &seqWindow{}
	}
	if !win.check(p.AuthSeq) {
		return false, ErrSeqReplay
	}

	if !s.established {
		if p.YourDisc != 0 || p.MyDisc == 0 {
			return false, ErrDiscBinding
		}
	} else {
		if p.YourDisc != s.id.LocalDisc || p.MyDisc != s.remoteDisc {
			return false, ErrDiscBinding
		}
	}

	if p.DetectMult == 0 || p.DetectMult > s.policy.MaxDetectMult {
		return false, ErrParamBounds
	}
	if !s.inBounds(p.TxInterval) || !s.inBounds(p.RxInterval) {
		return false, ErrParamBounds
	}

	// The previous key generation may keep the session alive but may not
	// move it (in particular it can never drive Up -> Down).
	if isPrevGen && p.State != s.state {
		return false, ErrOldKeyStateChange
	}

	// All checks passed: commit sequence window, timers and state.
	s.windows[p.AuthKeyGen] = win
	win.commit(p.AuthSeq)
	s.remoteTx = p.TxInterval
	s.remoteMinRx = p.RxInterval
	s.detectMult = p.DetectMult
	s.lastAuth = s.clock.Now()
	s.hasAuth = true
	if !s.established {
		s.established = true
		s.remoteDisc = p.MyDisc
	}
	return s.applyStateLocked(p.State), nil
}

func (s *Session) inBounds(v uint64) bool {
	return v >= s.policy.MinInterval && v <= s.policy.MaxInterval
}

// applyStateLocked applies the simplified BFD state machine and reports
// whether the routing callback must fire.
func (s *Session) applyStateLocked(remote State) bool {
	switch s.state {
	case StateDown:
		if remote == StateInit {
			s.state = StateInit
			s.notified = false
		}
	case StateInit:
		switch remote {
		case StateDown:
			return s.transitionDownLocked()
		case StateInit, StateUp:
			s.state = StateUp
			s.notified = false
		}
	case StateUp:
		if remote == StateDown {
			return s.transitionDownLocked()
		}
	}
	return false
}

func (s *Session) transitionDownLocked() bool {
	s.state = StateDown
	if s.notified {
		return false
	}
	s.notified = true
	return true
}

// deadline computes lastAuth + detectMult*max(localMinRx, remoteTx) using
// arbitrary-precision integers. ok is false when no packet has been
// authenticated yet or the product overflows int64 (treated as never
// expiring rather than wrapping around).
func (s *Session) deadline() (deadline int64, ok bool) {
	if !s.hasAuth {
		return 0, false
	}
	interval := s.localMinRx
	if s.remoteTx > interval {
		interval = s.remoteTx
	}
	prod := new(big.Int).Mul(
		new(big.Int).SetUint64(uint64(s.detectMult)),
		new(big.Int).SetUint64(interval),
	)
	sum := new(big.Int).Add(big.NewInt(s.lastAuth), prod)
	if !sum.IsInt64() {
		return 0, false
	}
	return sum.Int64(), true
}

// Deadline exposes the current detection deadline for diagnostics.
func (s *Session) Deadline() (deadline int64, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deadline()
}

// Tick advances timeout processing against the session clock. Only a
// genuinely expired timer drives the session Down and fires the callback.
func (s *Session) Tick() {
	s.mu.Lock()
	notify := false
	if s.state != StateDown {
		if dl, ok := s.deadline(); ok && s.clock.Now() >= dl {
			notify = s.transitionDownLocked()
		}
	}
	s.mu.Unlock()
	if notify && s.onDown != nil {
		s.onDown(DownByTimeout)
	}
}

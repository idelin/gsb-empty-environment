package netsec

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"
)

// DigestSize is the length of a sent-packet digest. Notifications must
// quote the full digest; truncated references are rejected.
const DigestSize = sha256.Size

// DefaultMinLinkMTU is the IPv6 minimum link MTU (RFC 8200 section 5).
// The encapsulation stack must leave room for it on the wire.
const DefaultMinLinkMTU = 1280

var (
	ErrStalePath           = errors.New("netsec: notification references a stale path instance")
	ErrTruncatedReference  = errors.New("netsec: notification quotes a truncated packet digest")
	ErrUnknownDigest       = errors.New("netsec: notification digest matches no recent sent packet")
	ErrInconsistentReport  = errors.New("netsec: reported MTU is not smaller than the matched packet")
	ErrProbeTooSmall       = errors.New("netsec: probe size must exceed the current MTU")
	ErrProbeTooLarge       = errors.New("netsec: probe size exceeds the maximum payload MTU")
	ErrUnknownProbe        = errors.New("netsec: unknown or mismatched probe token")
	ErrProbeExpired        = errors.New("netsec: probe acknowledgement arrived after its deadline")
	ErrProbeSuperseded     = errors.New("netsec: probe was superseded by a newer probe or MTU decrease")
	ErrPacketTooBig        = errors.New("netsec: packet exceeds the current payload MTU")
	ErrOverheadExceedsLink = errors.New("netsec: encapsulation overhead leaves no usable payload")
)

// EncapLayer describes one encapsulation header stack layer.
type EncapLayer struct {
	Name     string
	Overhead int // bytes added per packet by this layer
}

// Config configures a State. Zero values get sane defaults.
type Config struct {
	LinkMTU      int           // wire MTU of the underlying link (required)
	Layers       []EncapLayer  // encapsulation stack, outermost first
	MinLinkMTU   int           // minimum wire MTU the path must support (default 1280)
	SentCache    int           // sent-packet digest cache capacity (default 1024)
	SentTTL      time.Duration // how long a sent digest can be referenced (default 10s)
	ProbeTimeout time.Duration // probe acknowledgement deadline (default 3s)
	Clock        Clock         // defaults to the real clock
}

// Notification is an incoming "packet too big" report, e.g. carried by
// an ICMP PTB translated into a tunnel MTU state packet.
type Notification struct {
	PathInstance uint64 // path instance the report claims to belong to
	Digest       []byte // quoted digest of the offending packet; must be full length
	ReportedMTU  int    // wire MTU reported by the network
}

// ProbeToken authorizes one in-flight probe acknowledgement.
type ProbeToken struct {
	PathInstance uint64
	ID           uint64
	Secret       uint64
}

// DigestOf computes the reference digest for a packet about to be sent.
func DigestOf(packet []byte) [DigestSize]byte {
	return sha256.Sum256(packet)
}

type sentEntry struct {
	digest  [DigestSize]byte
	size    int // wire size of the sent packet
	expires time.Time
}

type probe struct {
	id          uint64
	size        int // payload MTU being probed
	secret      uint64
	expires     time.Time
	decreaseGen uint64 // decrease generation when the probe started
}

// State tracks the trusted MTU of one tunnel path. It is safe for
// concurrent use by a send host.
type State struct {
	mu     sync.Mutex
	clock  Clock
	cfg    Config
	random func([]byte) (int, error)

	overhead   int // total encapsulation overhead in bytes
	maxPayload int // LinkMTU - overhead
	floor      int // MinLinkMTU - overhead
	current    int // current trusted payload MTU

	pathInstance uint64

	sent     []sentEntry // ring buffer
	sentHead int

	probes      map[uint64]probe
	probeSeq    uint64
	decreaseGen uint64
}

// NewState builds a State. The trusted MTU starts at the maximum
// payload the encapsulation stack allows.
func NewState(cfg Config) (*State, error) {
	if cfg.LinkMTU <= 0 {
		return nil, fmt.Errorf("netsec: LinkMTU must be positive")
	}
	if cfg.MinLinkMTU == 0 {
		cfg.MinLinkMTU = DefaultMinLinkMTU
	}
	if cfg.SentCache == 0 {
		cfg.SentCache = 1024
	}
	if cfg.SentTTL == 0 {
		cfg.SentTTL = 10 * time.Second
	}
	if cfg.ProbeTimeout == 0 {
		cfg.ProbeTimeout = 3 * time.Second
	}
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	overhead := 0
	for _, l := range cfg.Layers {
		if l.Overhead < 0 {
			return nil, fmt.Errorf("netsec: layer %q has negative overhead", l.Name)
		}
		overhead += l.Overhead
	}
	maxPayload := cfg.LinkMTU - overhead
	floor := cfg.MinLinkMTU - overhead
	if floor <= 0 || maxPayload < floor {
		return nil, ErrOverheadExceedsLink
	}
	s := &State{
		clock:      cfg.Clock,
		cfg:        cfg,
		random:     rand.Read,
		overhead:   overhead,
		maxPayload: maxPayload,
		floor:      floor,
		current:    maxPayload,
		sent:       make([]sentEntry, cfg.SentCache),
		probes:     make(map[uint64]probe),
	}
	s.pathInstance = s.randUint64()
	return s, nil
}

func (s *State) randUint64() uint64 {
	var b [8]byte
	if _, err := s.random(b[:]); err != nil {
		panic(err)
	}
	return binary.LittleEndian.Uint64(b[:])
}

// Overhead returns the total encapsulation overhead in bytes.
func (s *State) Overhead() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.overhead
}

// MaxPayloadMTU is the payload MTU when the path is unconstrained.
func (s *State) MaxPayloadMTU() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxPayload
}

// MinPayloadMTU is the floor derived from the encapsulation overhead;
// the trusted MTU never drops below it.
func (s *State) MinPayloadMTU() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.floor
}

// CurrentMTU is the currently trusted payload MTU.
func (s *State) CurrentMTU() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

// PathInstance identifies the current path epoch; it changes on reset.
func (s *State) PathInstance() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pathInstance
}

// ResetPath starts a new path epoch: notifications and probes tied to
// the old instance become stale, the sent cache is cleared, and the
// trusted MTU returns to the unconstrained maximum.
func (s *State) ResetPath() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pathInstance = s.randUint64()
	s.sent = make([]sentEntry, s.cfg.SentCache)
	s.sentHead = 0
	s.probes = make(map[uint64]probe)
	s.current = s.maxPayload
}

// OnSend records a packet the host is about to send so that later
// notifications quoting its digest can be authenticated. It returns
// the wire size (payload plus encapsulation overhead).
func (s *State) OnSend(packet []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(packet) > s.current {
		return 0, ErrPacketTooBig
	}
	s.sent[s.sentHead] = sentEntry{
		digest:  DigestOf(packet),
		size:    len(packet) + s.overhead,
		expires: s.clock.Now().Add(s.cfg.SentTTL),
	}
	s.sentHead = (s.sentHead + 1) % len(s.sent)
	return len(packet) + s.overhead, nil
}

func (s *State) lookupSent(digest [DigestSize]byte, now time.Time) (sentEntry, bool) {
	for _, e := range s.sent {
		if e.size == 0 || now.After(e.expires) {
			continue
		}
		if e.digest == digest {
			return e, true
		}
	}
	return sentEntry{}, false
}

// HandleNotification validates an incoming MTU report and, if trusted,
// lowers the MTU. Reports never raise the MTU; only a successful probe
// does. Returns whether the report was applied.
func (s *State) HandleNotification(n Notification) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n.PathInstance != s.pathInstance {
		return false, ErrStalePath
	}
	if len(n.Digest) != DigestSize {
		return false, ErrTruncatedReference
	}
	var digest [DigestSize]byte
	copy(digest[:], n.Digest)
	entry, ok := s.lookupSent(digest, s.clock.Now())
	if !ok {
		return false, ErrUnknownDigest
	}
	if n.ReportedMTU >= entry.size {
		return false, ErrInconsistentReport
	}
	payload := n.ReportedMTU - s.overhead
	if payload >= s.current {
		return false, nil // not a decrease; ignore
	}
	if payload < s.floor {
		payload = s.floor
	}
	s.current = payload
	s.decreaseGen++
	return true, nil
}

// BeginProbe starts an active probe for a larger payload MTU. Only one
// probe generation is valid at a time; starting a new probe supersedes
// all earlier ones.
func (s *State) BeginProbe(size int) (ProbeToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if size <= s.current {
		return ProbeToken{}, ErrProbeTooSmall
	}
	if size > s.maxPayload {
		return ProbeToken{}, ErrProbeTooLarge
	}
	s.probeSeq++
	p := probe{
		id:          s.probeSeq,
		size:        size,
		secret:      s.randUint64(),
		expires:     s.clock.Now().Add(s.cfg.ProbeTimeout),
		decreaseGen: s.decreaseGen,
	}
	s.probes[p.id] = p
	return ProbeToken{PathInstance: s.pathInstance, ID: p.id, Secret: p.secret}, nil
}

// CompleteProbe applies a probe acknowledgement. The acknowledgement
// must match an outstanding, unexpired probe of the latest generation,
// and no MTU decrease may have happened since that probe began.
func (s *State) CompleteProbe(t ProbeToken) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.PathInstance != s.pathInstance {
		return false, ErrStalePath
	}
	p, ok := s.probes[t.ID]
	if !ok || p.secret != t.Secret {
		return false, ErrUnknownProbe
	}
	delete(s.probes, t.ID)
	if s.clock.Now().After(p.expires) {
		return false, ErrProbeExpired
	}
	if p.id != s.probeSeq || p.decreaseGen != s.decreaseGen {
		return false, ErrProbeSuperseded
	}
	if p.size > s.current {
		s.current = p.size
	}
	return true, nil
}

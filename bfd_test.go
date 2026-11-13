package netsec

import (
	"math"
	"reflect"
	"testing"
	"time"
)

var (
	keyCurrent  = []byte("current-generation-secret-key")
	keyPrevious = []byte("previous-generation-secret-key")
)

const (
	localDisc  = 0xAAAA0001
	remoteDisc = 0xBBBB0002
)

type harness struct {
	t     *testing.T
	clock *VirtualClock
	sess  *Session
	id    Identity
	downs []DownReason
	seq   uint64
}

func newHarness(t *testing.T, cfgMut func(*Config)) *harness {
	t.Helper()
	clock := NewVirtualClock(1_000_000)
	h := &harness{t: t, clock: clock}
	h.id = Identity{
		LocalAddr:  "2001:db8::1",
		RemoteAddr: "2001:db8::2",
		IfaceID:    7,
		LocalDisc:  localDisc,
		SessionGen: 1,
	}
	cfg := Config{
		Identity:       h.id,
		LocalMinRx:     300,
		Policy:         Policy{MinInterval: 100, MaxInterval: 10_000, MaxDetectMult: 10},
		Keys:           map[uint32][]byte{2: keyCurrent, 1: keyPrevious},
		CurrentKeyGen:  2,
		PreviousKeyGen: 1,
		HasPreviousKey: true,
		Clock:          clock,
		OnDown:         func(r DownReason) { h.downs = append(h.downs, r) },
	}
	if cfgMut != nil {
		cfgMut(&cfg)
	}
	sess, err := NewSession(cfg)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	h.sess = sess
	return h
}

// packet builds a properly signed packet from the legitimate peer.
func (h *harness) packet(mut func(p *ControlPacket)) *ControlPacket {
	h.t.Helper()
	h.seq++
	p := &ControlPacket{
		State:      StateUp,
		DetectMult: 3,
		MyDisc:     remoteDisc,
		YourDisc:   localDisc,
		TxInterval: 250,
		RxInterval: 200,
		AuthKeyGen: 2,
		AuthSeq:    h.seq,
	}
	if mut != nil {
		mut(p)
	}
	key := keyCurrent
	if p.AuthKeyGen == 1 {
		key = keyPrevious
	}
	SignControl(key, h.id, p)
	return p
}

func (h *harness) mustAccept(p *ControlPacket) {
	h.t.Helper()
	if err := h.sess.HandlePacket(p); err != nil {
		h.t.Fatalf("HandlePacket: unexpected rejection: %v", err)
	}
}

func (h *harness) mustReject(p *ControlPacket, want error) {
	h.t.Helper()
	err := h.sess.HandlePacket(p)
	if err != want {
		h.t.Fatalf("HandlePacket: got %v, want %v", err, want)
	}
}

// bringUp drives the session Down -> Init -> Up with legitimate packets.
func (h *harness) bringUp() {
	h.t.Helper()
	h.mustAccept(h.packet(func(p *ControlPacket) {
		p.State = StateInit
		p.YourDisc = 0
	}))
	h.mustAccept(h.packet(func(p *ControlPacket) { p.State = StateUp }))
	if got := h.sess.State(); got != StateUp {
		h.t.Fatalf("bringUp: state = %v, want Up", got)
	}
}

// snapshot captures every mutable session field to prove zero state change.
type snapshot struct {
	state       State
	established bool
	remoteDisc  uint32
	remoteTx    uint64
	remoteMinRx uint64
	detectMult  uint8
	lastAuth    int64
	hasAuth     bool
	notified    bool
	windows     map[uint32]seqWindow
	downCount   int
}

func (h *harness) snap() snapshot {
	s := h.sess
	s.mu.Lock()
	defer s.mu.Unlock()
	wins := make(map[uint32]seqWindow, len(s.windows))
	for gen, w := range s.windows {
		wins[gen] = *w
	}
	return snapshot{
		state:       s.state,
		established: s.established,
		remoteDisc:  s.remoteDisc,
		remoteTx:    s.remoteTx,
		remoteMinRx: s.remoteMinRx,
		detectMult:  s.detectMult,
		lastAuth:    s.lastAuth,
		hasAuth:     s.hasAuth,
		notified:    s.notified,
		windows:     wins,
		downCount:   len(h.downs),
	}
}

func (h *harness) requireNoChange(before snapshot) {
	h.t.Helper()
	after := h.snap()
	if !reflect.DeepEqual(before, after) {
		h.t.Fatalf("rejected packet mutated session:\nbefore: %+v\nafter:  %+v", before, after)
	}
}

func TestInitialBindingRules(t *testing.T) {
	h := newHarness(t, nil)
	before := h.snap()

	// First packet must carry yourDisc == 0.
	h.mustReject(h.packet(func(p *ControlPacket) {
		p.State = StateInit
		p.YourDisc = localDisc
	}), ErrDiscBinding)
	// First packet must carry a non-zero myDisc.
	h.mustReject(h.packet(func(p *ControlPacket) {
		p.State = StateInit
		p.YourDisc = 0
		p.MyDisc = 0
	}), ErrDiscBinding)
	h.requireNoChange(before)

	// Valid handshake packet binds the remote discriminator.
	h.mustAccept(h.packet(func(p *ControlPacket) {
		p.State = StateInit
		p.YourDisc = 0
	}))
	if got := h.sess.State(); got != StateInit {
		t.Fatalf("state = %v, want Init", got)
	}
}

func TestForgedMACDown(t *testing.T) {
	h := newHarness(t, nil)
	h.bringUp()
	before := h.snap()

	// Attacker forges a Down packet without the key: flip one MAC byte.
	forged := h.packet(func(p *ControlPacket) { p.State = StateDown })
	forged.MAC[0] ^= 0xFF
	h.mustReject(forged, ErrBadMAC)

	// Attacker replays a MAC computed for a different session identity.
	other := h.packet(func(p *ControlPacket) { p.State = StateDown })
	wrongID := h.id
	wrongID.SessionGen = 99
	SignControl(keyCurrent, wrongID, other)
	h.mustReject(other, ErrBadMAC)

	h.requireNoChange(before)
	if got := h.sess.State(); got != StateUp {
		t.Fatalf("state = %v, want Up", got)
	}
}

func TestOldKeyCannotDriveDown(t *testing.T) {
	h := newHarness(t, nil)
	h.bringUp()

	// Previous-generation key may maintain the current state.
	h.mustAccept(h.packet(func(p *ControlPacket) {
		p.State = StateUp
		p.AuthKeyGen = 1
	}))

	before := h.snap()
	// ... but it must never trigger Up -> Down.
	h.mustReject(h.packet(func(p *ControlPacket) {
		p.State = StateDown
		p.AuthKeyGen = 1
	}), ErrOldKeyStateChange)
	h.requireNoChange(before)

	// Unknown / retired key generations are rejected outright.
	h.mustReject(h.packet(func(p *ControlPacket) {
		p.State = StateDown
		p.AuthKeyGen = 9
	}), ErrUnknownKeyGen)
	h.requireNoChange(before)

	if got := h.sess.State(); got != StateUp {
		t.Fatalf("state = %v, want Up", got)
	}
	if len(h.downs) != 0 {
		t.Fatalf("unexpected down notifications: %v", h.downs)
	}
}

func TestSeqRollback(t *testing.T) {
	h := newHarness(t, nil)
	h.bringUp()                 // consumes seq 1, 2
	h.mustAccept(h.packet(nil)) // seq 3

	before := h.snap()
	// Replay of an already-seen sequence number.
	replay := h.packet(nil)
	replay.AuthSeq = 3
	SignControl(keyCurrent, h.id, replay)
	h.mustReject(replay, ErrSeqReplay)
	h.requireNoChange(before)

	// Sequence number fallen far behind the sliding window.
	for i := uint64(0); i < 70; i++ {
		h.mustAccept(h.packet(nil))
	}
	before2 := h.snap()
	old := h.packet(nil)
	old.AuthSeq = 3
	SignControl(keyCurrent, h.id, old)
	h.mustReject(old, ErrSeqReplay)
	h.requireNoChange(before2)

	// Independent window per key generation: gen 1 starts fresh.
	h.mustAccept(h.packet(func(p *ControlPacket) {
		p.State = StateUp
		p.AuthKeyGen = 1
		p.AuthSeq = 1
	}))
	// Replaying gen-1 seq 1 fails even though gen-2 window is far ahead.
	again := h.packet(func(p *ControlPacket) {
		p.State = StateUp
		p.AuthKeyGen = 1
	})
	again.AuthSeq = 1
	SignControl(keyPrevious, h.id, again)
	h.mustReject(again, ErrSeqReplay)
}

func TestDiscRebinding(t *testing.T) {
	h := newHarness(t, nil)
	h.bringUp()
	before := h.snap()

	// Attacker with a captured MAC key tries to rebind the remote disc.
	h.mustReject(h.packet(func(p *ControlPacket) {
		p.MyDisc = 0xDEADBEEF
	}), ErrDiscBinding)
	// ... or to steer the packet to a different local discriminator.
	h.mustReject(h.packet(func(p *ControlPacket) {
		p.YourDisc = 0xCAFE
	}), ErrDiscBinding)
	h.requireNoChange(before)
	if got := h.sess.State(); got != StateUp {
		t.Fatalf("state = %v, want Up", got)
	}
}

func TestParamBounds(t *testing.T) {
	h := newHarness(t, nil)
	h.bringUp()
	before := h.snap()

	h.mustReject(h.packet(func(p *ControlPacket) {
		p.DetectMult = 0
	}), ErrParamBounds)
	h.mustReject(h.packet(func(p *ControlPacket) {
		p.DetectMult = 11
	}), ErrParamBounds)
	h.mustReject(h.packet(func(p *ControlPacket) {
		p.TxInterval = 99
	}), ErrParamBounds)
	h.mustReject(h.packet(func(p *ControlPacket) {
		p.RxInterval = 10_001
	}), ErrParamBounds)
	h.requireNoChange(before)
}

func TestHugeIntervalOverflow(t *testing.T) {
	h := newHarness(t, func(c *Config) {
		c.Policy.MaxInterval = math.MaxUint64
	})
	h.bringUp()

	// detectMult * remoteTx overflows both uint64 and int64.
	h.mustAccept(h.packet(func(p *ControlPacket) {
		p.DetectMult = 10
		p.TxInterval = math.MaxUint64 - 1
	}))
	if _, ok := h.sess.Deadline(); ok {
		t.Fatal("overflowing deadline must be reported as unbounded")
	}

	// Advancing the clock enormously must not wrap the timeout around.
	h.clock.Advance(time.Duration(math.MaxInt64 / 2))
	h.sess.Tick()
	if got := h.sess.State(); got != StateUp {
		t.Fatalf("state = %v, want Up (timeout wrapped around)", got)
	}
	if len(h.downs) != 0 {
		t.Fatalf("spurious down notifications: %v", h.downs)
	}
}

func TestLegitimateDownAndIdempotentNotify(t *testing.T) {
	h := newHarness(t, nil)
	h.bringUp()

	h.mustAccept(h.packet(func(p *ControlPacket) { p.State = StateDown }))
	if got := h.sess.State(); got != StateDown {
		t.Fatalf("state = %v, want Down", got)
	}
	if len(h.downs) != 1 || h.downs[0] != DownByControl {
		t.Fatalf("downs = %v, want exactly one control notification", h.downs)
	}

	// Repeated authenticated Down packets stay idempotent.
	h.mustAccept(h.packet(func(p *ControlPacket) { p.State = StateDown }))
	if len(h.downs) != 1 {
		t.Fatalf("duplicate notification: %v", h.downs)
	}
}

func TestLegitimateTimeout(t *testing.T) {
	h := newHarness(t, nil)
	h.bringUp()
	// After bringUp: lastAuth = 1_000_000, detectMult = 3,
	// max(localMinRx=300, remoteTx=250) = 300 -> deadline = 1_000_900.
	dl, ok := h.sess.Deadline()
	if !ok || dl != 1_000_900 {
		t.Fatalf("deadline = %v, %v; want 1000900, true", dl, ok)
	}

	h.clock.Advance(899)
	h.sess.Tick()
	if got := h.sess.State(); got != StateUp {
		t.Fatalf("premature timeout: state = %v", got)
	}

	h.clock.Advance(1)
	h.sess.Tick()
	if got := h.sess.State(); got != StateDown {
		t.Fatalf("state = %v, want Down", got)
	}
	if len(h.downs) != 1 || h.downs[0] != DownByTimeout {
		t.Fatalf("downs = %v, want exactly one timeout notification", h.downs)
	}

	// Timer expiry is idempotent as well.
	h.clock.Advance(10_000)
	h.sess.Tick()
	if len(h.downs) != 1 {
		t.Fatalf("duplicate notification: %v", h.downs)
	}
}

func TestTimeoutUsesMaxOfLocalMinRxAndRemoteTx(t *testing.T) {
	h := newHarness(t, nil)
	h.bringUp()
	// remoteTx (250) < localMinRx (300): deadline used 300.
	// Now push remoteTx above localMinRx.
	h.mustAccept(h.packet(func(p *ControlPacket) {
		p.TxInterval = 500
		p.DetectMult = 2
	}))
	dl, ok := h.sess.Deadline()
	if !ok || dl != 1_000_000+2*500 {
		t.Fatalf("deadline = %v, %v; want 1001000, true", dl, ok)
	}
}

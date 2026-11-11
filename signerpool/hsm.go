package signerpool

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// HSM operations that can be gated by the pause barrier.
type opKind int

const (
	opSign opKind = iota
	opVerify
	opDestroy
)

func (k opKind) String() string {
	switch k {
	case opSign:
		return "sign"
	case opVerify:
		return "verify"
	case opDestroy:
		return "destroy"
	default:
		return "unknown"
	}
}

// Event records one interaction with the pseudo hardware, in arrival order.
type Event struct {
	Serial     int
	Op         string
	Generation int // key generation the handle belongs to
}

// Handle is a pseudo hardware key handle. Destroy is a one-shot transition:
// any repeat destroy or any use after destroy is recorded as a violation.
type Handle struct {
	Serial     int
	Generation int

	pub  ed25519.PublicKey
	priv ed25519.PrivateKey

	mu        sync.Mutex
	active    bool
	destroyed bool

	h *HSM
}

// HSM is a pausable pseudo hardware security module.
//
// When paused, Sign / Verify / Destroy block at the entry gate until Resume,
// which lets tests pin in-flight operations at deterministic moments.
type HSM struct {
	mu sync.Mutex

	paused  bool
	cond    *sync.Cond
	blocked int // operations currently waiting at the gate

	nextSerial atomic.Int64

	eventsMu sync.Mutex
	events   []Event

	failSignAtGen atomic.Int64 // generation whose Sign must fail (-1 disables)
	failSigns     atomic.Int64 // remaining transient Sign failures
	failVerifies  atomic.Int64 // remaining transient Verify failures

	doubleDestroys  atomic.Int64
	useAfterDestroy atomic.Int64
}

// NewHSM creates an empty pseudo HSM.
func NewHSM() *HSM {
	h := &HSM{}
	h.cond = sync.NewCond(&h.mu)
	h.failSignAtGen.Store(-1)
	return h
}

// Pause blocks subsequent gated operations at the entry gate. Operations that
// already passed the gate are unaffected.
func (h *HSM) Pause() {
	h.mu.Lock()
	h.paused = true
	h.mu.Unlock()
}

// Resume releases every operation waiting at the gate.
func (h *HSM) Resume() {
	h.mu.Lock()
	h.paused = false
	h.cond.Broadcast()
	h.mu.Unlock()
}

// Blocked reports how many operations are currently held at the pause gate.
func (h *HSM) Blocked() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.blocked
}

// WaitBlocked blocks until at least want operations are held at the gate.
func (h *HSM) WaitBlocked(want int) {
	h.mu.Lock()
	for h.blocked < want {
		h.cond.Wait()
	}
	h.mu.Unlock()
}

// FailSigns makes the next n Sign calls fail before the key is touched.
func (h *HSM) FailSigns(n int) {
	h.failSigns.Store(int64(n))
}

// FailSignAtGeneration forces every Sign on the given generation to fail.
// Pass -1 to clear the rule.
func (h *HSM) FailSignAtGeneration(gen int) {
	h.failSignAtGen.Store(int64(gen))
}

// FailVerifies makes the next n Verify calls fail.
func (h *HSM) FailVerifies(n int) {
	h.failVerifies.Store(int64(n))
}

// gate is the common entry point for pausable operations.
func (h *HSM) gate(k opKind, serial, gen int) {
	h.mu.Lock()
	for h.paused {
		h.blocked++
		h.cond.Broadcast()
		h.cond.Wait()
		h.blocked--
	}
	h.mu.Unlock()

	h.record(k, serial, gen)
}

func (h *HSM) record(k opKind, serial, gen int) {
	h.eventsMu.Lock()
	h.events = append(h.events, Event{
		Serial:     serial,
		Op:         k.String(),
		Generation: gen,
	})
	h.eventsMu.Unlock()
}

// GenerateKey mints a fresh ed25519 key pair and returns its handle. Key
// generation is never gated so rotation setup stays independent of Sign load.
func (h *HSM) GenerateKey(generation int) (*Handle, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	serial := int(h.nextSerial.Add(1))
	hdl := &Handle{
		Serial:     serial,
		Generation: generation,
		pub:        pub,
		priv:       priv,
		active:     true,
		h:          h,
	}
	h.eventsMu.Lock()
	h.events = append(h.events, Event{Serial: serial, Op: "generate", Generation: generation})
	h.eventsMu.Unlock()
	return hdl, nil
}

// Sign produces an ed25519 signature, honouring the pause barrier and any
// configured fault injection.
func (hdl *Handle) Sign(msg []byte) ([]byte, error) {
	hdl.h.gate(opSign, hdl.Serial, hdl.Generation)

	hdl.mu.Lock()
	if !hdl.active {
		hdl.mu.Unlock()
		hdl.h.useAfterDestroy.Add(1)
		return nil, errors.New("sign on destroyed handle")
	}
	hdl.mu.Unlock()

	if hdl.h.failSignAtGen.Load() == int64(hdl.Generation) {
		return nil, fmt.Errorf("injected sign failure on generation %d", hdl.Generation)
	}
	if n := hdl.h.failSigns.Add(-1); n >= 0 {
		return nil, errors.New("injected transient sign failure")
	}
	return ed25519.Sign(hdl.priv, msg), nil
}

// Verify checks a signature against this handle's public key.
func (hdl *Handle) Verify(msg, sig []byte) bool {
	hdl.h.gate(opVerify, hdl.Serial, hdl.Generation)

	hdl.mu.Lock()
	active := hdl.active
	hdl.mu.Unlock()
	if !active {
		hdl.h.useAfterDestroy.Add(1)
		return false
	}
	if n := hdl.h.failVerifies.Add(-1); n >= 0 {
		return false
	}
	return ed25519.Verify(hdl.pub, msg, sig)
}

// PublicKey returns a copy of the handle's public verifier.
func (hdl *Handle) PublicKey() ed25519.PublicKey {
	return append(ed25519.PublicKey(nil), hdl.pub...)
}

// Destroy tears the handle down. A second Destroy on the same handle is a
// recorded violation rather than a second hardware call.
func (hdl *Handle) Destroy() error {
	hdl.h.gate(opDestroy, hdl.Serial, hdl.Generation)

	hdl.mu.Lock()
	if !hdl.active {
		hdl.mu.Unlock()
		hdl.h.doubleDestroys.Add(1)
		return errors.New("handle already destroyed")
	}
	hdl.active = false
	hdl.destroyed = true
	hdl.mu.Unlock()
	return nil
}

// IsDestroyed reports the terminal state of the handle.
func (hdl *Handle) IsDestroyed() bool {
	hdl.mu.Lock()
	defer hdl.mu.Unlock()
	return hdl.destroyed
}

// Events returns a copy of the recorded event log.
func (h *HSM) Events() []Event {
	h.eventsMu.Lock()
	defer h.eventsMu.Unlock()
	out := make([]Event, len(h.events))
	copy(out, h.events)
	return out
}

// DoubleDestroys counts repeated Destroy calls on the same handle.
func (h *HSM) DoubleDestroys() int { return int(h.doubleDestroys.Load()) }

// UseAfterDestroy counts Sign/Verify calls reaching a destroyed handle.
func (h *HSM) UseAfterDestroy() int { return int(h.useAfterDestroy.Load()) }

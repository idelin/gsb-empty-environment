package netsec

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
)

// Level is an identity assurance level. Larger values are strictly stronger;
// a message protected at level L can be released once the session's granted
// level is at least L.
type Level int

// LevelNone is the level of a freshly established session: no post-handshake
// client identity has been proven yet.
const LevelNone Level = 0

// Sentinel errors reported by the gate.
var (
	// ErrClosed is returned after HalfClose: new submissions, new upgrade
	// requests and late credential deliveries are all refused.
	ErrClosed = errors.New("netsec: session gate is half-closed")
	// ErrUnknownContext is returned when a credential or cancellation
	// references an upgrade context that does not exist, was never requested,
	// or has already reached a terminal state.
	ErrUnknownContext = errors.New("netsec: unknown or already settled upgrade context")
	// ErrContextMismatch is returned when a credential presented for upgrade
	// context id is not itself bound to that same unique context.
	ErrContextMismatch = errors.New("netsec: credential not bound to upgrade context")
	// ErrLevelAlreadyGranted is returned when the requested level is no
	// stronger than the level the session currently holds.
	ErrLevelAlreadyGranted = errors.New("netsec: requested identity level already granted")
)

// Credential is a client certificate (or equivalent proof) delivered after the
// session was established. ContextID is the unique upgrade context the proof
// was issued for: the gate refuses any credential whose ContextID does not
// match the BeginUpgrade context it is being submitted against.
type Credential struct {
	ContextID string
	Level     Level
	Proof     []byte
}

// Verifier performs the protocol-specific, potentially slow cryptographic
// validation of a credential. The gate only calls Verify once the credential
// has already been matched to a live upgrade context, so an implementation
// never has to distinguish contexts itself.
type Verifier interface {
	Verify(ctx context.Context, cred Credential) error
}

// VerifierFunc adapts an ordinary function to the Verifier interface.
type VerifierFunc func(ctx context.Context, cred Credential) error

// Verify calls the wrapped function.
func (f VerifierFunc) Verify(ctx context.Context, cred Credential) error {
	return f(ctx, cred)
}

// Message is an inbound application message handed to the gate. RequiredLevel
// is the minimum client identity level that must have been granted before the
// payload may reach the protected sink.
type Message struct {
	Sequence      uint64
	RequiredLevel Level
	Payload       []byte
}

// Sink receives messages once the gate has decided their fate. Deliver is the
// protected application callback and must be safe for concurrent use. Reject
// is called for messages that can never be released (failed or canceled
// upgrade, or a half-closed session).
type Sink interface {
	Deliver(msg Message)
	Reject(msg Message, err error)
}

// SinkFunc builds a Sink from two callback functions.
func SinkFunc(deliver func(Message), reject func(Message, error)) Sink {
	return sinkFunc{deliver: deliver, reject: reject}
}

type sinkFunc struct {
	deliver func(Message)
	reject  func(Message, error)
}

func (s sinkFunc) Deliver(msg Message)           { s.deliver(msg) }
func (s sinkFunc) Reject(msg Message, err error) { s.reject(msg, err) }

// Gate enforces identity-level isolation on a long-lived connection.
//
// Application messages requiring an identity level already granted pass
// straight through, even while a stronger upgrade is in flight. Messages
// requiring a stronger level are queued outside the protected sink until an
// upgrade bound to its own unique context presents a matching credential and
// the verifier succeeds; the held messages are then released together, in
// arrival order. A failed or canceled upgrade never releases a held message.
type Gate struct {
	verifier Verifier
	sink     Sink

	mu       sync.Mutex
	level    Level
	closed   bool
	held     []Message
	upgrades map[string]*pendingUpgrade
}

type pendingUpgrade struct {
	target  Level
	cancel  context.CancelFunc
	settled bool
}

// NewGate creates a gate starting at LevelNone. Every accepted message is
// eventually delivered to or rejected by sink exactly once.
func NewGate(verifier Verifier, sink Sink) *Gate {
	return &Gate{
		verifier: verifier,
		sink:     sink,
		upgrades: make(map[string]*pendingUpgrade),
	}
}

// BeginUpgrade opens a new identity upgrade request bound to a freshly
// generated unique context. The returned context is canceled when the upgrade
// succeeds, fails, is canceled explicitly, or the gate is half-closed.
//
// Multiple upgrades may run concurrently; each keeps its own context so
// out-of-order credential callbacks cannot be confused with one another.
func (g *Gate) BeginUpgrade(ctx context.Context, target Level) (string, context.Context, error) {
	if target <= LevelNone {
		return "", nil, ErrLevelAlreadyGranted
	}

	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return "", nil, ErrClosed
	}
	if target <= g.level {
		g.mu.Unlock()
		return "", nil, ErrLevelAlreadyGranted
	}

	id, err := newContextID()
	if err != nil {
		g.mu.Unlock()
		return "", nil, err
	}
	upgradeCtx, cancel := context.WithCancel(ctx)
	g.upgrades[id] = &pendingUpgrade{target: target, cancel: cancel}
	g.mu.Unlock()

	go g.watchCancel(id, upgradeCtx, cancel)
	return id, upgradeCtx, nil
}

// watchCancel resolves the upgrade the moment its context is canceled, whether
// by the caller, the parent context, or gate closure.
func (g *Gate) watchCancel(id string, ctx context.Context, cancel context.CancelFunc) {
	<-ctx.Done()
	cancel()
	g.settleOnCancel(id, ctx.Err())
}

// CancelUpgrade abandons an in-flight upgrade. Held messages that no remaining
// live upgrade could satisfy are rejected with the given cause; canceling one
// context never touches another upgrade's messages.
func (g *Gate) CancelUpgrade(id string, cause error) error {
	g.mu.Lock()
	up, ok := g.upgrades[id]
	if !ok || up.settled {
		g.mu.Unlock()
		return ErrUnknownContext
	}
	up.settled = true
	delete(g.upgrades, id)
	cancel := up.cancel
	rejected := g.rejectUnreachableLocked()
	g.mu.Unlock()

	cancel()
	g.notifyRejected(rejected, cause)
	return nil
}

// Submit offers an inbound application message to the gate. Messages whose
// required level is already granted are delivered immediately; stronger ones
// are queued until a matching upgrade completes. It returns ErrClosed without
// touching the sink once the gate has been half-closed.
func (g *Gate) Submit(msg Message) error {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return ErrClosed
	}
	if msg.RequiredLevel <= g.level {
		g.mu.Unlock()
		g.sink.Deliver(msg)
		return nil
	}
	g.held = append(g.held, msg)
	g.mu.Unlock()
	return nil
}

// CompleteUpgrade is called by the protocol host when the client's credential
// for upgrade context id arrives. The credential must be bound to that exact
// context (cred.ContextID == id) and must pass the verifier; both conditions
// are required before any held message is released.
//
// A second call for the same context id fails with ErrUnknownContext, so
// duplicate or replayed authentication callbacks cannot re-enter the upgrade.
// A credential bound to a different context fails with ErrContextMismatch and
// leaves both upgrades untouched.
func (g *Gate) CompleteUpgrade(ctx context.Context, id string, cred Credential) error {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return ErrClosed
	}
	up, ok := g.upgrades[id]
	if !ok {
		g.mu.Unlock()
		return ErrUnknownContext
	}
	if cred.ContextID != id {
		g.mu.Unlock()
		return ErrContextMismatch
	}
	if up.settled {
		g.mu.Unlock()
		return ErrUnknownContext
	}
	up.settled = true
	target := up.target
	cancel := up.cancel
	delete(g.upgrades, id)
	g.mu.Unlock()

	verifyErr := g.verifier.Verify(ctx, cred)

	g.mu.Lock()
	if verifyErr != nil {
		rejected := g.rejectUnreachableLocked()
		g.mu.Unlock()
		cancel()
		g.notifyRejected(rejected, verifyErr)
		return verifyErr
	}
	if target > g.level {
		g.level = target
	}
	release := g.flushLocked()
	g.mu.Unlock()

	cancel()
	for _, msg := range release {
		g.sink.Deliver(msg)
	}
	return nil
}

// settleOnCancel resolves an upgrade whose context has been canceled without
// any valid credential being presented. It is a no-op for contexts already
// settled through CompleteUpgrade, CancelUpgrade or HalfClose.
func (g *Gate) settleOnCancel(id string, cause error) {
	g.mu.Lock()
	up, ok := g.upgrades[id]
	if !ok || up.settled {
		g.mu.Unlock()
		return
	}
	up.settled = true
	delete(g.upgrades, id)
	rejected := g.rejectUnreachableLocked()
	g.mu.Unlock()

	g.notifyRejected(rejected, cause)
}

// HalfClose marks the session read-half closed. Every pending upgrade is
// failed and every queued message is rejected with ErrClosed; afterwards all
// gate methods return ErrClosed. Messages already delivered at the old level
// are unaffected.
func (g *Gate) HalfClose() error {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil
	}
	g.closed = true
	cancels := make([]context.CancelFunc, 0, len(g.upgrades))
	for _, up := range g.upgrades {
		up.settled = true
		cancels = append(cancels, up.cancel)
	}
	g.upgrades = make(map[string]*pendingUpgrade)
	rejected := g.held
	g.held = nil
	g.mu.Unlock()

	for _, msg := range rejected {
		g.sink.Reject(msg, ErrClosed)
	}
	for _, cancel := range cancels {
		cancel()
	}
	return nil
}

// CurrentLevel reports the strongest identity level granted so far.
func (g *Gate) CurrentLevel() Level {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.level
}

// flushLocked returns and removes every held message whose required level is
// now satisfied. Remaining messages stay queued behind a later upgrade.
func (g *Gate) flushLocked() []Message {
	if len(g.held) == 0 {
		return nil
	}
	release := make([]Message, 0, len(g.held))
	remaining := make([]Message, 0, len(g.held))
	for _, msg := range g.held {
		if msg.RequiredLevel <= g.level {
			release = append(release, msg)
		} else {
			remaining = append(remaining, msg)
		}
	}
	g.held = remaining
	return release
}

// rejectUnreachableLocked returns and removes held messages whose required
// level no remaining live upgrade could ever grant.
func (g *Gate) rejectUnreachableLocked() []Message {
	floor := g.level
	for _, up := range g.upgrades {
		if up.target > floor {
			floor = up.target
		}
	}
	if len(g.held) == 0 {
		return nil
	}
	rejected := make([]Message, 0)
	remaining := make([]Message, 0, len(g.held))
	for _, msg := range g.held {
		if msg.RequiredLevel > floor {
			rejected = append(rejected, msg)
		} else {
			remaining = append(remaining, msg)
		}
	}
	g.held = remaining
	return rejected
}

func (g *Gate) notifyRejected(msgs []Message, cause error) {
	for _, msg := range msgs {
		g.sink.Reject(msg, cause)
	}
}

func newContextID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

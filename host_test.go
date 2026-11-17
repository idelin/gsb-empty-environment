package netsec

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

var errTransportClosed = errors.New("test transport closed")

const (
	capRead  Capability = "msg:read"
	capWrite Capability = "msg:write"
	capAdmin Capability = "msg:admin"
)

var (
	privilegedPeer = PeerCred{PID: 101, UID: 1000, GID: 1000, Context: "svc:app"}
	otherPeer      = PeerCred{PID: 202, UID: 2000, GID: 2000, Context: "svc:other"}
	unknownPeer    = PeerCred{PID: 303, UID: 3000, GID: 3000, Context: "svc:unknown"}
)

func baselineCreds() map[PeerCred]Credential {
	return map[PeerCred]Credential{
		privilegedPeer: {Principal: "app", Caps: NewCapSet(capRead, capWrite, capAdmin)},
		otherPeer:      {Principal: "other", Caps: NewCapSet(capRead)},
	}
}

func downgradedCreds() map[PeerCred]Credential {
	return map[PeerCred]Credential{
		privilegedPeer: {Principal: "app", Caps: NewCapSet(capRead)},
		otherPeer:      {Principal: "other", Caps: NewCapSet(capRead)},
	}
}

// allowAllPolicy makes the host-side capability checks the sole gate, so
// tests can reason purely about credential generations.
func allowAllPolicy() Policy {
	return PolicyFunc(func(req Request) Decision {
		for _, check := range req.Requires {
			if !check.Granted {
				return Decision{Allow: false, Reason: "missing capability"}
			}
		}
		return Decision{Allow: true}
	})
}

func newTestHost(t *testing.T) (*Host, *CredentialEpoch) {
	t.Helper()
	epochs := NewCredentialEpoch(baselineCreds())
	return NewHost(epochs, allowAllPolicy()), epochs
}

func handleFrame(conn string, peer PeerCred, id string) IncomingFrame {
	return IncomingFrame{
		Connection: conn,
		Kind:       FrameHandle,
		Peer:       peer,
		Handle:     Handle{ID: id, Value: id + "-fd"},
	}
}

func bodyFrame(conn string, peer PeerCred, id string, caps ...Capability) IncomingFrame {
	return IncomingFrame{
		Connection: conn,
		Kind:       FrameBody,
		Peer:       peer,
		Handle:     Handle{ID: id},
		Body:       []byte("payload-" + id),
		Requires:   caps,
	}
}

// TestCredentialEpoch_DowngradeMonotonic pins the generation semantics:
// downgrades may only remove privileges and always advance the epoch.
func TestCredentialEpoch_DowngradeMonotonic(t *testing.T) {
	epochs := NewCredentialEpoch(baselineCreds())
	if got := epochs.CurrentEpoch(); got != 1 {
		t.Fatalf("initial epoch = %d, want 1", got)
	}

	epoch, err := epochs.Downgrade(downgradedCreds())
	if err != nil {
		t.Fatalf("valid downgrade failed: %v", err)
	}
	if epoch != 2 {
		t.Fatalf("epoch after downgrade = %d, want 2", epoch)
	}

	cred, stampedEpoch, ok := epochs.Lookup(privilegedPeer)
	if !ok {
		t.Fatal("credential disappeared after downgrade")
	}
	if stampedEpoch != 2 {
		t.Fatalf("lookup epoch = %d, want 2", stampedEpoch)
	}
	if cred.Caps.Contains(capWrite) || cred.Caps.Contains(capAdmin) {
		t.Fatalf("downgrade did not remove privileges: %v", cred.Caps)
	}

	// Re-introducing a removed capability must be rejected.
	if _, err := epochs.Downgrade(baselineCreds()); !errors.Is(err, ErrInvalidDowngrade) {
		t.Fatalf("privilege escalation err = %v, want ErrInvalidDowngrade", err)
	}
	// Adding a brand new identity must be rejected.
	extra := downgradedCreds()
	extra[unknownPeer] = Credential{Principal: "intruder", Caps: NewCapSet(capRead)}
	if _, err := epochs.Downgrade(extra); !errors.Is(err, ErrInvalidDowngrade) {
		t.Fatalf("new identity err = %v, want ErrInvalidDowngrade", err)
	}
	if epochs.CurrentEpoch() != 2 {
		t.Fatalf("rejected downgrade moved epoch to %d", epochs.CurrentEpoch())
	}

	// Historical generation 1 must still show the old, wider privileges for
	// audit, while an identity absent from the live table fails lookup.
	old, ok := epochs.AtEpoch(1)
	if !ok {
		t.Fatal("epoch 1 history missing")
	}
	if !old[privilegedPeer].Caps.Contains(capWrite) {
		t.Fatal("history must preserve generation-1 privileges")
	}
	if _, _, ok := epochs.Lookup(unknownPeer); ok {
		t.Fatal("unknown peer must not resolve to a credential")
	}
}

// TestHost_SameGenerationHandleAndBody proves the same-message invariant:
// handle and body accepted inside one generation succeed and are stamped
// with that generation.
func TestHost_SameGenerationHandleAndBody(t *testing.T) {
	host, epochs := newTestHost(t)
	host.RegisterConnection("c1", privilegedPeer)

	if err := host.HandleFrame(handleFrame("c1", privilegedPeer, "h1")); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if err := host.HandleFrame(bodyFrame("c1", privilegedPeer, "h1", capWrite)); err != nil {
		t.Fatalf("body within same epoch: %v", err)
	}

	for _, entry := range host.Audit() {
		if entry.Epoch != 1 {
			t.Fatalf("audit epoch = %d, want 1", entry.Epoch)
		}
		if !entry.Allowed || entry.Principal != "app" {
			t.Fatalf("unexpected audit entry: %+v", entry)
		}
	}
	if epochs.CurrentEpoch() != 1 {
		t.Fatalf("epoch = %d, want 1", epochs.CurrentEpoch())
	}
}

// TestHost_DowngradeInterleavedWithReceive is the core window test: the
// handle is received before the downgrade, the body after it. The reused
// connection must not extend the old write privilege across generations.
func TestHost_DowngradeInterleavedWithReceive(t *testing.T) {
	host, epochs := newTestHost(t)
	host.RegisterConnection("c1", privilegedPeer)

	if err := host.HandleFrame(handleFrame("c1", privilegedPeer, "h1")); err != nil {
		t.Fatalf("pre-downgrade handle: %v", err)
	}
	if _, err := epochs.Downgrade(downgradedCreds()); err != nil {
		t.Fatalf("downgrade: %v", err)
	}

	// Body on the very same long-lived connection, but a new generation.
	err := host.HandleFrame(bodyFrame("c1", privilegedPeer, "h1", capWrite))
	if !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("cross-generation body err = %v, want ErrGenerationMismatch", err)
	}

	// The stale handle was consumed by the refusal: retrying must not
	// resurrect it.
	if err := host.HandleFrame(bodyFrame("c1", privilegedPeer, "h1", capRead)); !errors.Is(err, ErrOrphanBody) {
		t.Fatalf("retry err = %v, want ErrOrphanBody", err)
	}

	// A fresh handle/body pair under the new generation works only with the
	// reduced (read-only) privilege set.
	if err := host.HandleFrame(handleFrame("c1", privilegedPeer, "h2")); err != nil {
		t.Fatalf("post-downgrade handle: %v", err)
	}
	if err := host.HandleFrame(bodyFrame("c1", privilegedPeer, "h2", capRead)); err != nil {
		t.Fatalf("read-only body: %v", err)
	}
	if err := host.HandleFrame(bodyFrame("c1", privilegedPeer, "missing", capWrite)); !errors.Is(err, ErrOrphanBody) {
		t.Fatalf("write without handle err = %v, want ErrOrphanBody", err)
	}

	audit := host.Audit()
	var denied *AuditEntry
	for i := range audit {
		if audit[i].Kind == FrameBody && !audit[i].Allowed {
			denied = &audit[i]
			break
		}
	}
	if denied == nil || denied.Epoch != 2 || !errors.Is(denied.Err, ErrGenerationMismatch) {
		t.Fatalf("missing generation-2 denial in audit: %+v", audit)
	}
}

// TestHost_MissingCredential ensures frames from peers absent from the
// current trusted snapshot are refused and never create usable handles.
func TestHost_MissingCredential(t *testing.T) {
	host, _ := newTestHost(t)
	host.RegisterConnection("c1", privilegedPeer)

	if err := host.HandleFrame(handleFrame("c1", unknownPeer, "hx")); !errors.Is(err, ErrMissingCredential) {
		t.Fatalf("unknown handle err = %v, want ErrMissingCredential", err)
	}
	// The refused handle must not have been parked.
	if err := host.HandleFrame(bodyFrame("c1", unknownPeer, "hx", capRead)); !errors.Is(err, ErrMissingCredential) {
		t.Fatalf("unknown body err = %v, want ErrMissingCredential", err)
	}

	// A valid handle followed by a body whose peer vanished from the
	// snapshot invalidates the parked handle.
	if err := host.HandleFrame(handleFrame("c1", privilegedPeer, "h3")); err != nil {
		t.Fatalf("handle: %v", err)
	}
	host.mu.Lock()
	pending := host.pending["c1"]
	_, parked := pending["h3"]
	host.mu.Unlock()
	if !parked {
		t.Fatal("handle should be parked while awaiting its body")
	}

	if err := host.HandleFrame(bodyFrame("c1", unknownPeer, "h3", capRead)); !errors.Is(err, ErrMissingCredential) {
		t.Fatalf("body with vanished credential err = %v, want ErrMissingCredential", err)
	}
	host.mu.Lock()
	_, stillParked := pending["h3"]
	host.mu.Unlock()
	if stillParked {
		t.Fatal("pending handle must be dropped when its body lacks a credential")
	}

	for _, entry := range host.Audit() {
		if !entry.Allowed && !errors.Is(entry.Err, ErrMissingCredential) {
			t.Fatalf("unexpected denial reason: %+v", entry)
		}
	}
}

// TestHost_BatchAcrossDowngrade sends frames whose messages straddle a
// downgrade. Every frame is authorized on its own receive-time snapshot;
// post-downgrade frames can never use the old privilege.
func TestHost_BatchAcrossDowngrade(t *testing.T) {
	host, epochs := newTestHost(t)
	host.RegisterConnection("c1", privilegedPeer)

	// Fully generation-1 message inside the first batch.
	first := []IncomingFrame{
		handleFrame("c1", privilegedPeer, "b1"),
		bodyFrame("c1", privilegedPeer, "b1", capWrite),
	}
	for i, err := range host.HandleBatch(first) {
		if err != nil {
			t.Fatalf("frame %d before downgrade: %v", i, err)
		}
	}

	// Handle parked in generation 1, then the process drops privileges.
	if err := host.HandleFrame(handleFrame("c1", privilegedPeer, "b2")); err != nil {
		t.Fatalf("pre-downgrade handle b2: %v", err)
	}
	if _, err := epochs.Downgrade(downgradedCreds()); err != nil {
		t.Fatalf("downgrade: %v", err)
	}

	// The post-downgrade batch mixes a stale-generation completion, a fresh
	// read-only message and an unauthorized write attempt.
	rest := []IncomingFrame{
		bodyFrame("c1", privilegedPeer, "b2", capWrite),
		handleFrame("c1", privilegedPeer, "b3"),
		bodyFrame("c1", privilegedPeer, "b3", capRead),
		handleFrame("c1", privilegedPeer, "b4"),
		bodyFrame("c1", privilegedPeer, "b4", capWrite),
	}
	errs := host.HandleBatch(rest)
	wantErrs := []error{ErrGenerationMismatch, nil, nil, nil, ErrUnauthorized}
	for i, want := range wantErrs {
		if want == nil {
			if errs[i] != nil {
				t.Fatalf("batch frame %d: unexpected err %v", i, errs[i])
			}
			continue
		}
		if !errors.Is(errs[i], want) {
			t.Fatalf("batch frame %d: err = %v, want %v", i, errs[i], want)
		}
	}

	// No audit record may show an allowed write in generation 2.
	for _, entry := range host.Audit() {
		if entry.Epoch == 2 && entry.Allowed && entry.Kind == FrameBody && entry.HandleID == "b4" {
			t.Fatalf("write must not be allowed after downgrade: %+v", entry)
		}
	}

	// Connection reuse after downgrade: the same socket only keeps the
	// reduced read-only identity, and the audit trail shows both peers.
	if err := host.HandleFrame(handleFrame("c1", privilegedPeer, "b5")); err != nil {
		t.Fatalf("reused-connection handle: %v", err)
	}
	if err := host.HandleFrame(bodyFrame("c1", privilegedPeer, "b5", capRead)); err != nil {
		t.Fatalf("reused-connection read body: %v", err)
	}
	if err := host.HandleFrame(bodyFrame("c1", privilegedPeer, "b5", capWrite)); !errors.Is(err, ErrOrphanBody) {
		t.Fatalf("reused-connection write err = %v, want ErrOrphanBody", err)
	}
}

// TestHost_ConnectionIdentityAuditOnly proves the connect-time identity can
// never substitute for the per-message snapshot: a frame arriving with a
// peer that has no current credential is denied even though the connection
// itself was registered for a privileged peer.
func TestHost_ConnectionIdentityAuditOnly(t *testing.T) {
	host, _ := newTestHost(t)
	host.RegisterConnection("c1", privilegedPeer)

	err := host.HandleFrame(IncomingFrame{
		Connection: "c1",
		Kind:       FrameBody,
		Peer:       unknownPeer,
		Handle:     Handle{ID: "z"},
		Requires:   []Capability{capRead},
	})
	if !errors.Is(err, ErrMissingCredential) {
		t.Fatalf("err = %v, want ErrMissingCredential", err)
	}

	entry := host.Audit()[0]
	if entry.Allowed {
		t.Fatal("frame must be denied")
	}
	if entry.ConnectionPeer != privilegedPeer {
		t.Fatalf("audit must retain connection peer, got %+v", entry.ConnectionPeer)
	}
	if entry.MessagePeer != unknownPeer {
		t.Fatalf("audit must retain per-message peer, got %+v", entry.MessagePeer)
	}

	if err := host.HandleFrame(handleFrame("ghost", privilegedPeer, "g")); !errors.Is(err, ErrUnknownConnection) {
		t.Fatalf("unknown connection err = %v, want ErrUnknownConnection", err)
	}
}

// scriptedTransport emits queued frames, blocking between pushes.
type scriptedTransport struct {
	mu     sync.Mutex
	frames []IncomingFrame
	closed bool
}

func newScriptedTransport() *scriptedTransport {
	return &scriptedTransport{}
}

func (t *scriptedTransport) push(frame IncomingFrame) {
	t.mu.Lock()
	t.frames = append(t.frames, frame)
	t.mu.Unlock()
}

func (t *scriptedTransport) close() {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
}

func (t *scriptedTransport) Receive(ctx context.Context) (IncomingFrame, error) {
	t.mu.Lock()
	for len(t.frames) == 0 && !t.closed {
		t.mu.Unlock()
		select {
		case <-ctx.Done():
			return IncomingFrame{}, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
		t.mu.Lock()
	}
	if len(t.frames) == 0 {
		t.mu.Unlock()
		return IncomingFrame{}, errTransportClosed
	}
	frame := t.frames[0]
	t.frames = t.frames[1:]
	t.mu.Unlock()
	return frame, nil
}

// TestHost_PumpInterleavedWithDowngrade drives the host through the real
// Pump loop while downgrades interleave with receives, including a batch of
// in-flight messages. Run with -race to exercise the locking.
func TestHost_PumpInterleavedWithDowngrade(t *testing.T) {
	host, epochs := newTestHost(t)
	host.RegisterConnection("c1", privilegedPeer)
	transport := newScriptedTransport()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pumpDone := make(chan error, 1)
	go func() { pumpDone <- host.Pump(ctx, transport) }()

	// Generation 1: two messages queued back to back (a receive batch).
	transport.push(handleFrame("c1", privilegedPeer, "p1"))
	transport.push(bodyFrame("c1", privilegedPeer, "p1", capWrite))
	transport.push(handleFrame("c1", privilegedPeer, "p2"))

	waitFor(t, func() bool {
		host.mu.Lock()
		_, parked := host.pending["c1"]["p2"]
		host.mu.Unlock()
		return parked
	})

	// Downgrade while p2's body is still in flight.
	if _, err := epochs.Downgrade(downgradedCreds()); err != nil {
		t.Fatalf("downgrade: %v", err)
	}
	transport.push(bodyFrame("c1", privilegedPeer, "p2", capWrite))

	// Generation 2 traffic on the same reused connection: read-only works,
	// a missing-credential frame is dropped.
	transport.push(handleFrame("c1", privilegedPeer, "p3"))
	transport.push(bodyFrame("c1", privilegedPeer, "p3", capRead))
	transport.push(handleFrame("c1", unknownPeer, "p4"))

	waitFor(t, func() bool {
		entries := host.Audit()
		missing := 0
		for _, entry := range entries {
			if errors.Is(entry.Err, ErrMissingCredential) {
				missing++
			}
		}
		return missing >= 1
	})

	transport.close()
	cancel()
	<-pumpDone

	var allowedWriteEpoch2, deniedMismatch, readOnlyOK bool
	for _, entry := range host.Audit() {
		if entry.Kind == FrameBody && entry.Allowed && entry.Epoch == 2 {
			if entry.HandleID == "p3" {
				readOnlyOK = true
			}
		}
		if entry.Epoch == 2 && errors.Is(entry.Err, ErrGenerationMismatch) {
			deniedMismatch = true
		}
		if entry.Allowed && entry.Epoch >= 2 {
			// Only read-only messages can be allowed in generation 2.
			if entry.HandleID == "p2" {
				allowedWriteEpoch2 = true
			}
		}
	}
	if allowedWriteEpoch2 {
		t.Fatal("old write privilege survived the downgrade via connection reuse")
	}
	if !deniedMismatch {
		t.Fatal("expected ErrGenerationMismatch for in-flight p2 body")
	}
	if !readOnlyOK {
		t.Fatal("read-only generation-2 message should be allowed")
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	for i := 0; i < 1000; i++ {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not reached")
}

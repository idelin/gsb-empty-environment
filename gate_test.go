package netsec

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testLevelSession  Level = 20
	testLevelHardware Level = 30
)

var (
	errBadProof       = errors.New("netsec-test: invalid proof")
	errUpgradeAbandon = errors.New("netsec-test: upgrade abandoned by host")
	testHMACKey       = []byte("unit-test-context-binding-key")
)

// staticVerifier accepts proofs whose HMAC is bound to the exact context id,
// modelling a certificate that can only be valid for one upgrade request.
type staticVerifier struct{ key []byte }

func (v staticVerifier) Verify(_ context.Context, cred Credential) error {
	if !hmac.Equal(cred.Proof, signProof(v.key, cred.ContextID, cred.Level)) {
		return errBadProof
	}
	return nil
}

func signProof(key []byte, contextID string, level Level) []byte {
	mac := hmac.New(sha256.New, key)
	fmt.Fprintf(mac, "%s|%d", contextID, level)
	return mac.Sum(nil)
}

func validCred(contextID string, level Level) Credential {
	return Credential{
		ContextID: contextID,
		Level:     level,
		Proof:     signProof(testHMACKey, contextID, level),
	}
}

// blockVerifier stalls named contexts so tests can drive out-of-order and
// concurrent completion. A missing entry means the context was never expected.
type blockVerifier struct {
	key     []byte
	mu      sync.Mutex
	entries map[string]*blockEntry
	open    map[string]bool
	calls   []string
}

type blockEntry struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockVerifier() *blockVerifier {
	return &blockVerifier{
		key:     testHMACKey,
		entries: make(map[string]*blockEntry),
		open:    make(map[string]bool),
	}
}

// accept marks a context whose verification should pass immediately without
// stalling, so failure paths of sibling contexts stay unblocked.
func (v *blockVerifier) accept(contextID string) {
	v.mu.Lock()
	v.open[contextID] = true
	v.mu.Unlock()
}

func (v *blockVerifier) expect(contextID string) *blockEntry {
	entry := &blockEntry{started: make(chan struct{}), release: make(chan struct{})}
	v.mu.Lock()
	v.entries[contextID] = entry
	v.mu.Unlock()
	return entry
}

func (v *blockVerifier) Verify(ctx context.Context, cred Credential) error {
	v.mu.Lock()
	entry, ok := v.entries[cred.ContextID]
	immediate := v.open[cred.ContextID]
	v.calls = append(v.calls, cred.ContextID)
	v.mu.Unlock()
	if !ok && !immediate {
		return errBadProof
	}
	if ok {
		entry.once.Do(func() { close(entry.started) })
		select {
		case <-entry.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if !hmac.Equal(cred.Proof, signProof(v.key, cred.ContextID, cred.Level)) {
		return errBadProof
	}
	return nil
}

func (v *blockVerifier) saw(contextID string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, seen := range v.calls {
		if seen == contextID {
			return true
		}
	}
	return false
}

type rejection struct {
	msg Message
	err error
}

// recordingSink is the protected application callback. Every payload it sees
// has therefore passed the gate's identity check.
type recordingSink struct {
	mu        sync.Mutex
	cond      *sync.Cond
	delivered [][]byte
	rejected  []rejection
}

func newRecordingSink() *recordingSink {
	s := &recordingSink{}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *recordingSink) Deliver(msg Message) {
	s.mu.Lock()
	s.delivered = append(s.delivered, append([]byte(nil), msg.Payload...))
	s.cond.Broadcast()
	s.mu.Unlock()
}

func (s *recordingSink) Reject(msg Message, err error) {
	s.mu.Lock()
	s.rejected = append(s.rejected, rejection{msg: msg, err: err})
	s.cond.Broadcast()
	s.mu.Unlock()
}

func (s *recordingSink) waitDeliveries(want int) [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for len(s.delivered) < want && time.Now().Before(deadline) {
		s.cond.Wait()
	}
	out := make([][]byte, len(s.delivered))
	copy(out, s.delivered)
	return out
}

func (s *recordingSink) waitRejections(want int) []rejection {
	s.mu.Lock()
	defer s.mu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for len(s.rejected) < want && time.Now().Before(deadline) {
		s.cond.Wait()
	}
	out := make([]rejection, len(s.rejected))
	copy(out, s.rejected)
	return out
}

func (s *recordingSink) snapshot() ([][]byte, []rejection) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delivered := make([][]byte, len(s.delivered))
	copy(delivered, s.delivered)
	rejected := make([]rejection, len(s.rejected))
	copy(rejected, s.rejected)
	return delivered, rejected
}

func msg(seq uint64, level Level, body string) Message {
	return Message{Sequence: seq, RequiredLevel: level, Payload: []byte(body)}
}

func equalPayloads(got [][]byte, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if !bytes.Equal(got[i], []byte(want[i])) {
			return false
		}
	}
	return true
}

func contains(got [][]byte, want string) bool {
	for _, payload := range got {
		if bytes.Equal(payload, []byte(want)) {
			return true
		}
	}
	return false
}

func assertNoWatcherLeak(t *testing.T) {
	t.Helper()
	if testing.Short() {
		return
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	var stack []byte
	for time.Now().Before(deadline) {
		stack = make([]byte, 1<<16)
		n := runtime.Stack(stack, true)
		if !strings.Contains(string(stack[:n]), "watchCancel") {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("watchCancel goroutine leaked:\n%s", stack)
}

func TestUpgradeReleasesQueuedMessagesAndKeepsOldLevelFlowing(t *testing.T) {
	sink := newRecordingSink()
	gate := NewGate(staticVerifier{key: testHMACKey}, sink)

	if err := gate.Submit(msg(1, LevelNone, "open-1")); err != nil {
		t.Fatalf("old-level submit: %v", err)
	}

	id, upgradeCtx, err := gate.BeginUpgrade(context.Background(), testLevelSession)
	if err != nil {
		t.Fatalf("begin upgrade: %v", err)
	}

	if err := gate.Submit(msg(2, testLevelSession, "protected-1")); err != nil {
		t.Fatalf("queued submit: %v", err)
	}
	if err := gate.Submit(msg(3, LevelNone, "open-2")); err != nil {
		t.Fatalf("old-level submit during upgrade: %v", err)
	}
	if err := gate.Submit(msg(4, testLevelSession, "protected-2")); err != nil {
		t.Fatalf("queued submit: %v", err)
	}

	if got := sink.waitDeliveries(2); !equalPayloads(got, []string{"open-1", "open-2"}) {
		t.Fatalf("old-level messages must flow during verification, got %v", got)
	}
	if delivered, rejected := sink.snapshot(); len(rejected) != 0 || contains(delivered, "protected-1") {
		t.Fatalf("protected content leaked before verification: delivered=%v rejected=%d", delivered, len(rejected))
	}

	if err := gate.CompleteUpgrade(context.Background(), id, validCred(id, testLevelSession)); err != nil {
		t.Fatalf("complete upgrade: %v", err)
	}

	want := []string{"open-1", "open-2", "protected-1", "protected-2"}
	if got := sink.waitDeliveries(4); !equalPayloads(got, want) {
		t.Fatalf("queued messages released in arrival order, got %v", got)
	}
	if gate.CurrentLevel() != testLevelSession {
		t.Fatalf("level = %d, want %d", gate.CurrentLevel(), testLevelSession)
	}
	select {
	case <-upgradeCtx.Done():
	default:
		t.Fatal("upgrade context not canceled after success")
	}
	assertNoWatcherLeak(t)
}

func TestOutOfOrderCallbacksTwoContexts(t *testing.T) {
	verifier := newBlockVerifier()
	sink := newRecordingSink()
	gate := NewGate(verifier, sink)

	idA, ctxA, err := gate.BeginUpgrade(context.Background(), testLevelSession)
	if err != nil {
		t.Fatalf("begin A: %v", err)
	}
	idB, ctxB, err := gate.BeginUpgrade(context.Background(), testLevelHardware)
	if err != nil {
		t.Fatalf("begin B: %v", err)
	}
	entryB := verifier.expect(idB)
	verifier.accept(idA)

	if err := gate.Submit(msg(1, testLevelSession, "lvl20-1")); err != nil {
		t.Fatal(err)
	}
	if err := gate.Submit(msg(2, testLevelHardware, "lvl30-1")); err != nil {
		t.Fatal(err)
	}
	if err := gate.Submit(msg(3, testLevelSession, "lvl20-2")); err != nil {
		t.Fatal(err)
	}

	// Credential for B arrives first, but its verification is held open.
	errB := make(chan error, 1)
	go func() {
		errB <- gate.CompleteUpgrade(context.Background(), idB, validCred(idB, testLevelHardware))
	}()
	<-entryB.started

	// A's credential arrives second and verifies immediately.
	if err := gate.CompleteUpgrade(context.Background(), idA, validCred(idA, testLevelSession)); err != nil {
		t.Fatalf("complete A out of order: %v", err)
	}
	if got := sink.waitDeliveries(2); !equalPayloads(got, []string{"lvl20-1", "lvl20-2"}) {
		t.Fatalf("level-20 messages released in order, got %v", got)
	}
	if delivered, _ := sink.snapshot(); contains(delivered, "lvl30-1") {
		t.Fatalf("hardware-level content leaked while B still verifying: %v", delivered)
	}

	close(entryB.release)
	if err := <-errB; err != nil {
		t.Fatalf("complete B: %v", err)
	}
	want := []string{"lvl20-1", "lvl20-2", "lvl30-1"}
	if got := sink.waitDeliveries(3); !equalPayloads(got, want) {
		t.Fatalf("level-30 message released after B, got %v", got)
	}
	if rejected := sink.waitRejections(0); len(rejected) != 0 {
		t.Fatalf("unexpected rejections: %+v", rejected)
	}
	if gate.CurrentLevel() != testLevelHardware {
		t.Fatalf("level = %d, want %d", gate.CurrentLevel(), testLevelHardware)
	}
	if !verifier.saw(idA) || !verifier.saw(idB) {
		t.Fatal("verifier never saw both contexts")
	}
	assertContextDone(t, ctxA, "A")
	assertContextDone(t, ctxB, "B")
	assertNoWatcherLeak(t)
}

func TestDuplicateCompletionSameContextRejected(t *testing.T) {
	sink := newRecordingSink()
	gate := NewGate(staticVerifier{key: testHMACKey}, sink)

	id, _, err := gate.BeginUpgrade(context.Background(), testLevelSession)
	if err != nil {
		t.Fatal(err)
	}
	if err := gate.Submit(msg(1, testLevelSession, "protected")); err != nil {
		t.Fatal(err)
	}

	if err := gate.CompleteUpgrade(context.Background(), id, validCred(id, testLevelSession)); err != nil {
		t.Fatalf("first completion: %v", err)
	}
	if got := sink.waitDeliveries(1); !equalPayloads(got, []string{"protected"}) {
		t.Fatalf("delivery = %v", got)
	}

	// Replayed / duplicate authentication callback for the same context.
	err = gate.CompleteUpgrade(context.Background(), id, validCred(id, testLevelSession))
	if !errors.Is(err, ErrUnknownContext) {
		t.Fatalf("duplicate completion err = %v, want ErrUnknownContext", err)
	}
	err = gate.CancelUpgrade(id, errUpgradeAbandon)
	if !errors.Is(err, ErrUnknownContext) {
		t.Fatalf("cancel after settle err = %v, want ErrUnknownContext", err)
	}
	if delivered, _ := sink.snapshot(); len(delivered) != 1 {
		t.Fatalf("duplicate callback changed sink state: %v", delivered)
	}
	assertNoWatcherLeak(t)
}

func TestFailedVerificationRejectsProtectedMessages(t *testing.T) {
	sink := newRecordingSink()
	gate := NewGate(staticVerifier{key: testHMACKey}, sink)

	id, _, err := gate.BeginUpgrade(context.Background(), testLevelSession)
	if err != nil {
		t.Fatal(err)
	}
	if err := gate.Submit(msg(1, LevelNone, "open")); err != nil {
		t.Fatal(err)
	}
	if err := gate.Submit(msg(2, testLevelSession, "protected")); err != nil {
		t.Fatal(err)
	}

	bad := validCred(id, testLevelSession)
	bad.Proof = []byte("forged")
	err = gate.CompleteUpgrade(context.Background(), id, bad)
	if !errors.Is(err, errBadProof) {
		t.Fatalf("completion err = %v, want errBadProof", err)
	}

	rejected := sink.waitRejections(1)
	if len(rejected) != 1 || !bytes.Equal(rejected[0].msg.Payload, []byte("protected")) {
		t.Fatalf("rejected = %+v", rejected)
	}
	if !errors.Is(rejected[0].err, errBadProof) {
		t.Fatalf("rejection cause = %v", rejected[0].err)
	}
	if delivered, _ := sink.snapshot(); contains(delivered, "protected") {
		t.Fatal("protected content reached callback after failed verification")
	}
	if gate.CurrentLevel() != LevelNone {
		t.Fatalf("level advanced after failure: %d", gate.CurrentLevel())
	}
	assertNoWatcherLeak(t)
}

func TestFailureOfOneContextKeepsOtherContextAlive(t *testing.T) {
	verifier := newBlockVerifier()
	sink := newRecordingSink()
	gate := NewGate(verifier, sink)

	idA, _, err := gate.BeginUpgrade(context.Background(), testLevelSession)
	if err != nil {
		t.Fatal(err)
	}
	idB, _, err := gate.BeginUpgrade(context.Background(), testLevelHardware)
	if err != nil {
		t.Fatal(err)
	}
	verifier.accept(idB)

	if err := gate.Submit(msg(1, testLevelSession, "lvl20")); err != nil {
		t.Fatal(err)
	}
	if err := gate.Submit(msg(2, testLevelHardware, "lvl30")); err != nil {
		t.Fatal(err)
	}

	// A fails; its level-20 message stays queued because B still targets a
	// stronger level that would satisfy it.
	if err := gate.CompleteUpgrade(context.Background(), idA, Credential{ContextID: idA, Level: testLevelSession}); !errors.Is(err, errBadProof) {
		t.Fatalf("A failure err = %v, want errBadProof", err)
	}
	if _, rejected := sink.snapshot(); len(rejected) != 0 {
		t.Fatalf("no message should be rejected while B is live: %+v", rejected)
	}

	// B succeeds and releases every queued message together.
	if err := gate.CompleteUpgrade(context.Background(), idB, validCred(idB, testLevelHardware)); err != nil {
		t.Fatalf("complete B: %v", err)
	}
	want := []string{"lvl20", "lvl30"}
	if got := sink.waitDeliveries(2); !equalPayloads(got, want) {
		t.Fatalf("released = %v, want %v", got, want)
	}
	assertNoWatcherLeak(t)
}

func TestCancelUpgradeRejectsUnreachableMessages(t *testing.T) {
	sink := newRecordingSink()
	gate := NewGate(staticVerifier{key: testHMACKey}, sink)

	id, upgradeCtx, err := gate.BeginUpgrade(context.Background(), testLevelSession)
	if err != nil {
		t.Fatal(err)
	}
	if err := gate.Submit(msg(1, LevelNone, "open")); err != nil {
		t.Fatal(err)
	}
	if err := gate.Submit(msg(2, testLevelSession, "protected")); err != nil {
		t.Fatal(err)
	}

	if err := gate.CancelUpgrade(id, errUpgradeAbandon); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	rejected := sink.waitRejections(1)
	if len(rejected) != 1 || !bytes.Equal(rejected[0].msg.Payload, []byte("protected")) {
		t.Fatalf("rejected = %+v", rejected)
	}
	if !errors.Is(rejected[0].err, errUpgradeAbandon) {
		t.Fatalf("rejection cause = %v", rejected[0].err)
	}
	if delivered, _ := sink.snapshot(); contains(delivered, "protected") {
		t.Fatal("protected content reached callback after cancellation")
	}
	assertContextDone(t, upgradeCtx, "canceled")

	// A late credential for the canceled context must be refused.
	err = gate.CompleteUpgrade(context.Background(), id, validCred(id, testLevelSession))
	if !errors.Is(err, ErrUnknownContext) {
		t.Fatalf("late credential err = %v, want ErrUnknownContext", err)
	}
	if gate.CurrentLevel() != LevelNone {
		t.Fatalf("level advanced after cancellation: %d", gate.CurrentLevel())
	}
	assertNoWatcherLeak(t)
}

func TestParentContextCancellationSettlesUpgrade(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	sink := newRecordingSink()
	gate := NewGate(staticVerifier{key: testHMACKey}, sink)

	id, upgradeCtx, err := gate.BeginUpgrade(parent, testLevelSession)
	if err != nil {
		t.Fatal(err)
	}
	if err := gate.Submit(msg(1, testLevelSession, "protected")); err != nil {
		t.Fatal(err)
	}

	cancelParent()

	rejected := sink.waitRejections(1)
	if len(rejected) != 1 {
		t.Fatalf("rejected = %+v", rejected)
	}
	assertContextDone(t, upgradeCtx, "parent-canceled")

	if err := gate.CancelUpgrade(id, errUpgradeAbandon); !errors.Is(err, ErrUnknownContext) {
		t.Fatalf("cancel after parent cancel err = %v", err)
	}
	if delivered, _ := sink.snapshot(); len(delivered) != 0 {
		t.Fatalf("protected content leaked: %v", delivered)
	}
	assertNoWatcherLeak(t)
}

func TestHalfCloseFailsEverythingAndClosesGate(t *testing.T) {
	sink := newRecordingSink()
	gate := NewGate(staticVerifier{key: testHMACKey}, sink)

	id, upgradeCtx, err := gate.BeginUpgrade(context.Background(), testLevelSession)
	if err != nil {
		t.Fatal(err)
	}
	if err := gate.Submit(msg(1, LevelNone, "open")); err != nil {
		t.Fatal(err)
	}
	if err := gate.Submit(msg(2, testLevelSession, "held")); err != nil {
		t.Fatal(err)
	}

	if err := gate.HalfClose(); err != nil {
		t.Fatalf("half close: %v", err)
	}

	rejected := sink.waitRejections(1)
	if len(rejected) != 1 || !bytes.Equal(rejected[0].msg.Payload, []byte("held")) {
		t.Fatalf("rejected = %+v", rejected)
	}
	if !errors.Is(rejected[0].err, ErrClosed) {
		t.Fatalf("rejection cause = %v", rejected[0].err)
	}
	if delivered, _ := sink.snapshot(); contains(delivered, "held") {
		t.Fatal("held content reached callback after half-close")
	}
	assertContextDone(t, upgradeCtx, "half-closed")

	if err := gate.Submit(msg(3, LevelNone, "late")); !errors.Is(err, ErrClosed) {
		t.Fatalf("submit after close err = %v", err)
	}
	if _, _, err := gate.BeginUpgrade(context.Background(), testLevelSession); !errors.Is(err, ErrClosed) {
		t.Fatalf("begin after close err = %v", err)
	}
	if err := gate.CompleteUpgrade(context.Background(), id, validCred(id, testLevelSession)); !errors.Is(err, ErrClosed) {
		t.Fatalf("completion after close err = %v", err)
	}
	if err := gate.HalfClose(); err != nil {
		t.Fatalf("idempotent half close: %v", err)
	}
	assertNoWatcherLeak(t)
}

func TestCredentialBoundToDifferentContextRejected(t *testing.T) {
	verifier := newBlockVerifier()
	sink := newRecordingSink()
	gate := NewGate(verifier, sink)

	idA, _, err := gate.BeginUpgrade(context.Background(), testLevelSession)
	if err != nil {
		t.Fatal(err)
	}
	idB, _, err := gate.BeginUpgrade(context.Background(), testLevelHardware)
	if err != nil {
		t.Fatal(err)
	}
	verifier.accept(idA)
	verifier.accept(idB)
	if err := gate.Submit(msg(1, testLevelSession, "protected")); err != nil {
		t.Fatal(err)
	}

	// Present B's context-bound certificate against A's request.
	err = gate.CompleteUpgrade(context.Background(), idA, validCred(idB, testLevelHardware))
	if !errors.Is(err, ErrContextMismatch) {
		t.Fatalf("mismatch err = %v, want ErrContextMismatch", err)
	}
	if verifier.saw(idA) || verifier.saw(idB) {
		t.Fatal("verifier must not run for a context-mismatched credential")
	}

	// Both contexts are still live and can complete correctly.
	if err := gate.CompleteUpgrade(context.Background(), idA, validCred(idA, testLevelSession)); err != nil {
		t.Fatalf("complete A after mismatch: %v", err)
	}
	if got := sink.waitDeliveries(1); !equalPayloads(got, []string{"protected"}) {
		t.Fatalf("delivery = %v", got)
	}
	if err := gate.CompleteUpgrade(context.Background(), idB, validCred(idB, testLevelHardware)); err != nil {
		t.Fatalf("complete B: %v", err)
	}
	assertNoWatcherLeak(t)
}

func TestUnknownContextAndRedundantUpgrade(t *testing.T) {
	sink := newRecordingSink()
	gate := NewGate(staticVerifier{key: testHMACKey}, sink)

	err := gate.CompleteUpgrade(context.Background(), "never-issued", Credential{ContextID: "never-issued"})
	if !errors.Is(err, ErrUnknownContext) {
		t.Fatalf("unknown context err = %v", err)
	}
	if err := gate.CancelUpgrade("never-issued", errUpgradeAbandon); !errors.Is(err, ErrUnknownContext) {
		t.Fatalf("unknown cancel err = %v", err)
	}
	if _, _, err := gate.BeginUpgrade(context.Background(), LevelNone); !errors.Is(err, ErrLevelAlreadyGranted) {
		t.Fatalf("zero-level upgrade err = %v", err)
	}

	id, _, err := gate.BeginUpgrade(context.Background(), testLevelSession)
	if err != nil {
		t.Fatal(err)
	}
	if err := gate.CompleteUpgrade(context.Background(), id, validCred(id, testLevelSession)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := gate.BeginUpgrade(context.Background(), testLevelSession); !errors.Is(err, ErrLevelAlreadyGranted) {
		t.Fatalf("redundant upgrade err = %v", err)
	}
	assertNoWatcherLeak(t)
}

func TestVerificationContextCanceledMidFlight(t *testing.T) {
	verifier := newBlockVerifier()
	sink := newRecordingSink()
	gate := NewGate(verifier, sink)

	id, _, err := gate.BeginUpgrade(context.Background(), testLevelSession)
	if err != nil {
		t.Fatal(err)
	}
	entry := verifier.expect(id)
	if err := gate.Submit(msg(1, testLevelSession, "protected")); err != nil {
		t.Fatal(err)
	}

	verifyCtx, cancelVerify := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- gate.CompleteUpgrade(verifyCtx, id, validCred(id, testLevelSession))
	}()
	<-entry.started
	cancelVerify()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-flight cancel err = %v", err)
	}
	rejected := sink.waitRejections(1)
	if len(rejected) != 1 {
		t.Fatalf("rejected = %+v", rejected)
	}
	if delivered, _ := sink.snapshot(); len(delivered) != 0 {
		t.Fatalf("protected content leaked: %v", delivered)
	}
	if gate.CurrentLevel() != LevelNone {
		t.Fatalf("level = %d after failed verification", gate.CurrentLevel())
	}
	assertNoWatcherLeak(t)
}

func assertContextDone(t *testing.T, ctx context.Context, name string) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatalf("context %s was not canceled", name)
	}
}

// TestConcurrentSubmitAndUpgrades hammers the gate from many goroutines to
// prove that queued messages are delivered at most once and that no payload
// ever reaches Deliver before its required level has been granted.
func TestConcurrentSubmitAndUpgrades(t *testing.T) {
	type counterSink struct {
		mu        sync.Mutex
		delivered map[string]int
		rejected  int
	}
	sink := &counterSink{delivered: make(map[string]int)}
	gate := NewGate(staticVerifier{key: testHMACKey}, SinkFunc(
		func(m Message) {
			sink.mu.Lock()
			sink.delivered[string(m.Payload)]++
			sink.mu.Unlock()
		},
		func(Message, error) {
			sink.mu.Lock()
			sink.rejected++
			sink.mu.Unlock()
		},
	))

	const writers = 8
	const perWriter = 40
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				body := fmt.Sprintf("w%d-m%d", w, i)
				if err := gate.Submit(msg(uint64(w*perWriter+i), testLevelSession, body)); err != nil {
					return
				}
			}
		}(w)
	}

	// Several doomed upgrade attempts followed by one that succeeds.
	for i := 0; i < 3; i++ {
		id, _, err := gate.BeginUpgrade(context.Background(), testLevelSession)
		if err != nil {
			t.Fatalf("begin %d: %v", i, err)
		}
		if err := gate.CancelUpgrade(id, errUpgradeAbandon); err != nil {
			t.Fatalf("cancel %d: %v", i, err)
		}
	}
	id, _, err := gate.BeginUpgrade(context.Background(), testLevelSession)
	if err != nil {
		t.Fatalf("final begin: %v", err)
	}
	if err := gate.CompleteUpgrade(context.Background(), id, validCred(id, testLevelSession)); err != nil {
		t.Fatalf("final completion: %v", err)
	}

	wg.Wait()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sink.mu.Lock()
		total := 0
		for _, n := range sink.delivered {
			total += n
		}
		sink.mu.Unlock()
		if total+sink.rejected == writers*perWriter {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	sink.mu.Lock()
	total, duplicates := 0, 0
	for body, n := range sink.delivered {
		total += n
		if n != 1 {
			duplicates++
			t.Errorf("payload %q delivered %d times", body, n)
		}
	}
	rejected := sink.rejected
	sink.mu.Unlock()

	if total+rejected != writers*perWriter {
		t.Fatalf("accounted = %d delivered + %d rejected, want %d", total, rejected, writers*perWriter)
	}
	if duplicates != 0 {
		t.Fatalf("duplicate deliveries detected")
	}
	assertNoWatcherLeak(t)
}

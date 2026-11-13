package netsec

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"
)

var (
	keyAID = KeyID("master-a-key")
	keyBID = KeyID("master-b-key")
	keyA   = []byte("0123456789abcdef0123456789abcdef")
	keyB   = []byte("fedcba9876543210fedcba9876543210")
)

func secretForMaster(master uint16) []byte {
	if master == 0x0202 {
		return keyB
	}
	return keyA
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Unix(1_700_000_000, 0).UTC()}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type executionRecord struct {
	identity []byte
	macInput []byte
	exec     Execution
}

type recorder struct {
	mu      sync.Mutex
	records []executionRecord
	payload []byte
	err     error
}

func (r *recorder) Execute(exec Execution) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, executionRecord{
		identity: MarshalIdentity(exec.Identity),
		macInput: append([]byte(nil), exec.MACInput()...),
		exec:     exec,
	})
	return cloneBytes(r.payload), r.err
}

func (r *recorder) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.records)
}

func (r *recorder) Last() executionRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.records[len(r.records)-1]
}

func newGateway(t *testing.T, clock *testClock, exec Executor) *Gateway {
	t.Helper()
	if exec == nil {
		exec = func(Execution) ([]byte, error) { return nil, nil }
	}
	gw, err := NewGateway(Config{
		Keys:               map[KeyID][]byte{keyAID: keyA, keyBID: keyB},
		KeyByMaster:        map[uint16]KeyID{0x0101: keyAID, 0x0202: keyBID},
		ChallengeTTL:       10 * time.Second,
		FailureWindow:      time.Minute,
		MaxFailedResponses: 100,
		Clock:              clock.Now,
		Executor:           exec,
		PolicyVersion:      "policy-v1",
	})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	return gw
}

func requestIdentity(master, outstation uint16, generation uint64, seq uint32, function, group, variation uint8, index uint32, value string) RequestIdentity {
	return RequestIdentity{
		MasterAddress:        master,
		OutstationAddress:    outstation,
		ConnectionGeneration: generation,
		ApplicationSequence:  seq,
		FunctionCode:         function,
		ObjectGroup:          group,
		ObjectVariation:      variation,
		ObjectIndex:          index,
		OperationValue:       []byte(value),
	}
}

func responseFor(challenge *Challenge, identity RequestIdentity, secret []byte) Response {
	return Response{
		ChallengeID: challenge.ChallengeID,
		KeyID:       challenge.KeyID,
		MAC: ComputeResponseMAC(
			secret,
			identity,
			challenge.Nonce,
			challenge.ChallengeID,
			challenge.KeyID,
		),
	}
}

func TestInterleavedMastersFunctionsAndObjects(t *testing.T) {
	clock := newTestClock()
	rec := &recorder{payload: []byte("applied")}
	gw := newGateway(t, clock, rec.Execute)

	requests := []struct {
		req RequestIdentity
	}{
		{requestIdentity(0x0101, 0x00AA, 7, 1, 0x05, 1, 1, 10, "master-a/function-5/object-1")},
		{requestIdentity(0x0202, 0x00BB, 9, 1, 0x06, 2, 2, 20, "master-b/function-6/object-2")},
		{requestIdentity(0x0101, 0x00AA, 7, 2, 0x06, 2, 2, 20, "master-a/function-6/object-2")},
		{requestIdentity(0x0202, 0x00BB, 9, 2, 0x05, 1, 1, 10, "master-b/function-5/object-1")},
	}

	challenges := make([]*Challenge, len(requests))
	for i, item := range requests {
		ch, err := gw.Submit(item.req)
		if err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
		challenges[i] = ch
	}
	if got := gw.PendingCount(); got != len(requests) {
		t.Fatalf("pending count = %d, want %d", got, len(requests))
	}
	if rec.Count() != 0 {
		t.Fatalf("Submit executed an operation before authentication")
	}

	// Interleave responses in a deliberately different order.
	order := []int{3, 0, 1, 2}
	for _, i := range order {
		ch := challenges[i]
		resp := responseFor(ch, ch.Identity, secretForMaster(ch.Identity.MasterAddress))
		receipt, err := gw.Respond(resp)
		if err != nil {
			t.Fatalf("Respond %d: %v", i, err)
		}
		if string(receipt.Payload) != "applied" {
			t.Fatalf("receipt payload = %q", receipt.Payload)
		}
		if receipt.ChallengeID != ch.ChallengeID {
			t.Fatalf("receipt challenge id mismatch")
		}
	}
	if rec.Count() != len(requests) {
		t.Fatalf("executions = %d, want %d", rec.Count(), len(requests))
	}

	// Every callback record must be byte-for-byte the identity protected by
	// the MAC, including the operation value.
	for i, ch := range challenges {
		found := false
		for _, call := range rec.records {
			if call.exec.ChallengeID == ch.ChallengeID {
				found = true
				if !bytes.Equal(call.identity, MarshalIdentity(ch.Identity)) {
					t.Fatalf("challenge %d callback identity differs from challenged identity", i)
				}
				if !bytes.Equal(call.identity, MarshalIdentity(call.exec.Identity)) {
					t.Fatalf("challenge %d callback identity bytes are unstable", i)
				}
				if !bytes.Equal(call.macInput, MACInput(ch.Identity, ch.Nonce, ch.ChallengeID, ch.KeyID)) {
					t.Fatalf("challenge %d callback MAC input differs from challenged bytes", i)
				}
				break
			}
		}
		if !found {
			t.Fatalf("missing callback for challenge %d", i)
		}
	}
}

func TestResponseCannotBeMovedToAnotherRequest(t *testing.T) {
	clock := newTestClock()
	rec := &recorder{}
	gw := newGateway(t, clock, rec.Execute)

	chA, err := gw.Submit(requestIdentity(0x0101, 0x00AA, 7, 1, 0x05, 1, 1, 10, "A"))
	if err != nil {
		t.Fatalf("Submit A: %v", err)
	}
	chB, err := gw.Submit(requestIdentity(0x0101, 0x00AA, 7, 2, 0x06, 2, 2, 20, "B"))
	if err != nil {
		t.Fatalf("Submit B: %v", err)
	}

	// Valid MAC for A, but the challenge id is replaced with B's.  A response is
	// therefore not accepted for the other function code/object.
	resp := responseFor(chA, chA.Identity, secretForMaster(chA.Identity.MasterAddress))
	resp.ChallengeID = chB.ChallengeID
	if _, err := gw.Respond(resp); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("transplanted response error = %v, want ErrAuthenticationFailed", err)
	}
	if rec.Count() != 0 {
		t.Fatalf("transplanted response executed the callback")
	}
	if status, ok := gw.Status(chB.ChallengeID); !ok || status.State != StatePending {
		t.Fatalf("challenge B status = %+v, want pending", status)
	}

	// A fresh, correct response for B still works after the failed attempt.
	if _, err := gw.Respond(responseFor(chB, chB.Identity, secretForMaster(chB.Identity.MasterAddress))); err != nil {
		t.Fatalf("valid response B: %v", err)
	}
	if rec.Count() != 1 {
		t.Fatalf("executions = %d, want 1", rec.Count())
	}
}

func TestFieldRewriteDoesNotAuthorise(t *testing.T) {
	clock := newTestClock()
	rec := &recorder{}
	gw := newGateway(t, clock, rec.Execute)

	ch, err := gw.Submit(requestIdentity(0x0101, 0x00AA, 7, 1, 0x05, 1, 1, 10, "original"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	rewritten := ch.Identity
	rewritten.ApplicationSequence = 2
	rewritten.ConnectionGeneration = 8
	rewritten.FunctionCode = 0x06
	rewritten.ObjectGroup = 2
	rewritten.ObjectVariation = 2
	rewritten.ObjectIndex = 99
	rewritten.OperationValue = []byte("rewritten")
	resp := responseFor(ch, rewritten, secretForMaster(ch.Identity.MasterAddress))
	if _, err := gw.Respond(resp); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("field-rewrite response error = %v, want ErrAuthenticationFailed", err)
	}
	if rec.Count() != 0 {
		t.Fatalf("field-rewrite response executed")
	}

	// The originally challenged identity remains intact and can be executed.
	if _, err := gw.Respond(responseFor(ch, ch.Identity, secretForMaster(ch.Identity.MasterAddress))); err != nil {
		t.Fatalf("original response: %v", err)
	}
	if got := string(rec.Last().exec.Identity.OperationValue); got != "original" {
		t.Fatalf("executed operation value = %q", got)
	}
}

func TestResponseCannotCrossMasterKeyBoundary(t *testing.T) {
	clock := newTestClock()
	rec := &recorder{}
	gw := newGateway(t, clock, rec.Execute)

	chA, err := gw.Submit(requestIdentity(0x0101, 0x00AA, 7, 1, 0x05, 1, 1, 10, "master-a"))
	if err != nil {
		t.Fatalf("Submit A: %v", err)
	}
	chB, err := gw.Submit(requestIdentity(0x0202, 0x00BB, 9, 1, 0x05, 1, 1, 10, "master-b"))
	if err != nil {
		t.Fatalf("Submit B: %v", err)
	}
	if chA.KeyID != keyAID || chB.KeyID != keyBID {
		t.Fatalf("key selection = %q/%q", chA.KeyID, chB.KeyID)
	}

	// A response carrying master B's key id cannot select master A's challenge.
	transplant := responseFor(chB, chB.Identity, keyB)
	transplant.ChallengeID = chA.ChallengeID
	if _, err := gw.Respond(transplant); !errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("cross-master key id error = %v, want ErrKeyMismatch", err)
	}

	// Relabelling the key id while retaining the other master's MAC fails.
	relabelled := transplant
	relabelled.KeyID = chA.KeyID
	if _, err := gw.Respond(relabelled); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("relabelled key error = %v, want ErrAuthenticationFailed", err)
	}
	if rec.Count() != 0 {
		t.Fatalf("cross-master responses executed")
	}

	if _, err := gw.Respond(responseFor(chA, chA.Identity, keyA)); err != nil {
		t.Fatalf("valid master A response: %v", err)
	}
	if rec.Count() != 1 {
		t.Fatalf("executions = %d, want 1", rec.Count())
	}
}

func TestRetransmissionReturnsOriginalReceiptAndExecutesOnce(t *testing.T) {
	clock := newTestClock()
	rec := &recorder{payload: []byte("receipt-data")}
	gw := newGateway(t, clock, rec.Execute)

	ch, err := gw.Submit(requestIdentity(0x0101, 0x00AA, 7, 1, 0x05, 1, 1, 10, "retransmit"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	resp := responseFor(ch, ch.Identity, secretForMaster(ch.Identity.MasterAddress))

	first, err := gw.Respond(resp)
	if err != nil {
		t.Fatalf("first response: %v", err)
	}
	second, err := gw.Respond(resp) // receipt was "lost" on the wire
	if err != nil {
		t.Fatalf("retransmitted response: %v", err)
	}
	if rec.Count() != 1 {
		t.Fatalf("executions = %d, want 1", rec.Count())
	}
	if !bytes.Equal(first.Payload, second.Payload) ||
		first.ChallengeID != second.ChallengeID ||
		first.ExecutedAt != second.ExecutedAt ||
		!bytes.Equal(MarshalIdentity(first.Identity), MarshalIdentity(second.Identity)) {
		t.Fatalf("retransmission did not return the original receipt")
	}
	if stored, ok := gw.ReceiptFor(ch.ChallengeID); !ok || !bytes.Equal(stored.Payload, first.Payload) {
		t.Fatalf("stored receipt missing or different")
	}
}

func TestChallengeReplayAfterCompletionDoesNotExecuteAgain(t *testing.T) {
	clock := newTestClock()
	rec := &recorder{}
	gw := newGateway(t, clock, rec.Execute)

	ch, err := gw.Submit(requestIdentity(0x0101, 0x00AA, 7, 1, 0x05, 1, 1, 10, "replay"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	resp := responseFor(ch, ch.Identity, secretForMaster(ch.Identity.MasterAddress))
	for i := 0; i < 5; i++ {
		if _, err := gw.Respond(resp); err != nil {
			t.Fatalf("Respond %d: %v", i, err)
		}
	}
	if rec.Count() != 1 {
		t.Fatalf("executions = %d, want 1", rec.Count())
	}
}

func TestFragmentsMustBeCompleteBeforeChallenge(t *testing.T) {
	clock := newTestClock()
	rec := &recorder{}
	gw := newGateway(t, clock, rec.Execute)

	base := Fragment{
		MasterAddress:        0x0101,
		OutstationAddress:    0x00AA,
		ConnectionGeneration: 7,
		ApplicationSequence:  1,
		FunctionCode:         0x05,
		ObjectGroup:          1,
		ObjectVariation:      1,
		ObjectIndex:          10,
	}

	first := base
	first.FragmentIndex = 0
	first.Data = []byte("frag-0|")
	ch, complete, err := gw.AddFragment(first)
	if err != nil {
		t.Fatalf("fragment 0: %v", err)
	}
	if complete || ch != nil {
		t.Fatalf("partial request produced a challenge")
	}
	if gw.PendingCount() != 0 {
		t.Fatalf("partial request entered pending")
	}

	second := base
	second.FragmentIndex = 1
	second.Data = []byte("frag-1|")
	ch, complete, err = gw.AddFragment(second)
	if err != nil {
		t.Fatalf("fragment 1: %v", err)
	}
	if complete || ch != nil {
		t.Fatalf("partial request produced a challenge")
	}

	last := base
	last.FragmentIndex = 2
	last.Final = true
	last.Data = []byte("frag-2")
	ch, complete, err = gw.AddFragment(last)
	if err != nil {
		t.Fatalf("fragment 2: %v", err)
	}
	if !complete || ch == nil {
		t.Fatalf("final fragment did not freeze the request")
	}
	if got, want := string(ch.Identity.OperationValue), "frag-0|frag-1|frag-2"; got != want {
		t.Fatalf("reassembled value = %q, want %q", got, want)
	}
	if ch.Identity.RequestDigest != ComputeRequestDigest(ch.Identity) {
		t.Fatalf("fragment digest was not frozen")
	}

	receipt, err := gw.Respond(responseFor(ch, ch.Identity, secretForMaster(ch.Identity.MasterAddress)))
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if rec.Count() != 1 {
		t.Fatalf("executions = %d, want 1", rec.Count())
	}
	if !bytes.Equal(rec.Last().identity, MarshalIdentity(ch.Identity)) {
		t.Fatalf("callback identity does not match frozen fragment identity")
	}
	if !bytes.Equal(rec.Last().macInput, MACInput(ch.Identity, ch.Nonce, ch.ChallengeID, ch.KeyID)) {
		t.Fatalf("callback MAC input does not match challenge")
	}
	if !bytes.Equal(receipt.Identity.OperationValue, ch.Identity.OperationValue) {
		t.Fatalf("receipt operation value differs from frozen request")
	}
}

func TestFragmentSubstitutionAndDuplicatesAreRejected(t *testing.T) {
	clock := newTestClock()
	gw := newGateway(t, clock, nil)

	base := Fragment{
		MasterAddress:        0x0101,
		OutstationAddress:    0x00AA,
		ConnectionGeneration: 7,
		ApplicationSequence:  1,
		FunctionCode:         0x05,
		ObjectGroup:          1,
		ObjectVariation:      1,
		ObjectIndex:          10,
	}

	first := base
	first.FragmentIndex = 0
	first.Data = []byte("first")
	if _, complete, err := gw.AddFragment(first); err != nil || complete {
		t.Fatalf("first fragment: complete=%v err=%v", complete, err)
	}

	// Replace the continuation with a fragment for a different object.
	replacement := base
	replacement.ObjectGroup = 2
	replacement.ObjectVariation = 2
	replacement.ObjectIndex = 20
	replacement.FragmentIndex = 1
	replacement.Final = true
	replacement.Data = []byte("replacement")
	if _, _, err := gw.AddFragment(replacement); !errors.Is(err, ErrFragmentMismatch) {
		t.Fatalf("fragment substitution error = %v, want ErrFragmentMismatch", err)
	}

	// The original partial assembly is discarded; a lone final fragment cannot
	// become a challenge.
	late := first
	late.FragmentIndex = 2
	late.Final = true
	if _, _, err := gw.AddFragment(late); !errors.Is(err, ErrFragmentGap) {
		t.Fatalf("after substitution error = %v, want ErrFragmentGap", err)
	}

	// A duplicate index within one assembly is also discarded.
	first = base
	first.FragmentIndex = 0
	first.Data = []byte("chunk-0")
	if _, _, err := gw.AddFragment(first); err != nil {
		t.Fatalf("restart fragment 0: %v", err)
	}
	second := base
	second.FragmentIndex = 1
	second.Data = []byte("chunk-1")
	if _, _, err := gw.AddFragment(second); err != nil {
		t.Fatalf("fragment 1: %v", err)
	}
	duplicate := second
	duplicate.Data = []byte("replaced-chunk")
	if _, _, err := gw.AddFragment(duplicate); !errors.Is(err, ErrDuplicateFragment) {
		t.Fatalf("duplicate fragment error = %v, want ErrDuplicateFragment", err)
	}
}

func TestCancellationOnTimeoutResetPolicyAndSequenceReuse(t *testing.T) {
	clock := newTestClock()
	rec := &recorder{}
	gw := newGateway(t, clock, rec.Execute)

	timeoutCh, err := gw.Submit(requestIdentity(0x0101, 0x00AA, 7, 1, 0x05, 1, 1, 10, "timeout"))
	if err != nil {
		t.Fatalf("Submit timeout: %v", err)
	}
	clock.Advance(11 * time.Second)
	if _, err := gw.Respond(responseFor(timeoutCh, timeoutCh.Identity, secretForMaster(timeoutCh.Identity.MasterAddress))); !errors.Is(err, ErrChallengeExpired) {
		t.Fatalf("timeout error = %v, want ErrChallengeExpired", err)
	}
	if status, ok := gw.Status(timeoutCh.ChallengeID); !ok || status.State != StateCanceled || status.CancelReason != CancelExpired {
		t.Fatalf("timeout status = %+v ok=%v", status, ok)
	}

	resetCh, err := gw.Submit(requestIdentity(0x0101, 0x00AA, 8, 1, 0x05, 1, 1, 10, "reset"))
	if err != nil {
		t.Fatalf("Submit reset: %v", err)
	}
	if n := gw.ResetConnection(0x0101, 0x00AA, 8); n != 1 {
		t.Fatalf("ResetConnection canceled %d, want 1", n)
	}
	if _, err := gw.Respond(responseFor(resetCh, resetCh.Identity, secretForMaster(resetCh.Identity.MasterAddress))); !errors.Is(err, ErrChallengeCanceled) {
		t.Fatalf("reset error = %v, want ErrChallengeCanceled", err)
	}
	if _, err := gw.Submit(requestIdentity(0x0101, 0x00AA, 8, 2, 0x05, 1, 1, 10, "stale-generation")); !errors.Is(err, ErrConnectionReset) {
		t.Fatalf("stale generation error = %v, want ErrConnectionReset", err)
	}

	policyCh, err := gw.Submit(requestIdentity(0x0101, 0x00AA, 9, 1, 0x05, 1, 1, 10, "policy"))
	if err != nil {
		t.Fatalf("Submit policy: %v", err)
	}
	if n := gw.SetPolicyVersion("policy-v2"); n != 1 {
		t.Fatalf("SetPolicyVersion canceled %d, want 1", n)
	}
	if _, err := gw.Respond(responseFor(policyCh, policyCh.Identity, secretForMaster(policyCh.Identity.MasterAddress))); !errors.Is(err, ErrChallengeCanceled) {
		t.Fatalf("policy error = %v, want ErrChallengeCanceled", err)
	}
	if status, _ := gw.Status(policyCh.ChallengeID); status.State != StateCanceled || status.CancelReason != CancelPolicyChange {
		t.Fatalf("policy status = %+v", status)
	}

	executedCh, err := gw.Submit(requestIdentity(0x0202, 0x00BB, 10, 1, 0x06, 2, 2, 20, "reuse"))
	if err != nil {
		t.Fatalf("Submit executed: %v", err)
	}
	if _, err := gw.Respond(responseFor(executedCh, executedCh.Identity, secretForMaster(executedCh.Identity.MasterAddress))); err != nil {
		t.Fatalf("Respond executed: %v", err)
	}
	if _, err := gw.Submit(requestIdentity(0x0202, 0x00BB, 10, 1, 0x06, 2, 2, 20, "reuse-again")); !errors.Is(err, ErrSequenceReuse) {
		t.Fatalf("sequence reuse error = %v, want ErrSequenceReuse", err)
	}

	occupiedCh, err := gw.Submit(requestIdentity(0x0202, 0x00BB, 10, 2, 0x05, 1, 1, 10, "occupied"))
	if err != nil {
		t.Fatalf("Submit occupied: %v", err)
	}
	if _, err := gw.Submit(requestIdentity(0x0202, 0x00BB, 10, 2, 0x06, 2, 2, 20, "conflict")); !errors.Is(err, ErrSequenceOccupied) {
		t.Fatalf("occupied error = %v, want ErrSequenceOccupied", err)
	}
	if _, err := gw.Respond(responseFor(occupiedCh, occupiedCh.Identity, secretForMaster(occupiedCh.Identity.MasterAddress))); !errors.Is(err, ErrChallengeCanceled) {
		t.Fatalf("occupied response error = %v, want ErrChallengeCanceled", err)
	}
	if rec.Count() != 1 {
		t.Fatalf("executions = %d, want 1", rec.Count())
	}
}

func TestFailedResponsesAreRateLimited(t *testing.T) {
	clock := newTestClock()
	rec := &recorder{}
	gw, err := NewGateway(Config{
		Keys:               map[KeyID][]byte{keyAID: keyA},
		DefaultKeyID:       keyAID,
		ChallengeTTL:       10 * time.Minute,
		MaxFailedResponses: 3,
		FailureWindow:      time.Minute,
		Clock:              clock.Now,
		Executor:           rec.Execute,
	})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	ch, err := gw.Submit(requestIdentity(0x0101, 0x00AA, 7, 1, 0x05, 1, 1, 10, "rate"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	bad := responseFor(ch, ch.Identity, secretForMaster(ch.Identity.MasterAddress))
	bad.MAC[0] ^= 0xFF
	for i := 0; i < 3; i++ {
		if _, err := gw.Respond(bad); !errors.Is(err, ErrAuthenticationFailed) {
			t.Fatalf("bad response %d error = %v", i, err)
		}
	}
	if rec.Count() != 0 {
		t.Fatalf("bad responses executed")
	}
	if _, err := gw.Respond(responseFor(ch, ch.Identity, secretForMaster(ch.Identity.MasterAddress))); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected rate limit, got %v", err)
	}
	if rec.Count() != 0 {
		t.Fatalf("rate-limited response executed")
	}

	// Rate limiting is per master: a healthy master is unaffected.
	other, err := gw.Submit(requestIdentity(0x0202, 0x00BB, 7, 1, 0x06, 2, 2, 20, "other-master"))
	if err != nil {
		t.Fatalf("Submit other master: %v", err)
	}
	if _, err := gw.Respond(responseFor(other, other.Identity, keyA)); err != nil {
		t.Fatalf("other master response: %v", err)
	}
	if rec.Count() != 1 {
		t.Fatalf("other master executions = %d, want 1", rec.Count())
	}

	clock.Advance(time.Minute + time.Second)
	if _, err := gw.Respond(responseFor(ch, ch.Identity, secretForMaster(ch.Identity.MasterAddress))); err != nil {
		t.Fatalf("response after window: %v", err)
	}
	if rec.Count() != 2 {
		t.Fatalf("executions = %d, want 2", rec.Count())
	}
}

func TestDigestAndConnectionGenerationBinding(t *testing.T) {
	clock := newTestClock()
	gw := newGateway(t, clock, nil)

	badDigest := requestIdentity(0x0101, 0x00AA, 7, 1, 0x05, 1, 1, 10, "bad")
	badDigest.RequestDigest = [32]byte{0xFF}
	if _, err := gw.Submit(badDigest); !errors.Is(err, ErrRequestDigestMismatch) {
		t.Fatalf("digest error = %v, want ErrRequestDigestMismatch", err)
	}

	chOld, err := gw.Submit(requestIdentity(0x0101, 0x00AA, 7, 1, 0x05, 1, 1, 10, "generation-7"))
	if err != nil {
		t.Fatalf("Submit old generation: %v", err)
	}
	chNew, err := gw.Submit(requestIdentity(0x0101, 0x00AA, 8, 1, 0x05, 1, 1, 10, "generation-8"))
	if err != nil {
		t.Fatalf("Submit new generation: %v", err)
	}

	// A response computed for generation 7 cannot be used on generation 8 even
	// if every other field is identical.
	resp := responseFor(chOld, chNew.Identity, secretForMaster(chOld.Identity.MasterAddress))
	resp.ChallengeID = chNew.ChallengeID
	if _, err := gw.Respond(resp); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("generation transplant error = %v, want ErrAuthenticationFailed", err)
	}
}

func TestConcurrentRetransmissionExecutesOnce(t *testing.T) {
	clock := newTestClock()
	rec := &recorder{payload: []byte("once")}
	gw := newGateway(t, clock, rec.Execute)

	ch, err := gw.Submit(requestIdentity(0x0101, 0x00AA, 7, 1, 0x05, 1, 1, 10, "concurrent"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	resp := responseFor(ch, ch.Identity, secretForMaster(ch.Identity.MasterAddress))

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := gw.Respond(resp); err != nil {
				t.Errorf("Respond: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if rec.Count() != 1 {
		t.Fatalf("executions = %d, want 1", rec.Count())
	}
}

func TestPendingBecomesAuthorizedBeforeExecution(t *testing.T) {
	clock := newTestClock()
	started := make(chan struct{})
	release := make(chan struct{})
	gw := newGateway(t, clock, func(Execution) ([]byte, error) {
		close(started)
		<-release
		return []byte("authorized"), nil
	})

	ch, err := gw.Submit(requestIdentity(0x0101, 0x00AA, 7, 1, 0x05, 1, 1, 10, "transition"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	resp := responseFor(ch, ch.Identity, secretForMaster(ch.Identity.MasterAddress))
	done := make(chan error, 1)
	go func() {
		_, err := gw.Respond(resp)
		done <- err
	}()

	<-started
	if status, ok := gw.Status(ch.ChallengeID); !ok || status.State != StateAuthorized {
		t.Fatalf("state during callback = %+v, want authorized", status)
	}
	if gw.PendingCount() != 0 {
		t.Fatalf("challenge is still pending during execution")
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if status, _ := gw.Status(ch.ChallengeID); status.State != StateCompleted {
		t.Fatalf("final state = %q, want completed", status.State)
	}
}

func TestReceiptKeepsFrozenIdentityWhenCallbackMutatesView(t *testing.T) {
	clock := newTestClock()
	gw := newGateway(t, clock, func(exec Execution) ([]byte, error) {
		// A callback receives a private copy.  Mutating the operation value must
		// not change the identity recorded in the durable receipt.
		for i := range exec.Identity.OperationValue {
			exec.Identity.OperationValue[i] = 'X'
		}
		return []byte("done"), nil
	})

	id := requestIdentity(0x0101, 0x00AA, 7, 1, 0x05, 1, 1, 10, "immutable")
	ch, err := gw.Submit(id)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	receipt, err := gw.Respond(responseFor(ch, ch.Identity, secretForMaster(ch.Identity.MasterAddress)))
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if got := string(receipt.Identity.OperationValue); got != "immutable" {
		t.Fatalf("receipt identity = %q, want immutable", got)
	}
	if !bytes.Equal(MarshalIdentity(receipt.Identity), MarshalIdentity(ch.Identity)) {
		t.Fatalf("receipt identity differs from challenged identity")
	}
}

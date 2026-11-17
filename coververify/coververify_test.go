package coververify

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// fakeModel is a deterministic, model-scoped transition function used as the
// host replay callback. Its output is a pure function of the evidence.
type fakeModel struct {
	prefix string
	fail   bool
	panic_ bool
}

func (m fakeModel) transition(prev, input Digest) ([]byte, error) {
	if m.panic_ {
		panic("boom")
	}
	if m.fail {
		return nil, errors.New("model unavailable")
	}
	out := make([]byte, 0, len(prev)+len(input)+len(m.prefix))
	out = append(out, m.prefix...)
	out = append(out, prev[:]...)
	out = append(out, input[:]...)
	return out, nil
}

const (
	modelA = "model-a@1"
	modelB = "model-b@1"
	chain1 = "chain-1"
	chain2 = "chain-2"
)

type harness struct {
	t      *testing.T
	v      *Verifier
	inputs []Digest
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, v: NewVerifier()}
	h.v.RegisterModel(modelA, fakeModel{prefix: "A:"}.transition)
	h.v.RegisterModel(modelB, fakeModel{prefix: "B:"}.transition)

	anchor := SumDigest([]byte("genesis"))
	if err := h.v.AnchorChain(chain1, modelA, anchor); err != nil {
		t.Fatalf("anchor chain1: %v", err)
	}
	if err := h.v.AnchorChain(chain2, modelA, anchor); err != nil {
		t.Fatalf("anchor chain2: %v", err)
	}
	for i := 0; i < 8; i++ {
		h.inputs = append(h.inputs, SumDigest([]byte(fmt.Sprintf("input-%d", i))))
	}
	return h
}

// receipt builds a genuine replay-consistent receipt continuing from prev.
func (h *harness) receipt(chain, model string, prev, input Digest, id int) Receipt {
	cb := h.v.models[model]
	out, err := cb(prev, input)
	if err != nil {
		h.t.Fatalf("test setup callback: %v", err)
	}
	return h.receiptWithOutput(chain, model, prev, input, out, id)
}

// receiptWithOutput builds a receipt with an explicit output, used for chains
// whose callback is intentionally broken and cannot be pre-invoked.
func (h *harness) receiptWithOutput(chain, model string, prev, input Digest, output []byte, id int) Receipt {
	return Receipt{
		ChainID:         chain,
		ModelVersion:    model,
		PrevStateDigest: prev,
		InputDigest:     input,
		Output:          output,
		ReceiptID:       fmt.Sprintf("r-%d", id),
	}
}

func (h *harness) frontier(chain string) Digest {
	f, ok := h.v.Frontier(chain)
	if !ok {
		h.t.Fatalf("unknown chain %q", chain)
	}
	return f
}

func (h *harness) mustReplay(model string, prev, input Digest) []byte {
	out, err := h.v.models[model](prev, input)
	if err != nil {
		h.t.Fatalf("mustReplay: %v", err)
	}
	return out
}

func (h *harness) expectBalanced() {
	h.t.Helper()
	s := h.v.Stats()
	sum := s.Accepted + s.Pending + s.Rejected + s.Conflict + s.Duplicate + s.CallbackError
	if sum != s.Total {
		h.t.Fatalf("accounts unbalanced: sum %d != total %d (%+v)", sum, s.Total, s)
	}
	if s.Accepted != h.v.Covered() {
		h.t.Fatalf("accepted %d != Covered %d", s.Accepted, h.v.Covered())
	}
	if s.Pending != h.v.Pending() {
		h.t.Fatalf("pending %d != Pending %d", s.Pending, h.v.Pending())
	}
}

func TestHappyPathAndForgedEvidenceRejected(t *testing.T) {
	h := newHarness(t)
	anchor := h.v.chains[chain1].anchor

	first := h.receipt(chain1, modelA, anchor, h.inputs[0], 0)
	if res := h.v.Submit(first); res.Status != StatusAccepted {
		t.Fatalf("first: got %s, want accepted", res.Status)
	}
	second := h.receipt(chain1, modelA, SumDigest(first.Output), h.inputs[1], 1)
	if res := h.v.Submit(second); res.Status != StatusAccepted {
		t.Fatalf("second: got %s, want accepted", res.Status)
	}
	if h.v.Covered() != 2 {
		t.Fatalf("covered = %d, want 2", h.v.Covered())
	}

	frontier := h.frontier(chain1)

	// Forge the output: the replayed digest will not match.
	forge := h.receipt(chain1, modelA, frontier, h.inputs[2], 2)
	forge.Output = append(forge.Output, []byte("tampered")...)
	if res := h.v.Submit(forge); res.Status != StatusRejected {
		t.Fatalf("forged output: got %s, want rejected", res.Status)
	}

	// Forge the previous-state summary independently.
	badPrev := h.receipt(chain1, modelA, frontier, h.inputs[3], 3)
	badPrev.PrevStateDigest = SumDigest([]byte("made-up-state"))
	if res := h.v.Submit(badPrev); res.Status != StatusRejected {
		t.Fatalf("forged prev digest: got %s, want rejected", res.Status)
	}

	// Forge the actual-input summary independently.
	badInput := h.receipt(chain1, modelA, frontier, h.inputs[4], 4)
	badInput.InputDigest = SumDigest([]byte("made-up-input"))
	if res := h.v.Submit(badInput); res.Status != StatusRejected {
		t.Fatalf("forged input digest: got %s, want rejected", res.Status)
	}

	// Missing mandatory evidence and unknown chain references.
	empty := Receipt{ChainID: chain1, ModelVersion: modelA, ReceiptID: "r-empty"}
	if res := h.v.Submit(empty); res.Status != StatusRejected {
		t.Fatalf("empty receipt: got %s, want rejected", res.Status)
	}
	ghost := h.receipt("ghost", modelA, anchor, h.inputs[1], 9)
	if res := h.v.Submit(ghost); res.Status != StatusRejected {
		t.Fatalf("unknown chain: got %s, want rejected", res.Status)
	}

	// None of the forgeries may advance coverage or the frontier.
	if h.v.Covered() != 2 {
		t.Fatalf("covered after forgeries = %d, want 2", h.v.Covered())
	}
	if got := h.frontier(chain1); got != frontier {
		t.Fatalf("frontier moved after rejected receipts: %x", got)
	}
	h.expectBalanced()
}

func TestBrokenLinkRetainedThenResolved(t *testing.T) {
	h := newHarness(t)
	anchor := h.v.chains[chain1].anchor

	first := h.receipt(chain1, modelA, anchor, h.inputs[0], 0)
	second := h.receipt(chain1, modelA, SumDigest(first.Output), h.inputs[1], 1)

	// Submit the second transition before the first: the link is broken.
	if res := h.v.Submit(second); res.Status != StatusPending {
		t.Fatalf("out-of-order: got %s, want pending", res.Status)
	}
	if h.v.Covered() != 0 || h.v.Pending() != 1 {
		t.Fatalf("after gap: covered %d pending %d, want 0/1", h.v.Covered(), h.v.Pending())
	}

	// Closing the gap accepts both without double counting.
	if res := h.v.Submit(first); res.Status != StatusAccepted {
		t.Fatalf("first: got %s, want accepted", res.Status)
	}
	if h.v.Covered() != 2 || h.v.Pending() != 0 {
		t.Fatalf("resolved: covered %d pending %d, want 2/0", h.v.Covered(), h.v.Pending())
	}
	if got := h.frontier(chain1); got != SumDigest(second.Output) {
		t.Fatalf("frontier did not reach second transition output")
	}
	h.expectBalanced()
}

func TestOmittedTransitionNeverCovered(t *testing.T) {
	h := newHarness(t)
	anchor := h.v.chains[chain1].anchor

	first := h.receipt(chain1, modelA, anchor, h.inputs[0], 0)
	second := h.receipt(chain1, modelA, SumDigest(first.Output), h.inputs[1], 1)
	third := h.receipt(chain1, modelA, SumDigest(second.Output), h.inputs[2], 2)

	// Report first and third but deliberately omit the middle transition.
	if res := h.v.Submit(first); res.Status != StatusAccepted {
		t.Fatalf("first: got %s, want accepted", res.Status)
	}
	if res := h.v.Submit(third); res.Status != StatusPending {
		t.Fatalf("skipped transition: got %s, want pending", res.Status)
	}
	if h.v.Covered() != 1 || h.v.Pending() != 1 {
		t.Fatalf("omission: covered %d pending %d, want 1/1", h.v.Covered(), h.v.Pending())
	}

	// Resending the same skipped receipt does not manufacture coverage.
	if res := h.v.Submit(third); res.Status != StatusPending {
		t.Fatalf("resent skipped: got %s, want pending", res.Status)
	}
	if h.v.Covered() != 1 || h.v.Pending() != 1 {
		t.Fatalf("after resend: covered %d pending %d, want 1/1", h.v.Covered(), h.v.Pending())
	}

	// Supplying the previously omitted transition links the retained tail.
	if res := h.v.Submit(second); res.Status != StatusAccepted {
		t.Fatalf("fill gap: got %s, want accepted", res.Status)
	}
	if h.v.Covered() != 3 || h.v.Pending() != 0 {
		t.Fatalf("filled: covered %d pending %d, want 3/0", h.v.Covered(), h.v.Pending())
	}
	h.expectBalanced()
}

func TestDuplicateReceiptsDoNotInflateCoverage(t *testing.T) {
	h := newHarness(t)
	anchor := h.v.chains[chain1].anchor
	r := h.receipt(chain1, modelA, anchor, h.inputs[0], 0)
	if res := h.v.Submit(r); res.Status != StatusAccepted {
		t.Fatalf("original: got %s, want accepted", res.Status)
	}
	for i := 0; i < 3; i++ {
		dup := r
		dup.ReceiptID = fmt.Sprintf("dup-%d", i)
		if res := h.v.Submit(dup); res.Status != StatusDuplicate {
			t.Fatalf("duplicate %d: got %s (%s), want duplicate", i, res.Status, res.Reason)
		}
	}
	if h.v.Covered() != 1 {
		t.Fatalf("covered = %d, want 1", h.v.Covered())
	}
	h.expectBalanced()
}

func TestCrossModelSplicingIsolated(t *testing.T) {
	h := newHarness(t)
	anchor := h.v.chains[chain1].anchor
	first := h.receipt(chain1, modelA, anchor, h.inputs[0], 0)
	if res := h.v.Submit(first); res.Status != StatusAccepted {
		t.Fatalf("first: got %s, want accepted", res.Status)
	}

	// A genuinely valid model-B receipt spliced into the model-A-bound chain.
	frontier := h.frontier(chain1)
	splice := h.receipt(chain1, modelB, frontier, h.inputs[1], 1)
	res := h.v.Submit(splice)
	if res.Status != StatusConflict {
		t.Fatalf("cross-model splice: got %s, want conflict", res.Status)
	}
	if !h.v.IsConflict(chain1, splice.PrevStateDigest, splice.InputDigest) {
		t.Fatalf("splice key not recorded as conflict")
	}
	if model, _ := h.v.ChainModel(chain1); model != modelA {
		t.Fatalf("chain model binding changed to %q", model)
	}
	if h.v.Covered() != 1 {
		t.Fatalf("covered after splice = %d, want 1", h.v.Covered())
	}

	// The isolated key stays quarantined even when resubmitted correctly.
	again := h.receipt(chain1, modelA, frontier, h.inputs[1], 1)
	if res := h.v.Submit(again); res.Status != StatusConflict {
		t.Fatalf("resubmitted isolated key: got %s, want conflict", res.Status)
	}

	// An independent chain bound to model B is unaffected by the isolation.
	if err := h.v.AnchorChain("chain-b", modelB, anchor); err != nil {
		t.Fatalf("anchor chain-b: %v", err)
	}
	bReceipt := h.receipt("chain-b", modelB, anchor, h.inputs[0], 10)
	if res := h.v.Submit(bReceipt); res.Status != StatusAccepted {
		t.Fatalf("independent model-B chain: got %s, want accepted", res.Status)
	}
	h.expectBalanced()
}

func TestCallbackFailureAndPanicNotCovered(t *testing.T) {
	h := newHarness(t)
	anchor := h.v.chains[chain1].anchor

	h.v.RegisterModel("fail-model", fakeModel{prefix: "F:", fail: true}.transition)
	h.v.RegisterModel("panic-model", fakeModel{prefix: "P:", panic_: true}.transition)
	if err := h.v.AnchorChain("fail-chain", "fail-model", anchor); err != nil {
		t.Fatalf("anchor fail-chain: %v", err)
	}
	if err := h.v.AnchorChain("panic-chain", "panic-model", anchor); err != nil {
		t.Fatalf("anchor panic-chain: %v", err)
	}

	failing := h.receiptWithOutput("fail-chain", "fail-model", anchor, h.inputs[0],
		[]byte("claimed-output"), 20)
	if res := h.v.Submit(failing); res.Status != StatusCallbackError {
		t.Fatalf("erroring callback: got %s, want callback_error", res.Status)
	}
	panicking := h.receiptWithOutput("panic-chain", "panic-model", anchor, h.inputs[0],
		[]byte("claimed-output"), 21)
	if res := h.v.Submit(panicking); res.Status != StatusCallbackError {
		t.Fatalf("panicking callback: got %s, want callback_error", res.Status)
	}
	if h.v.Covered() != 0 {
		t.Fatalf("covered after callback failures = %d, want 0", h.v.Covered())
	}

	// Recovering the model later lets the same evidence be replayed and
	// counted, proving the failure was not silently accepted.
	h.v.RegisterModel("fail-model", fakeModel{prefix: "F:"}.transition)
	failing.Output = h.mustReplay("fail-model", anchor, h.inputs[0])
	if res := h.v.Submit(failing); res.Status != StatusAccepted {
		t.Fatalf("recovered callback: got %s, want accepted", res.Status)
	}
	if h.v.Covered() != 1 {
		t.Fatalf("covered after recovery = %d, want 1", h.v.Covered())
	}
	h.expectBalanced()
}

func TestKeyConflictIsolatedAndQuarantined(t *testing.T) {
	h := newHarness(t)
	anchor := h.v.chains[chain1].anchor
	first := h.receipt(chain1, modelA, anchor, h.inputs[0], 0)
	if res := h.v.Submit(first); res.Status != StatusAccepted {
		t.Fatalf("first: got %s, want accepted", res.Status)
	}
	second := h.receipt(chain1, modelA, SumDigest(first.Output), h.inputs[1], 1)
	if res := h.v.Submit(second); res.Status != StatusAccepted {
		t.Fatalf("second: got %s, want accepted", res.Status)
	}

	// Replace the model callback so the same key now yields a different
	// canonical output (simulated model drift), then replay the first key.
	keyPrev := anchor
	keyInput := h.inputs[0]
	h.v.RegisterModel(modelA, fakeModel{prefix: "A-DRIFT:"}.transition)
	drift := h.receipt(chain1, modelA, keyPrev, keyInput, 2)
	res := h.v.Submit(drift)
	if res.Status != StatusConflict {
		t.Fatalf("contradictory replay: got %s, want conflict", res.Status)
	}
	if !h.v.IsConflict(chain1, keyPrev, keyInput) {
		t.Fatalf("contradictory key not isolated")
	}

	// The accepted frontier and coverage are untouched by the conflict.
	if h.v.Covered() != 2 {
		t.Fatalf("covered after conflict = %d, want 2", h.v.Covered())
	}
	if got := h.frontier(chain1); got != SumDigest(second.Output) {
		t.Fatalf("frontier changed after conflict: %x", got)
	}

	// A new, unrelated transition on the same chain still verifies.
	third := h.receipt(chain1, modelA, SumDigest(second.Output), h.inputs[2], 3)
	if res := h.v.Submit(third); res.Status != StatusAccepted {
		t.Fatalf("unrelated transition after conflict: got %s, want accepted", res.Status)
	}
	h.expectBalanced()
}

func TestAnchorValidation(t *testing.T) {
	v := NewVerifier()
	anchor := SumDigest([]byte("genesis"))
	if err := v.AnchorChain("c", "unregistered", anchor); !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("unregistered model: got %v, want ErrUnknownModel", err)
	}
	v.RegisterModel(modelA, fakeModel{prefix: "A:"}.transition)
	if err := v.AnchorChain("c", modelA, Digest{}); err == nil {
		t.Fatalf("zero anchor: expected error")
	}
	if err := v.AnchorChain("", modelA, anchor); err == nil {
		t.Fatalf("empty chain id: expected error")
	}
	if err := v.AnchorChain("c", modelA, anchor); err != nil {
		t.Fatalf("valid anchor: %v", err)
	}
	if err := v.AnchorChain("c", modelA, anchor); err == nil {
		t.Fatalf("duplicate anchor: expected error")
	}
}

func TestConcurrentSubmissionAccounting(t *testing.T) {
	h := newHarness(t)
	const workers = 8
	const perWorker = 25
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// All workers replay the same chain; only the first submission of
			// each key can be accepted, the rest must be duplicates or pending.
			r := h.receipt(chain2, modelA, h.v.chains[chain2].anchor, h.inputs[0], 0)
			for i := 0; i < perWorker; i++ {
				dup := r
				dup.ReceiptID = fmt.Sprintf("c-%p-%d", &wg, i)
				h.v.Submit(dup)
			}
		}()
	}
	wg.Wait()

	s := h.v.Stats()
	if s.Accepted != 1 {
		t.Fatalf("concurrent accepted = %d, want 1", s.Accepted)
	}
	if s.Total != workers*perWorker {
		t.Fatalf("concurrent total = %d, want %d", s.Total, workers*perWorker)
	}
	h.expectBalanced()
}

func TestUnknownChainAccessors(t *testing.T) {
	v := NewVerifier()
	anchor := SumDigest([]byte("genesis"))
	if _, ok := v.Frontier("nope"); ok {
		t.Fatalf("frontier of unknown chain should report !ok")
	}
	if model, ok := v.ChainModel("nope"); ok || model != "" {
		t.Fatalf("model of unknown chain should report !ok")
	}
	if v.IsConflict("nope", Digest{1}, Digest{2}) {
		t.Fatalf("conflict lookup on unknown chain should be false")
	}
	if got := (Status(99)).String(); got != "unknown" {
		t.Fatalf("status string = %q, want unknown", got)
	}
	zero := Digest{}
	if got := zero.String(); got == "" || !zero.IsZero() {
		t.Fatalf("zero digest helpers misbehave")
	}
	if got := SumDigest([]byte("x")); got.IsZero() {
		t.Fatalf("non-trivial digest must not be zero")
	}

	// RegisterModel ignores unusable arguments without mutating state.
	v.RegisterModel("", fakeModel{}.transition)
	v.RegisterModel("nil-model", nil)
	if err := v.AnchorChain("c", "nil-model", anchor); !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("ignored registration: got %v, want ErrUnknownModel", err)
	}

	allStatuses := []Status{
		StatusPending, StatusAccepted, StatusRejected,
		StatusConflict, StatusDuplicate, StatusCallbackError,
	}
	seen := map[string]bool{}
	for _, s := range allStatuses {
		name := s.String()
		if name == "" || seen[name] {
			t.Fatalf("status %d has invalid or duplicate name %q", s, name)
		}
		seen[name] = true
	}
}

func TestHeldReceiptContradictionIsolated(t *testing.T) {
	h := newHarness(t)
	anchor := h.v.chains[chain1].anchor

	first := h.receipt(chain1, modelA, anchor, h.inputs[0], 0)

	// Accept the first transition, then replace the model so the same
	// transition key deterministically replays to a different output.
	if res := h.v.Submit(first); res.Status != StatusAccepted {
		t.Fatalf("first: got %s, want accepted", res.Status)
	}
	h.v.RegisterModel(modelA, fakeModel{prefix: "A-DRIFT:"}.transition)
	drifted := h.receipt(chain1, modelA, anchor, h.inputs[0], 11)
	if res := h.v.Submit(drifted); res.Status != StatusConflict {
		t.Fatalf("drifted replay of accepted key: got %s, want conflict", res.Status)
	}

	// The quarantined key stays isolated and the original pending/accepted
	// accounting is untouched.
	if !h.v.IsConflict(chain1, anchor, h.inputs[0]) {
		t.Fatalf("contradictory key not isolated")
	}
	if h.v.Pending() != 0 || h.v.Covered() != 1 {
		t.Fatalf("covered %d pending %d, want 1/0", h.v.Covered(), h.v.Pending())
	}
	h.expectBalanced()
}

func TestMultipleGapsResolvedDeterministically(t *testing.T) {
	h := newHarness(t)
	anchor := h.v.chains[chain2].anchor

	// Build a three-transition chain and submit the tail two out of order so
	// two receipts are retained pending simultaneously.
	r0 := h.receipt(chain2, modelA, anchor, h.inputs[0], 30)
	r1 := h.receipt(chain2, modelA, SumDigest(r0.Output), h.inputs[1], 31)
	r2 := h.receipt(chain2, modelA, SumDigest(r1.Output), h.inputs[2], 32)

	res := h.v.Submit(r2)
	if res.Status != StatusPending {
		t.Fatalf("tail: got %s, want pending", res.Status)
	}
	res = h.v.Submit(r1)
	if res.Status != StatusPending {
		t.Fatalf("middle: got %s, want pending", res.Status)
	}
	if h.v.Pending() != 2 || h.v.Covered() != 0 {
		t.Fatalf("gaps: pending %d covered %d, want 2/0", h.v.Pending(), h.v.Covered())
	}

	// Submitting the head drains both retained receipts in one cascade.
	head := h.v.Submit(r0)
	if head.Status != StatusAccepted {
		t.Fatalf("head: got %s, want accepted", head.Status)
	}
	if h.v.Covered() != 3 || h.v.Pending() != 0 {
		t.Fatalf("drained: covered %d pending %d, want 3/0", h.v.Covered(), h.v.Pending())
	}
	if got := h.frontier(chain2); got != SumDigest(r2.Output) {
		t.Fatalf("frontier did not reach tail output")
	}

	// Exercise the human-readable verdict rendering.
	if head.String() == "" {
		t.Fatalf("empty result rendering")
	}
	h.expectBalanced()
}

func TestPendingRetentionWhenCallbackLaterFails(t *testing.T) {
	h := newHarness(t)
	anchor := h.v.chains[chain2].anchor

	r0 := h.receipt(chain2, modelA, anchor, h.inputs[0], 40)
	r1 := h.receipt(chain2, modelA, SumDigest(r0.Output), h.inputs[1], 41)

	// Hold r1 pending behind the unsubmitted r0.
	if res := h.v.Submit(r1); res.Status != StatusPending {
		t.Fatalf("r1: got %s, want pending", res.Status)
	}

	// Close the head gap with a healthy callback: r0 links and the cascade
	// tries to promote retained r1, so both are initially accepted.
	if res := h.v.Submit(r0); res.Status != StatusAccepted {
		t.Fatalf("r0: got %s, want accepted", res.Status)
	}
	if h.v.Covered() != 2 || h.v.Pending() != 0 {
		t.Fatalf("healthy cascade: covered %d pending %d, want 2/0", h.v.Covered(), h.v.Pending())
	}

	// Now hold another receipt pending, then break the model. When a later
	// healthy-style submission reaches the cascade, the held receipt's replay
	// must fail and keep it retained rather than counting it.
	r2 := h.receipt(chain2, modelA, SumDigest(r1.Output), h.inputs[2], 42)
	r3 := h.receipt(chain2, modelA, SumDigest(r2.Output), h.inputs[3], 43)
	if res := h.v.Submit(r3); res.Status != StatusPending {
		t.Fatalf("r3: got %s, want pending", res.Status)
	}
	h.v.RegisterModel(modelA, fakeModel{prefix: "A:", fail: true}.transition)

	// r2 itself cannot replay under the broken model and is rejected, but the
	// already-retained r3 must never be promoted while replay is impossible.
	if res := h.v.Submit(r2); res.Status != StatusCallbackError {
		t.Fatalf("r2 under broken model: got %s, want callback_error", res.Status)
	}
	if h.v.Covered() != 2 || h.v.Pending() != 1 {
		t.Fatalf("broken replay: covered %d pending %d, want 2/1", h.v.Covered(), h.v.Pending())
	}

	// Restore the model and supply r2 again; the retained r3 then links too.
	h.v.RegisterModel(modelA, fakeModel{prefix: "A:"}.transition)
	if res := h.v.Submit(r2); res.Status != StatusAccepted {
		t.Fatalf("r2 restored: got %s, want accepted", res.Status)
	}
	if h.v.Covered() != 4 || h.v.Pending() != 0 {
		t.Fatalf("after restore: covered %d pending %d, want 4/0", h.v.Covered(), h.v.Pending())
	}
	h.expectBalanced()
}

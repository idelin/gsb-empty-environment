package takeover

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"
)

var testClock = func() time.Time {
	return time.Unix(1_700_000_000, 0)
}

func testProcess(boot string, pid uint32, start uint64) ProcessStartID {
	return NewProcessStartID(boot, pid, start)
}

func newTreeHost(t *testing.T, root Grant) *Host {
	t.Helper()
	host, err := NewHost(root, WithSharedHMACKey("test-key", []byte("test-secret")), WithNow(testClock))
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	return host
}

func mustDelegate(t *testing.T, host *Host, parent GrantID, request DelegateRequest) Grant {
	t.Helper()
	grant, err := host.Delegate(parent, request)
	if err != nil {
		t.Fatalf("Delegate(%s -> %s): %v", parent, request.GrantID, err)
	}
	return grant
}

func signedReturn(t *testing.T, host *Host, id GrantID, scope Scope, final bool) ReturnReceipt {
	t.Helper()
	grant, ok := host.Lookup(id)
	if !ok {
		t.Fatalf("missing grant %s", id)
	}
	receipt := ReturnReceipt{
		GrantID:        id,
		ProcessStartID: grant.ProcessStartID,
		SessionRoot:    grant.SessionRoot,
		Epoch:          grant.Epoch,
		Returned:       scope,
		Final:          final,
		IssuedAt:       testClock(),
		Nonce:          "receipt-" + string(id),
	}
	if err := receipt.Sign(host.Signer()); err != nil {
		t.Fatalf("sign receipt: %v", err)
	}
	return receipt
}

func TestSeqSetRangeAlgebraAndLargeDomain(t *testing.T) {
	set := Set(Range(1, 3), Range(4, 7), Range(10, 12))
	if want := []SeqRange{{1, 7}, {10, 12}}; !reflect.DeepEqual(set.Ranges(), want) {
		t.Fatalf("normalized ranges = %v, want %v", set.Ranges(), want)
	}
	if got := set.Subtract(Set(Range(2, 5))); !got.Equal(Set(Range(1, 1), Range(6, 7), Range(10, 12))) {
		t.Fatalf("subtract = %v", got.Ranges())
	}
	if got := set.Intersect(Set(Range(5, 11))); !got.Equal(Set(Range(5, 7), Range(10, 11))) {
		t.Fatalf("intersect = %v", got.Ranges())
	}

	large := Set(Range(0, math.MaxUint64))
	if ranges := large.Ranges(); len(ranges) != 1 || ranges[0] != (SeqRange{0, math.MaxUint64}) {
		t.Fatalf("large set expanded: %v", ranges)
	}
	if _, exact := large.CountExact(); exact {
		t.Fatal("full uint64 cardinality unexpectedly fit in uint64")
	}
	hole := large.Subtract(Set(Range(5, 5)))
	wantHole := []SeqRange{{0, 4}, {6, math.MaxUint64}}
	if !reflect.DeepEqual(hole.Ranges(), wantHole) {
		t.Fatalf("large subtraction = %v, want %v", hole.Ranges(), wantHole)
	}
	if hole.Count() != math.MaxUint64 {
		t.Fatalf("saturated count = %d", hole.Count())
	}
}

func TestSeqSetAgainstPerSequenceModel(t *testing.T) {
	const limit = 40
	a := Set(Range(1, 5), Range(6, 10), Range(15, 15), Range(30, 39))
	b := Set(Range(4, 8), Range(15, 20), Range(31, 31))
	model := func(set SeqSet) [limit]bool {
		var out [limit]bool
		for seq := uint64(0); seq < limit; seq++ {
			out[seq] = set.Contains(seq)
		}
		return out
	}
	ma, mb := model(a), model(b)
	union, intersect, subtract := model(a.Union(b)), model(a.Intersect(b)), model(a.Subtract(b))
	for seq := uint64(0); seq < limit; seq++ {
		if union[seq] != (ma[seq] || mb[seq]) {
			t.Fatalf("union mismatch at %d", seq)
		}
		if intersect[seq] != (ma[seq] && mb[seq]) {
			t.Fatalf("intersection mismatch at %d", seq)
		}
		if subtract[seq] != (ma[seq] && !mb[seq]) {
			t.Fatalf("subtraction mismatch at %d", seq)
		}
	}

	universe := SeqRangeSet(0, limit-1)
	observations := []Observation{
		{Scope: Scope{Sequences: Set(Range(1, 9), Range(12, 14))}, Class: EvidenceControlled},
		{Scope: Scope{Sequences: Set(Range(8, 12))}, Class: EvidenceInvalid, Epoch: 1},
		{Scope: Scope{Sequences: Set(Range(20, 25))}, Class: EvidenceControlled, Epoch: 2},
		{Scope: Scope{Sequences: Set(Range(20, 25))}, Class: EvidenceInvalid, Epoch: 2},
	}
	partition, err := Reconcile(universe, observations...)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	for seq := uint64(0); seq < limit; seq++ {
		class, ok := partition.Classify(seq)
		if !ok {
			t.Fatalf("seq %d not in partition", seq)
		}
		var want EvidenceClass
		switch {
		case seq >= 20 && seq <= 25:
			want = EvidencePending
		case seq >= 8 && seq <= 12:
			want = EvidenceInvalid
		case (seq >= 1 && seq <= 7) || (seq >= 13 && seq <= 14):
			want = EvidenceControlled
		default:
			want = EvidencePending
		}
		if class != want {
			t.Fatalf("seq %d class = %s, want %s", seq, class, want)
		}
	}
}

func TestSameDisplayDifferentWindowsCanBeLentInParallel(t *testing.T) {
	left := WindowDimension("display-1", "left")
	right := WindowDimension("display-1", "right")
	root := Grant{
		ID:             "root",
		ProcessStartID: testProcess("boot", 10, 1),
		SessionRoot:    "session",
		Signals:        Signals(SignalInterrupt, SignalTerminate),
		Dimensions:     Dimensions(left, right),
		Sequences:      SeqRangeSet(1, 200),
	}
	host := newTreeHost(t, root)

	mustDelegate(t, host, "root", DelegateRequest{
		GrantID:        "left-child",
		ProcessStartID: testProcess("boot", 11, 1),
		SessionRoot:    "session",
		Signals:        Signals(SignalInterrupt),
		Dimensions:     Dimensions(left),
		Sequences:      SeqRangeSet(1, 50),
	})
	mustDelegate(t, host, "root", DelegateRequest{
		GrantID:        "right-child",
		ProcessStartID: testProcess("boot", 12, 1),
		SessionRoot:    "session",
		Signals:        Signals(SignalInterrupt),
		Dimensions:     Dimensions(right),
		Sequences:      SeqRangeSet(51, 100),
	})

	_, err := host.Delegate("root", DelegateRequest{
		GrantID:        "right-again",
		ProcessStartID: testProcess("boot", 13, 1),
		SessionRoot:    "session",
		Signals:        Signals(SignalInterrupt),
		Dimensions:     Dimensions(right),
		Sequences:      SeqRangeSet(101, 150),
	})
	var conflict *DimensionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("parallel overlap error = %v, want DimensionConflictError", err)
	}
}

func TestLocalSubtreeHostCanImportDelegatedGrant(t *testing.T) {
	dimension := WindowDimension("display", "window")
	root := Grant{
		ID:             "root",
		ProcessStartID: testProcess("boot", 20, 20),
		SessionRoot:    "session",
		Signals:        Signals(SignalInterrupt),
		Dimensions:     Dimensions(dimension),
		Sequences:      SeqRangeSet(1, 100),
	}
	parent := newTreeHost(t, root)
	childGrant := mustDelegate(t, parent, "root", DelegateRequest{
		GrantID:        "child",
		ProcessStartID: testProcess("boot", 21, 21),
		SessionRoot:    "session",
		Signals:        Signals(SignalInterrupt),
		Dimensions:     Dimensions(dimension),
		Sequences:      SeqRangeSet(1, 30),
	})
	child, err := NewHost(childGrant,
		WithSharedHMACKey("test-key", []byte("test-secret")),
		WithNow(testClock))
	if err != nil {
		t.Fatalf("import delegated grant: %v", err)
	}
	mustDelegate(t, child, "child", DelegateRequest{
		GrantID:        "grandchild",
		ProcessStartID: testProcess("boot", 22, 22),
		SessionRoot:    "session",
		Signals:        Signals(SignalInterrupt),
		Dimensions:     Dimensions(dimension),
		Sequences:      SeqRangeSet(1, 10),
	})
	if _, err := child.Return(ReturnRequest{GrantID: "child"}); !errors.Is(err, ErrUnsettled) {
		t.Fatalf("local root return without grandchild evidence = %v", err)
	}
	evidence := signedReturn(t, child, "grandchild", Scope{
		Dimensions: Dimensions(dimension),
		Sequences:  SeqRangeSet(1, 10),
	}, true)
	result, err := child.Return(ReturnRequest{
		GrantID:  "child",
		Receipts: []ReturnReceipt{evidence},
	})
	if err != nil {
		t.Fatalf("local root return: %v", err)
	}
	if !result.Closed {
		t.Fatal("local root should be closed")
	}
	if err := result.Receipt.Verify(parent.Verifier()); err != nil {
		t.Fatalf("parent cannot verify imported subtree receipt: %v", err)
	}
}

func TestCrossLayerPartialReturnAndDirectChildReceipt(t *testing.T) {
	left := WindowDimension("display-1", "left")
	right := WindowDimension("display-1", "right")
	rootStart := testProcess("boot", 100, 1000)
	root := Grant{
		ID:             "root",
		ProcessStartID: rootStart,
		SessionRoot:    "session",
		Signals:        Signals(SignalInterrupt, SignalTerminate),
		Dimensions:     Dimensions(left, right),
		Sequences:      SeqRangeSet(1, 200),
	}
	host := newTreeHost(t, root)

	childA := mustDelegate(t, host, "root", DelegateRequest{
		GrantID:        "a",
		ProcessStartID: testProcess("boot", 101, 1001),
		SessionRoot:    "session",
		Signals:        Signals(SignalInterrupt),
		Dimensions:     Dimensions(left),
		Sequences:      SeqRangeSet(1, 50),
	})
	mustDelegate(t, host, "root", DelegateRequest{
		GrantID:        "b",
		ProcessStartID: testProcess("boot", 102, 1002),
		SessionRoot:    "session",
		Signals:        Signals(SignalInterrupt),
		Dimensions:     Dimensions(right),
		Sequences:      SeqRangeSet(51, 100),
	})
	childG := mustDelegate(t, host, "a", DelegateRequest{
		GrantID:        "g",
		ProcessStartID: testProcess("boot", 103, 1003),
		SessionRoot:    "session",
		Signals:        Signals(SignalInterrupt),
		Dimensions:     Dimensions(left),
		Sequences:      SeqRangeSet(1, 20),
	})

	directOnly := signedReturn(t, host, "a", Scope{
		Dimensions: Dimensions(left),
		Sequences:  SeqRangeSet(1, 20),
	}, false)
	tampered := directOnly
	tampered.Signature = append([]byte(nil), directOnly.Signature...)
	tampered.Signature[0] ^= 0xff
	_, err := host.Return(ReturnRequest{
		GrantID:    "root",
		Partial:    true,
		Dimensions: Dimensions(left),
		Sequences:  SeqRangeSet(1, 20),
		Receipts:   []ReturnReceipt{tampered},
	})
	if !errors.Is(err, ErrReceiptSignature) {
		t.Fatalf("tampered receipt = %v, want signature failure", err)
	}

	staleFence := directOnly
	staleFence.ProcessStartID.Start++
	staleFence.Signature = nil
	if err := staleFence.Sign(host.Signer()); err != nil {
		t.Fatalf("sign stale-fence receipt: %v", err)
	}
	_, err = host.Return(ReturnRequest{
		GrantID:    "root",
		Partial:    true,
		Dimensions: Dimensions(left),
		Sequences:  SeqRangeSet(1, 20),
		Receipts:   []ReturnReceipt{staleFence},
	})
	if !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("stale receipt fence = %v, want invalid receipt", err)
	}

	_, err = host.Return(ReturnRequest{
		GrantID:    "root",
		Partial:    true,
		Dimensions: Dimensions(left),
		Sequences:  SeqRangeSet(1, 20),
		Receipts:   []ReturnReceipt{directOnly},
	})
	var unsettled *UnsettledError
	if !errors.As(err, &unsettled) {
		t.Fatalf("root return without grandchild evidence = %v, want unsettled", err)
	}
	if want := []GrantID{"g"}; !reflect.DeepEqual(unsettled.GrantIDs, want) {
		t.Fatalf("unsettled descendants = %v, want %v", unsettled.GrantIDs, want)
	}

	gReceipt1 := signedReturn(t, host, "g", Scope{
		Sequences: SeqRangeSet(1, 10),
	}, false)
	first, err := host.Return(ReturnRequest{
		GrantID:   "a",
		Partial:   true,
		Sequences: SeqRangeSet(1, 10),
		Receipts:  []ReturnReceipt{gReceipt1},
	})
	if err != nil {
		t.Fatalf("first partial A return: %v", err)
	}
	if !first.Retained.Dimensions.Equal(Dimensions(left)) ||
		!first.Retained.Sequences.Equal(SeqRangeSet(11, 50)) {
		t.Fatalf("first retained scope = %+v", first.Retained)
	}

	second, err := host.Return(ReturnRequest{
		GrantID:    "a",
		Partial:    true,
		Dimensions: Dimensions(left),
		Sequences:  SeqRangeSet(11, 20),
		Seals: []DescendantSeal{{
			GrantID:    "g",
			Dimensions: Dimensions(left),
			Sequences:  SeqRangeSet(11, 20),
			Reason:     "fence remainder",
		}},
	})
	if err != nil {
		t.Fatalf("second partial A return: %v", err)
	}
	if !second.Retained.Dimensions.IsEmpty() ||
		!second.Retained.Sequences.Equal(SeqRangeSet(21, 50)) {
		t.Fatalf("second retained scope = %+v", second.Retained)
	}

	_, err = host.AuthorizeControl(ControlRequest{
		GrantID:        childG.ID,
		ProcessStartID: childG.ProcessStartID,
		SessionRoot:    "session",
		Sequence:       11,
		Signal:         SignalInterrupt,
		Dimensions:     Dimensions(left),
		OperationID:    "g-late",
	})
	if !errors.Is(err, ErrGrantInactive) {
		t.Fatalf("late grandchild control = %v, want inactive", err)
	}

	_, err = host.AuthorizeControl(ControlRequest{
		GrantID:        "a",
		ProcessStartID: childA.ProcessStartID,
		SessionRoot:    "session",
		Sequence:       22,
		Signal:         SignalInterrupt,
		Dimensions:     Dimensions(left),
		OperationID:    "a-old-window",
	})
	if !errors.Is(err, ErrDimensionNotOwned) {
		t.Fatalf("A control on returned window = %v, want dimension rejection", err)
	}

	childC := mustDelegate(t, host, "root", DelegateRequest{
		GrantID:        "c",
		ProcessStartID: testProcess("boot", 104, 1004),
		SessionRoot:    "session",
		Signals:        Signals(SignalInterrupt),
		Dimensions:     Dimensions(left),
		Sequences:      SeqRangeSet(101, 150),
	})
	if _, err := host.AuthorizeControl(ControlRequest{
		GrantID:        childC.ID,
		ProcessStartID: childC.ProcessStartID,
		SessionRoot:    "session",
		Sequence:       101,
		Signal:         SignalInterrupt,
		Dimensions:     Dimensions(left),
		OperationID:    "c-101",
	}); err != nil {
		t.Fatalf("new child on freed window: %v", err)
	}

	final, err := host.Return(ReturnRequest{GrantID: "a"})
	if err != nil {
		t.Fatalf("final A return: %v", err)
	}
	if !final.Closed {
		t.Fatal("A should be closed after residual return")
	}
	_, err = host.AuthorizeControl(ControlRequest{
		GrantID:        "a",
		ProcessStartID: childA.ProcessStartID,
		SessionRoot:    "session",
		Sequence:       23,
		Signal:         SignalInterrupt,
		Dimensions:     Dimensions(left),
		OperationID:    "a-after-close",
	})
	if !errors.Is(err, ErrGrantInactive) {
		t.Fatalf("late A control = %v, want inactive", err)
	}

	_, err = host.Delegate("root", DelegateRequest{
		GrantID:        "a",
		ProcessStartID: testProcess("boot", 105, 1005),
		SessionRoot:    "session",
		Signals:        Signals(SignalInterrupt),
		Dimensions:     Dimensions(left),
		Sequences:      SeqRangeSet(151, 160),
	})
	if !errors.Is(err, ErrGrantExists) {
		t.Fatalf("grant-id reuse = %v, want ErrGrantExists", err)
	}

	_, err = host.AuthorizeControl(ControlRequest{
		GrantID:        "root",
		ProcessStartID: NewProcessStartID(rootStart.BootID, rootStart.PID, rootStart.Start+1),
		SessionRoot:    "session",
		Sequence:       199,
		Signal:         SignalInterrupt,
		Dimensions:     Dimensions(right),
		OperationID:    "reused-pid",
	})
	if !errors.Is(err, ErrNotHolder) {
		t.Fatalf("reused process start = %v, want ErrNotHolder", err)
	}
}

func TestEffectReceiptsAreIdempotentAndQueryable(t *testing.T) {
	dimension := WindowDimension("display", "window")
	root := Grant{
		ID:             "root",
		ProcessStartID: testProcess("boot", 200, 2000),
		SessionRoot:    "session",
		Signals:        Signals(SignalInterrupt, SignalTerminate),
		Dimensions:     Dimensions(dimension),
		Sequences:      SeqRangeSet(1, 10),
	}
	host := newTreeHost(t, root)
	request := ControlRequest{
		GrantID:        "root",
		ProcessStartID: root.ProcessStartID,
		SessionRoot:    "session",
		Sequence:       1,
		Signal:         SignalInterrupt,
		Dimensions:     Dimensions(dimension),
		OperationID:    "op-1",
	}
	first, err := host.AuthorizeControl(request)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if first.Status != EffectPending {
		t.Fatalf("initial effect = %s", first.Status)
	}
	second, err := host.AuthorizeControl(request)
	if err != nil {
		t.Fatalf("idempotent authorize: %v", err)
	}
	if second.AcceptedAt != first.AcceptedAt {
		t.Fatal("idempotent authorization produced a new receipt")
	}
	if _, err := host.AuthorizeControl(ControlRequest{
		GrantID:        "root",
		ProcessStartID: root.ProcessStartID,
		SessionRoot:    "session",
		Sequence:       1,
		Signal:         SignalInterrupt,
		Dimensions:     Dimensions(dimension),
		OperationID:    "op-other",
	}); !errors.Is(err, ErrSequenceUsed) {
		t.Fatalf("second operation on one sequence = %v", err)
	}

	applied := first
	applied.Status = EffectApplied
	applied.Detail = "delivered"
	if err := host.RecordEffect(applied); err != nil {
		t.Fatalf("record effect: %v", err)
	}
	if err := host.RecordEffect(applied); err != nil {
		t.Fatalf("repeat effect: %v", err)
	}
	queried, ok := host.Receipt("op-1")
	if !ok || queried.Status != EffectApplied || queried.Detail != "delivered" {
		t.Fatalf("queried receipt = %+v, %v", queried, ok)
	}
	bySequence, ok := host.ReceiptForSequence("root", 1)
	if !ok || bySequence.OperationID != "op-1" {
		t.Fatalf("receipt by sequence = %+v, %v", bySequence, ok)
	}
	rejected := applied
	rejected.Status = EffectRejected
	if err := host.RecordEffect(rejected); !errors.Is(err, ErrEffectTransition) {
		t.Fatalf("illegal effect transition = %v", err)
	}

	if _, err := host.AuthorizeControl(ControlRequest{
		GrantID:        "root",
		ProcessStartID: root.ProcessStartID,
		SessionRoot:    "session",
		Sequence:       2,
		Signal:         SignalKill,
		Dimensions:     Dimensions(dimension),
		OperationID:    "disallowed-signal",
	}); !errors.Is(err, ErrSignalNotAllowed) {
		t.Fatalf("disallowed signal = %v", err)
	}
}

func TestReconcileProducesOrderIndependentPartition(t *testing.T) {
	universe := SeqRangeSet(1, 25)
	observations := []Observation{
		{GrantID: "a", Epoch: 1, Scope: Scope{Sequences: SeqRangeSet(1, 10)}, Class: EvidenceControlled},
		{GrantID: "a", Epoch: 2, Scope: Scope{Sequences: SeqRangeSet(8, 12)}, Class: EvidenceInvalid},
		{GrantID: "a", Epoch: 1, Scope: Scope{Sequences: SeqRangeSet(13, 15)}, Class: EvidencePending},
		{GrantID: "b", Epoch: 2, Scope: Scope{Sequences: SeqRangeSet(16, 20)}, Class: EvidenceControlled},
		{GrantID: "b", Epoch: 3, Scope: Scope{Sequences: SeqRangeSet(21, 25)}, Class: EvidenceControlled},
		{GrantID: "b", Epoch: 3, Scope: Scope{Sequences: SeqRangeSet(21, 25)}, Class: EvidenceInvalid},
	}
	want := Partition{
		Universe:   universe,
		Controlled: Set(Range(1, 7), Range(16, 20)),
		Invalid:    SeqRangeSet(8, 12),
		Pending:    Set(Range(13, 15), Range(21, 25)),
	}
	orders := [][]int{
		{0, 1, 2, 3, 4, 5},
		{5, 3, 0, 4, 2, 1},
		{2, 5, 1, 4, 0, 3},
	}
	for _, order := range orders {
		input := make([]Observation, 0, len(order))
		for _, index := range order {
			input = append(input, observations[index])
		}
		got, err := Reconcile(universe, input...)
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if !got.Controlled.Equal(want.Controlled) ||
			!got.Invalid.Equal(want.Invalid) ||
			!got.Pending.Equal(want.Pending) {
			t.Fatalf("partition for order %v = %+v, want %+v", order, got, want)
		}
		if err := got.Validate(); err != nil {
			t.Fatalf("partition validation: %v", err)
		}
	}

	viewA := []Observation{{Scope: Scope{Sequences: SeqRangeSet(1, 5)}, Class: EvidenceControlled}}
	viewB := []Observation{{Scope: Scope{Sequences: SeqRangeSet(1, 5)}, Class: EvidenceInvalid}}
	got, err := ReconcileViews(SeqRangeSet(1, 5), viewA, viewB)
	if err != nil {
		t.Fatalf("ReconcileViews: %v", err)
	}
	if !got.Pending.Equal(SeqRangeSet(1, 5)) || !got.Controlled.IsEmpty() || !got.Invalid.IsEmpty() {
		t.Fatalf("conservative views = %+v", got)
	}
}

type fakeTransport struct {
	mu         sync.Mutex
	deliveries int
	receipts   map[string]EffectReceipt
	deliverErr error
	lookupErr  error
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{receipts: make(map[string]EffectReceipt)}
}

func (f *fakeTransport) Deliver(_ context.Context, request ControlRequest) (EffectReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deliveries++
	if f.deliverErr != nil {
		return EffectReceipt{}, f.deliverErr
	}
	now := testClock()
	receipt := EffectReceipt{
		OperationID:    request.OperationID,
		GrantID:        request.GrantID,
		ProcessStartID: request.ProcessStartID,
		SessionRoot:    request.SessionRoot,
		Sequence:       request.Sequence,
		Signal:         request.Signal,
		Dimensions:     request.Dimensions.Clone(),
		Status:         EffectApplied,
		AcceptedAt:     now,
		UpdatedAt:      now,
	}
	f.receipts[request.OperationID] = receipt
	return receipt, nil
}

func (f *fakeTransport) Lookup(_ context.Context, operationID string) (EffectReceipt, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lookupErr != nil {
		return EffectReceipt{}, false, f.lookupErr
	}
	receipt, ok := f.receipts[operationID]
	return receipt, ok, nil
}

func (f *fakeTransport) deliveryCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deliveries
}

func (f *fakeTransport) putReceipt(receipt EffectReceipt) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.receipts[receipt.OperationID] = receipt
}

func TestEmitterIsIdempotentAndRecoversWithoutReplay(t *testing.T) {
	dimension := WindowDimension("display", "window")
	root := Grant{
		ID:             "root",
		ProcessStartID: testProcess("boot", 300, 3000),
		SessionRoot:    "session",
		Signals:        Signals(SignalInterrupt),
		Dimensions:     Dimensions(dimension),
		Sequences:      SeqRangeSet(1, 10),
	}
	host := newTreeHost(t, root)
	journal := NewMemoryJournal()
	transport := newFakeTransport()
	emitter, err := NewEmitter(journal, transport, host, WithEmitterClock(testClock))
	if err != nil {
		t.Fatalf("NewEmitter: %v", err)
	}
	request := ControlRequest{
		GrantID:        "root",
		ProcessStartID: root.ProcessStartID,
		SessionRoot:    "session",
		Sequence:       1,
		Signal:         SignalInterrupt,
		Dimensions:     Dimensions(dimension),
		OperationID:    "emit-1",
	}
	receipt, err := emitter.Emit(context.Background(), request)
	if err != nil || receipt.Status != EffectApplied {
		t.Fatalf("Emit = %+v, %v", receipt, err)
	}
	if transport.deliveryCount() != 1 {
		t.Fatalf("deliveries = %d", transport.deliveryCount())
	}
	if _, err := emitter.Emit(context.Background(), request); err != nil {
		t.Fatalf("idempotent Emit: %v", err)
	}
	if transport.deliveryCount() != 1 {
		t.Fatalf("idempotent Emit replayed signal: %d", transport.deliveryCount())
	}

	recoveryHostRoot := root
	recoveryHostRoot.ID = "recovery-root"
	recoveryHostRoot.Sequences = SeqRangeSet(1, 10)
	recoveryHost, err := NewHost(recoveryHostRoot, WithSharedHMACKey("test-key", []byte("test-secret")), WithNow(testClock))
	if err != nil {
		t.Fatalf("recovery host: %v", err)
	}
	recoveryJournal := NewMemoryJournal()
	secondRequest := request
	secondRequest.GrantID = recoveryHost.rootID
	secondRequest.OperationID = "emit-2"
	secondRequest.Sequence = 2
	if _, err := recoveryHost.AuthorizeControl(secondRequest); err != nil {
		t.Fatalf("authorize recovery request: %v", err)
	}
	// The receiver applied the effect, but the sending process died before it
	// could confirm the journal entry.
	applied, err := transport.Deliver(context.Background(), secondRequest)
	if err != nil {
		t.Fatalf("simulate receiver effect: %v", err)
	}
	if err := recoveryJournal.Put(JournalRecord{
		OperationID: secondRequest.OperationID,
		Request:     secondRequest,
		Phase:       JournalDispatched,
		Attempts:    1,
		UpdatedAt:   testClock(),
	}); err != nil {
		t.Fatalf("seed journal: %v", err)
	}
	deliveriesBeforeRecovery := transport.deliveryCount()
	recoveredEmitter, err := NewEmitter(recoveryJournal, transport, recoveryHost, WithEmitterClock(testClock))
	if err != nil {
		t.Fatalf("recovery emitter: %v", err)
	}
	report, err := recoveredEmitter.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if report.Recovered != 1 || report.InDoubt != 0 {
		t.Fatalf("recovery report = %+v", report)
	}
	if transport.deliveryCount() != deliveriesBeforeRecovery {
		t.Fatalf("recovery replayed signal: %d -> %d", deliveriesBeforeRecovery, transport.deliveryCount())
	}
	record, ok, err := recoveryJournal.Get(secondRequest.OperationID)
	if err != nil || !ok || record.Phase != JournalConfirmed {
		t.Fatalf("journal after recovery = %+v, %v, %v", record, ok, err)
	}
	if record.Receipt.Status != applied.Status {
		t.Fatalf("recovered receipt = %+v", record.Receipt)
	}

	// A dispatched record with no receiver receipt is parked in doubt rather
	// than replayed speculatively.
	third := secondRequest
	third.OperationID = "emit-3"
	third.Sequence = 3
	if err := recoveryJournal.Put(JournalRecord{
		OperationID: third.OperationID,
		Request:     third,
		Phase:       JournalDispatched,
		Attempts:    1,
		UpdatedAt:   testClock(),
	}); err != nil {
		t.Fatalf("seed in-doubt journal: %v", err)
	}
	report, err = recoveredEmitter.Recover(context.Background())
	if err != nil {
		t.Fatalf("second Recover: %v", err)
	}
	if report.InDoubt != 1 {
		t.Fatalf("in-doubt report = %+v", report)
	}
	if transport.deliveryCount() != deliveriesBeforeRecovery {
		t.Fatalf("in-doubt recovery replayed signal: %d", transport.deliveryCount())
	}
}

package recovery

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestPartialWritesAtEveryLaneSlot(t *testing.T) {
	f := makePairFixture(t, 10, 2, 2, 3)
	newBlocks := []Block{
		testBlock(f.tenant, "partial-01", "partial-data-01", 2),
		testBlock(f.tenant, "partial-02", "partial-data-02", 2),
	}
	prefixes, receipt := captureSnapshotPair(t, f.left, f.right, f.keys, f.layout, f.lease, "partial", 1, newBlocks)
	final := prefixes[len(prefixes)-1]
	lane := f.layout.Lanes[0]

	var cases []CrashCase
	for region := RegionA; region <= RegionB; region++ {
		for slot := lane.Start; slot < lane.Start+lane.Slots; slot++ {
			left := cloneRawState(final.left)
			right := cloneRawState(final.right)
			corruptFrame(left, region, slot)
			corruptFrame(right, region, slot)
			want := expectedLive(receipt.Generation, receipt.Txn, newBlocks)
			if region == receipt.Region && slot >= receipt.FirstSlot && slot < receipt.FirstSlot+receipt.FrameCount {
				want = expectedLive(1, f.initialTxn.ID, f.initialBlock)
			}
			cases = append(cases, CrashCase{
				Name:     fmt.Sprintf("partial-%s-%d", region, slot),
				Left:     left,
				Right:    right,
				Expected: want,
			})
		}
	}
	report, err := VerifyCrashPrefixes(verifyConfig(f, f.lease, 0), cases)
	if err != nil {
		t.Fatal(err)
	}
	if report.Prefixes != len(cases) {
		t.Fatalf("checked %d of %d prefixes", report.Prefixes, len(cases))
	}
}

func corruptFrame(state RawState, region Region, slot int) {
	var frame []byte
	if region == RegionA {
		frame = state.A[slot]
		if frame == nil {
			frame = make([]byte, state.FrameSize)
			state.A[slot] = frame
		}
	} else {
		frame = state.B[slot]
		if frame == nil {
			frame = make([]byte, state.FrameSize)
			state.B[slot] = frame
		}
	}
	cut := len(frame) / 2
	for i := cut; i < len(frame); i++ {
		frame[i] = 0
	}
}

func TestWriteRetryIsVisibleAndDeterministic(t *testing.T) {
	f := makePairFixture(t, 10, 1, 1, 3)
	for _, store := range []*MemStore{f.left, f.right} {
		store.QueueFailure(Failure{Op: OpWrite, Region: RegionB, Slot: 0, Kind: FailPartialWrite, Bytes: 100})
	}
	blocks := []Block{testBlock(f.tenant, "retry-block", "retry-data", 2)}
	txn := signedTxn(t, f.keys, f.tenant, f.lease, 1, "retry")
	var traces [2][]Event
	var receipts [2]CommitReceipt
	for i, store := range []*MemStore{f.left, f.right} {
		engine, err := New(store, f.keys, Config{Layout: f.layout, FrameSize: f.frameSize, MaxRetries: 2},
			WithObserver(ObserverFunc(func(event Event) { traces[i] = append(traces[i], event) })))
		if err != nil {
			t.Fatal(err)
		}
		batch, err := engine.Begin(txn)
		if err != nil {
			t.Fatal(err)
		}
		if err := batch.Stage(blocks[0]); err != nil {
			t.Fatal(err)
		}
		receipt, err := batch.Commit()
		if err != nil {
			t.Fatal(err)
		}
		receipts[i] = receipt
	}
	if !reflect.DeepEqual(receipts[0], receipts[1]) {
		t.Fatalf("retry receipts differ")
	}
	if !reflect.DeepEqual(traces[0], traces[1]) {
		t.Fatalf("retry traces differ")
	}
	foundRetry := false
	for i, event := range traces[0] {
		if event.Op == OpWrite && event.Region == RegionB && event.Slot == 0 && event.Phase == PhaseBegin && event.Attempt == 1 {
			if !event.Failed || event.Retry != 0 {
				t.Fatalf("first attempt not recorded: %+v", event)
			}
			if i+1 >= len(traces[0]) {
				t.Fatal("missing retry event")
			}
			retry := traces[0][i+1]
			if retry.Op != OpWrite || retry.Slot != 0 || retry.Phase != PhaseBegin || retry.Failed || retry.Retry != 1 {
				t.Fatalf("retry not visible: %+v", retry)
			}
			foundRetry = true
		}
	}
	if !foundRetry {
		t.Fatal("did not observe retry metadata")
	}
}

func TestDurableReceiptAfterAmbiguousWriteIsEvidence(t *testing.T) {
	f := makePairFixture(t, 10, 1, 1, 3)
	blocks := []Block{testBlock(f.tenant, "ambiguous", "ambiguous-data", 2)}
	txn := signedTxn(t, f.keys, f.tenant, f.lease, 1, "ambiguous")
	// Header, one block, prepare, decision, receipt => receipt at slot 4.
	for _, store := range []*MemStore{f.left, f.right} {
		for i := 0; i < 3; i++ {
			store.QueueFailure(Failure{Op: OpWrite, Region: RegionB, Slot: 4, Kind: FailAfterWrite})
		}
	}
	for _, store := range []*MemStore{f.left, f.right} {
		engine := testEngine(t, store, f.keys, f.layout)
		batch, err := engine.Begin(txn)
		if err != nil {
			t.Fatal(err)
		}
		if err := batch.Stage(blocks[0]); err != nil {
			t.Fatal(err)
		}
		if _, err := batch.Commit(); err == nil {
			t.Fatal("expected ambiguous receipt write error")
		}
	}
	cases := []CrashCase{{
		Name:     "ambiguous-receipt",
		Left:     f.left.Snapshot(),
		Right:    f.right.Snapshot(),
		Expected: expectedLive(2, txn.ID, blocks),
	}}
	if _, err := VerifyCrashPrefixes(verifyConfig(f, f.lease, 0), cases); err != nil {
		t.Fatal(err)
	}
}

func TestResolveRequiresTheSameSignedTransaction(t *testing.T) {
	tenant := TenantIDFromString("tenant-token")
	lease := Lease{Tenant: tenant, Epoch: 1, ID: LeaseIDFromString("lease-token")}
	layout := SingleTenantLayout(tenant, 12)
	store, _ := NewMemStore(12, 1024)
	keys := testKeyring(tenant)
	engine := testEngine(t, store, keys, layout)
	txn := signedTxn(t, keys, tenant, lease, 0, "token")
	batch, err := engine.Begin(txn)
	if err != nil {
		t.Fatal(err)
	}
	if err := batch.Stage(testBlock(tenant, "token-block", "token-data", 1)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		store.QueueFailure(Failure{Op: OpWrite, Region: RegionA, Slot: 4, Kind: FailBeforeWrite})
	}
	if _, err := batch.Commit(); err == nil {
		t.Fatal("expected receipt failure")
	}
	forged := txn
	forged.Nonce[0] ^= 0xff
	forged, err = keys.SignTransaction(forged)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Resolve(forged); !errors.Is(err, ErrTxnMismatch) {
		t.Fatalf("forged token error %v, want mismatch", err)
	}
	resolved, err := engine.Resolve(txn)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.State != ResolveRetrieved || resolved.Receipt == nil || resolved.Receipt.Txn != txn.ID {
		t.Fatalf("same token did not retrieve: %+v", resolved)
	}
	next := signedTxn(t, keys, tenant, lease, 1, "after-token")
	nextBatch, err := engine.Begin(next)
	if err != nil {
		t.Fatalf("engine still has a stale open batch: %v", err)
	}
	nextBatch.Abort()
}

func TestReadRetryIsBoundedAndFailStopIsDeterministic(t *testing.T) {
	f := makePairFixture(t, 10, 1, 2, 3)
	transientLeft, _ := f.left.Clone()
	transientRight, _ := f.right.Clone()
	for _, store := range []*MemStore{transientLeft, transientRight} {
		store.QueueFailure(Failure{Op: OpRead, Region: RegionA, Slot: 3, Kind: FailRead})
	}
	leftResult := mustRecover(t, transientLeft, verifyConfig(f, f.lease, 0))
	rightResult := mustRecover(t, transientRight, verifyConfig(f, f.lease, 0))
	if leftResult.Receipt == nil || !reflect.DeepEqual(leftResult.Trace, rightResult.Trace) {
		t.Fatalf("transient read retry diverged")
	}
	seen := false
	for i, event := range leftResult.Trace {
		if event.Region == RegionA && event.Slot == 3 && event.Attempt == 1 {
			if !event.Failed || event.Retry != 0 || i+1 >= len(leftResult.Trace) {
				t.Fatalf("bad first read attempt: %+v", event)
			}
			retry := leftResult.Trace[i+1]
			if retry.Region != RegionA || retry.Slot != 3 || retry.Attempt != 2 || retry.Retry != 1 || retry.Failed {
				t.Fatalf("bad read retry: %+v", retry)
			}
			seen = true
		}
	}
	if !seen {
		t.Fatal("read retry not visible")
	}

	failLeft, _ := f.left.Clone()
	failRight, _ := f.right.Clone()
	for _, store := range []*MemStore{failLeft, failRight} {
		for i := 0; i < 3; i++ {
			store.QueueFailure(Failure{Op: OpRead, Region: RegionB, Slot: 7, Kind: FailRead})
		}
	}
	leftCfg := verifyConfig(f, f.lease, 0)
	leftTarget, err := New(failLeft, f.keys, Config{Layout: f.layout, FrameSize: f.frameSize, MaxRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	rightTarget, err := New(failRight, f.keys, Config{Layout: f.layout, FrameSize: f.frameSize, MaxRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	leftFail, leftErr := leftTarget.Recover(RecoveryRequest{Tenant: leftCfg.Tenant, Lease: f.lease})
	rightFail, rightErr := rightTarget.Recover(RecoveryRequest{Tenant: leftCfg.Tenant, Lease: f.lease})
	if !errors.Is(leftErr, ErrIO) || !errors.Is(rightErr, ErrIO) {
		t.Fatalf("expected fail-stop errors, got %v/%v", leftErr, rightErr)
	}
	if leftFail.Receipt != nil || !reflect.DeepEqual(leftFail.Trace, rightFail.Trace) {
		t.Fatalf("permanent read failure diverged")
	}
	plan, err := leftTarget.Plan(f.tenant)
	if err != nil {
		t.Fatal(err)
	}
	if len(leftFail.Trace) > plan.MaxReadAttempts {
		t.Fatalf("trace %d exceeds bounded attempts %d", len(leftFail.Trace), plan.MaxReadAttempts)
	}
}

package recovery

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

type pairFixture struct {
	tenant       TenantID
	foreign      TenantID
	lease        Lease
	foreignLeft  Lease
	foreignRight Lease
	layout       Layout
	keys         Keyring
	frameSize    int
	left         *MemStore
	right        *MemStore
	initial      RawState
	initialTxn   SignedTxn
	initialBlock []Block
}

func makePairFixture(t *testing.T, laneSlots, initialBlocks, leftForeignBlocks, rightForeignBlocks int) *pairFixture {
	t.Helper()
	if laneSlots < minLaneSlots {
		t.Fatalf("lane too small")
	}
	tenant := TenantIDFromString("tenant-target")
	foreign := TenantIDFromString("tenant-foreign")
	lease := Lease{Tenant: tenant, Epoch: 1, ID: LeaseIDFromString("lease-target-1")}
	layout := Layout{
		RegionSlots: 2 * laneSlots,
		Lanes: []LaneSpec{
			{Tenant: tenant, Start: 0, Slots: laneSlots},
			{Tenant: foreign, Start: laneSlots, Slots: laneSlots},
		},
	}
	keys := testKeyring(tenant, foreign)
	base, err := NewMemStore(layout.RegionSlots, 1024)
	if err != nil {
		t.Fatal(err)
	}
	engine := testEngine(t, base, keys, layout)
	txn := signedTxn(t, keys, tenant, lease, 0, "initial")
	batch, err := engine.Begin(txn)
	if err != nil {
		t.Fatal(err)
	}
	blocks := make([]Block, initialBlocks)
	for i := range blocks {
		blocks[i] = testBlock(tenant, fmt.Sprintf("target-initial-%02d", i), fmt.Sprintf("target-initial-data-%02d", i), 1)
		if err := batch.Stage(blocks[i]); err != nil {
			t.Fatalf("stage initial block: %v", err)
		}
	}
	if _, err := batch.Commit(); err != nil {
		t.Fatalf("initial commit: %v", err)
	}
	initial := base.Snapshot()
	left, err := NewMemStoreFromRaw(initial)
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewMemStoreFromRaw(initial)
	if err != nil {
		t.Fatal(err)
	}
	foreignLeft := Lease{Tenant: foreign, Epoch: 9, ID: LeaseIDFromString("foreign-left")}
	foreignRight := Lease{Tenant: foreign, Epoch: 17, ID: LeaseIDFromString("foreign-right")}
	commitForeign(t, left, keys, layout, foreignLeft, leftForeignBlocks, "left")
	commitForeign(t, right, keys, layout, foreignRight, rightForeignBlocks, "right")
	return &pairFixture{
		tenant:       tenant,
		foreign:      foreign,
		lease:        lease,
		foreignLeft:  foreignLeft,
		foreignRight: foreignRight,
		layout:       layout,
		keys:         keys,
		frameSize:    1024,
		left:         left,
		right:        right,
		initial:      initial,
		initialTxn:   txn,
		initialBlock: blocks,
	}
}

func commitForeign(t *testing.T, store *MemStore, keys Keyring, layout Layout, lease Lease, blocks int, side string) {
	t.Helper()
	engine := testEngine(t, store, keys, layout)
	txn := signedTxn(t, keys, lease.Tenant, lease, 0, "foreign-"+side)
	batch, err := engine.Begin(txn)
	if err != nil {
		t.Fatalf("foreign begin: %v", err)
	}
	for i := 0; i < blocks; i++ {
		block := testBlock(lease.Tenant, fmt.Sprintf("foreign-%s-%02d", side, i), fmt.Sprintf("foreign-secret-%s-%02d", side, i), uint64(i+1))
		if err := batch.Stage(block); err != nil {
			t.Fatalf("foreign stage: %v", err)
		}
	}
	if _, err := batch.Commit(); err != nil {
		t.Fatalf("foreign commit: %v", err)
	}
}

type prefixPair struct {
	left  RawState
	right RawState
}

func captureSnapshotPair(t *testing.T, left, right *MemStore, keys Keyring, layout Layout,
	lease Lease, txnLabel string, base uint64, blocks []Block) ([]prefixPair, CommitReceipt) {
	t.Helper()
	var leftPrefixes, rightPrefixes []RawState
	left.SetAckHook(func() { leftPrefixes = append(leftPrefixes, left.Snapshot()) })
	right.SetAckHook(func() { rightPrefixes = append(rightPrefixes, right.Snapshot()) })
	defer func() {
		left.SetAckHook(nil)
		right.SetAckHook(nil)
	}()
	txn := signedTxn(t, keys, lease.Tenant, lease, base, txnLabel)
	var receipts [2]CommitReceipt
	for i, store := range []*MemStore{left, right} {
		engine := testEngine(t, store, keys, layout)
		batch, err := engine.Begin(txn)
		if err != nil {
			t.Fatalf("begin %d: %v", i, err)
		}
		for _, block := range blocks {
			if err := batch.Stage(block); err != nil {
				t.Fatalf("stage %d: %v", i, err)
			}
		}
		receipt, err := batch.Commit()
		if err != nil {
			t.Fatalf("commit %d: %v", i, err)
		}
		receipts[i] = receipt
	}
	if !reflect.DeepEqual(receipts[0], receipts[1]) {
		t.Fatalf("pair receipts diverged: %+v %+v", receipts[0], receipts[1])
	}
	if len(leftPrefixes) != len(rightPrefixes) || len(leftPrefixes) == 0 {
		t.Fatalf("prefix counts %d/%d", len(leftPrefixes), len(rightPrefixes))
	}
	out := make([]prefixPair, len(leftPrefixes))
	for i := range out {
		out[i] = prefixPair{left: leftPrefixes[i], right: rightPrefixes[i]}
	}
	return out, receipts[0]
}

func captureDeletePair(t *testing.T, left, right *MemStore, keys Keyring, layout Layout,
	lease Lease, txnLabel string, base uint64) ([]prefixPair, CommitReceipt) {
	t.Helper()
	var leftPrefixes, rightPrefixes []RawState
	left.SetAckHook(func() { leftPrefixes = append(leftPrefixes, left.Snapshot()) })
	right.SetAckHook(func() { rightPrefixes = append(rightPrefixes, right.Snapshot()) })
	defer func() {
		left.SetAckHook(nil)
		right.SetAckHook(nil)
	}()
	txn := signedTxn(t, keys, lease.Tenant, lease, base, txnLabel)
	var receipts [2]CommitReceipt
	for i, store := range []*MemStore{left, right} {
		engine := testEngine(t, store, keys, layout)
		receipt, err := engine.Delete(txn)
		if err != nil {
			t.Fatalf("delete %d: %v", i, err)
		}
		receipts[i] = receipt
	}
	if !reflect.DeepEqual(receipts[0], receipts[1]) {
		t.Fatalf("delete receipts diverged")
	}
	if len(leftPrefixes) != len(rightPrefixes) || len(leftPrefixes) == 0 {
		t.Fatalf("delete prefix counts %d/%d", len(leftPrefixes), len(rightPrefixes))
	}
	out := make([]prefixPair, len(leftPrefixes))
	for i := range out {
		out[i] = prefixPair{left: leftPrefixes[i], right: rightPrefixes[i]}
	}
	return out, receipts[0]
}

func expectedLive(gen uint64, txn TxID, blocks []Block) Expected {
	return Expected{Set: true, Generation: gen, Txn: txn, Blocks: blocks}
}

func expectedDeleted(gen uint64, txn TxID) Expected {
	return Expected{Set: true, Deleted: true, Generation: gen, Txn: txn}
}

func expectedFromResult(result RecoveryResult) Expected {
	if result.Receipt == nil {
		return Expected{}
	}
	want := Expected{Set: true, Generation: result.Receipt.Generation, Txn: result.Receipt.Txn, Deleted: result.Deleted}
	if result.Snapshot != nil {
		want.Blocks = cloneBlocks(result.Snapshot.Blocks)
	}
	return want
}

func cloneBlocks(blocks []Block) []Block {
	out := make([]Block, len(blocks))
	for i, block := range blocks {
		out[i] = cloneBlock(block)
	}
	return out
}

func verifyConfig(f *pairFixture, lease Lease, minGeneration uint64) VerifyConfig {
	return VerifyConfig{
		Layout:          f.layout,
		Keys:            f.keys,
		FrameSize:       f.frameSize,
		MaxRetries:      2,
		Tenant:          f.tenant,
		Lease:           lease,
		MinGeneration:   minGeneration,
		ForbiddenTenant: f.foreign,
	}
}

func mustRecover(t *testing.T, store *MemStore, cfg VerifyConfig) RecoveryResult {
	t.Helper()
	engine := testEngine(t, store, cfg.Keys, cfg.Layout)
	result, err := engine.Recover(RecoveryRequest{Tenant: cfg.Tenant, Lease: cfg.Lease, MinGeneration: cfg.MinGeneration})
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	return result
}

func TestForeignTenantDifferencesProduceIdenticalTrace(t *testing.T) {
	f := makePairFixture(t, 10, 2, 1, 4)
	cfg := verifyConfig(f, f.lease, 0)
	leftEvents, leftResult, leftApplies, err := verifyOne(cfg, f.left)
	if err != nil {
		t.Fatal(err)
	}
	rightEvents, rightResult, rightApplies, err := verifyOne(cfg, f.right)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(leftEvents, rightEvents) {
		t.Fatalf("foreign data changed the visible recovery trace")
	}
	if !reflect.DeepEqual(leftResult.Snapshot, rightResult.Snapshot) {
		t.Fatalf("foreign data changed the recovered target snapshot")
	}
	if leftApplies != 1 || rightApplies != 1 {
		t.Fatalf("apply counts %d/%d", leftApplies, rightApplies)
	}
	for _, event := range leftEvents {
		if event.Slot < 0 || event.Slot >= 10 {
			t.Fatalf("target recovery read foreign lane slot %d", event.Slot)
		}
	}
}

func TestAllAcknowledgedCrashPrefixesOfCommit(t *testing.T) {
	f := makePairFixture(t, 10, 2, 1, 3)
	newBlocks := []Block{
		testBlock(f.tenant, "new-01", "new-data-01", 2),
		testBlock(f.tenant, "new-02", "new-data-02", 2),
	}
	prefixes, receipt := captureSnapshotPair(t, f.left, f.right, f.keys, f.layout, f.lease, "new", 1, newBlocks)
	if len(prefixes) != 6 {
		t.Fatalf("captured %d prefixes, want header+2 blocks+3 trailers", len(prefixes))
	}
	var cases []CrashCase
	for i, prefix := range prefixes {
		want := expectedLive(1, f.initialTxn.ID, f.initialBlock)
		if i == len(prefixes)-1 {
			want = expectedLive(2, receipt.Txn, newBlocks)
		}
		cases = append(cases, CrashCase{
			Name:     fmt.Sprintf("commit-prefix-%d", i),
			Left:     prefix.left,
			Right:    prefix.right,
			Expected: want,
		})
	}
	report, err := VerifyCrashPrefixes(verifyConfig(f, f.lease, 0), cases)
	if err != nil {
		t.Fatal(err)
	}
	if report.Prefixes != len(prefixes) || report.ApplyCallbacks != len(prefixes) {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestTwoConsecutivePowerLossPrefixes(t *testing.T) {
	f := makePairFixture(t, 10, 1, 2, 3)
	firstBlocks := []Block{
		testBlock(f.tenant, "first-01", "first-data-01", 2),
		testBlock(f.tenant, "first-02", "first-data-02", 2),
	}
	firstPrefixes, firstReceipt := captureSnapshotPair(t, f.left, f.right, f.keys, f.layout, f.lease, "first", 1, firstBlocks)

	var cases []CrashCase
	secondCaseCount := 0
	for i, first := range firstPrefixes {
		leftStore, err := NewMemStoreFromRaw(first.left)
		if err != nil {
			t.Fatal(err)
		}
		rightStore, err := NewMemStoreFromRaw(first.right)
		if err != nil {
			t.Fatal(err)
		}
		leftCurrent := mustRecover(t, leftStore, verifyConfig(f, f.lease, 0))
		rightCurrent := mustRecover(t, rightStore, verifyConfig(f, f.lease, 0))
		if !reflect.DeepEqual(expectedFromResult(leftCurrent), expectedFromResult(rightCurrent)) {
			t.Fatalf("first crash %d diverged after reboot", i)
		}
		wantPrior := expectedFromResult(leftCurrent)
		base := uint64(1)
		if leftCurrent.Receipt != nil {
			base = leftCurrent.Receipt.Generation
		}
		secondBlocks := []Block{
			testBlock(f.tenant, fmt.Sprintf("second-%d-01", i), fmt.Sprintf("second-%d-data-01", i), 3),
		}
		secondPrefixes, secondReceipt := captureSnapshotPair(t, leftStore, rightStore, f.keys, f.layout, f.lease,
			fmt.Sprintf("second-%d", i), base, secondBlocks)
		secondCaseCount += len(secondPrefixes)
		for j, second := range secondPrefixes {
			want := wantPrior
			if j == len(secondPrefixes)-1 {
				want = expectedLive(base+1, secondReceipt.Txn, secondBlocks)
			}
			cases = append(cases, CrashCase{
				Name:     fmt.Sprintf("first-%d-second-%d", i, j),
				Left:     second.left,
				Right:    second.right,
				Expected: want,
			})
		}
	}
	if len(cases) != secondCaseCount {
		t.Fatalf("enumerated %d double-crash cases, want %d", len(cases), secondCaseCount)
	}
	report, err := VerifyCrashPrefixes(verifyConfig(f, f.lease, 0), cases)
	if err != nil {
		t.Fatal(err)
	}
	if report.Prefixes != len(cases) {
		t.Fatalf("report prefixes %d", report.Prefixes)
	}
	_ = firstReceipt
}

func TestStaleLogReplayRequiresGenerationAnchor(t *testing.T) {
	f := makePairFixture(t, 10, 2, 2, 3)
	gen1 := f.left.Snapshot()
	newBlocks := []Block{testBlock(f.tenant, "gen2", "gen2-data", 2)}
	prefixes, receipt := captureSnapshotPair(t, f.left, f.right, f.keys, f.layout, f.lease, "gen2", 1, newBlocks)
	gen2 := prefixes[len(prefixes)-1]

	// Replaying the old region over the newest region deletes the newer
	// authenticated evidence. Authentication cannot distinguish this from a
	// legitimate old disk; the host's monotonic generation anchor must.
	replayedLeft := replayLane(gen2.left, gen1, f.layout.Lanes[0])
	replayedRight := replayLane(gen2.right, gen1, f.layout.Lanes[0])
	staleCases := []CrashCase{{
		Name:     "both-regions-replayed",
		Left:     replayedLeft,
		Right:    replayedRight,
		Expected: Expected{MinRollback: true},
	}}
	report, err := VerifyCrashPrefixes(verifyConfig(f, f.lease, 2), staleCases)
	if err != nil {
		t.Fatal(err)
	}
	if report.ApplyCallbacks != 0 {
		t.Fatal("stale replay was applied despite generation anchor")
	}

	// Replaying only the inactive region leaves the newer receipt intact.
	oldRegion := RegionA
	if receipt.Region == RegionA {
		oldRegion = RegionB
	}
	oneRegionLeft := replayInactiveLane(gen2.left, gen1, f.layout.Lanes[0], oldRegion)
	oneRegionRight := replayInactiveLane(gen2.right, gen1, f.layout.Lanes[0], oldRegion)
	cases := []CrashCase{{
		Name:     "one-region-replayed",
		Left:     oneRegionLeft,
		Right:    oneRegionRight,
		Expected: expectedLive(receipt.Generation, receipt.Txn, newBlocks),
	}}
	if _, err := VerifyCrashPrefixes(verifyConfig(f, f.lease, 0), cases); err != nil {
		t.Fatal(err)
	}
}

func replayLane(dst, src RawState, lane LaneSpec) RawState {
	out := cloneRawState(dst)
	for slot := lane.Start; slot < lane.Start+lane.Slots; slot++ {
		out.A[slot] = cloneFrame(src.A[slot])
		out.B[slot] = cloneFrame(src.B[slot])
	}
	return out
}

func cloneFrame(frame []byte) []byte {
	if frame == nil {
		return nil
	}
	return append([]byte(nil), frame...)
}

func replayInactiveLane(dst, src RawState, lane LaneSpec, inactive Region) RawState {
	out := cloneRawState(dst)
	for slot := lane.Start; slot < lane.Start+lane.Slots; slot++ {
		var frame []byte
		if inactive == RegionA {
			frame = src.A[slot]
			if frame != nil {
				out.A[slot] = append([]byte(nil), frame...)
			}
		} else {
			frame = src.B[slot]
			if frame != nil {
				out.B[slot] = append([]byte(nil), frame...)
			}
		}
	}
	return out
}

func cloneRawState(state RawState) RawState {
	return RawState{
		Slots:     state.Slots,
		FrameSize: state.FrameSize,
		A:         cloneSlots(state.A),
		B:         cloneSlots(state.B),
	}
}

func TestDeleteNewLeaseAllCrashPrefixes(t *testing.T) {
	f := makePairFixture(t, 10, 1, 1, 3)
	prefixes, deleteReceipt := captureDeletePair(t, f.left, f.right, f.keys, f.layout, f.lease, "delete", 1)
	if len(prefixes) != 4 {
		t.Fatalf("delete prefixes %d", len(prefixes))
	}
	var oldCases, newCases []CrashCase
	for i, prefix := range prefixes {
		want := expectedLive(1, f.initialTxn.ID, f.initialBlock)
		if i == len(prefixes)-1 {
			want = expectedDeleted(deleteReceipt.Generation, deleteReceipt.Txn)
		}
		oldCases = append(oldCases, CrashCase{Name: fmt.Sprintf("delete-old-%d", i), Left: prefix.left, Right: prefix.right, Expected: want})
		newCases = append(newCases, CrashCase{Name: fmt.Sprintf("delete-new-%d", i), Left: prefix.left, Right: prefix.right, Expected: Expected{}})
	}
	if _, err := VerifyCrashPrefixes(verifyConfig(f, f.lease, 0), oldCases); err != nil {
		t.Fatal(err)
	}
	newLease := Lease{Tenant: f.tenant, Epoch: 2, ID: LeaseIDFromString("lease-target-2")}
	if _, err := VerifyCrashPrefixes(verifyConfig(f, newLease, 0), newCases); err != nil {
		t.Fatal(err)
	}

	leftStore, _ := NewMemStoreFromRaw(prefixes[len(prefixes)-1].left)
	rightStore, _ := NewMemStoreFromRaw(prefixes[len(prefixes)-1].right)
	newBlocks := []Block{testBlock(f.tenant, "new-lease-block", "new-lease-data", 1)}
	newPrefixes, newReceipt := captureSnapshotPair(t, leftStore, rightStore, f.keys, f.layout, newLease, "new-lease", 0, newBlocks)
	var newLeaseCases, oldLeaseCases []CrashCase
	for i, prefix := range newPrefixes {
		want := Expected{}
		oldWant := expectedDeleted(deleteReceipt.Generation, deleteReceipt.Txn)
		if i == len(newPrefixes)-1 {
			want = expectedLive(1, newReceipt.Txn, newBlocks)
			oldWant = expectedDeleted(deleteReceipt.Generation, deleteReceipt.Txn)
		}
		newLeaseCases = append(newLeaseCases, CrashCase{Name: fmt.Sprintf("new-lease-%d", i), Left: prefix.left, Right: prefix.right, Expected: want})
		oldLeaseCases = append(oldLeaseCases, CrashCase{Name: fmt.Sprintf("new-lease-old-view-%d", i), Left: prefix.left, Right: prefix.right, Expected: oldWant})
	}
	if _, err := VerifyCrashPrefixes(verifyConfig(f, newLease, 0), newLeaseCases); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyCrashPrefixes(verifyConfig(f, f.lease, 0), oldLeaseCases); err != nil {
		t.Fatal(err)
	}
}

func TestCapacityExhaustionAllSequences(t *testing.T) {
	f := makePairFixture(t, 6, 2, 1, 2)
	maxBlocks := 6 - minLaneSlots
	for count := 0; count <= maxBlocks+2; count++ {
		left, _ := f.left.Clone()
		right, _ := f.right.Clone()
		blocks := make([]Block, 0, count)
		for i := 0; i < count; i++ {
			blocks = append(blocks, testBlock(f.tenant, fmt.Sprintf("cap-%02d", i), fmt.Sprintf("cap-data-%02d", i), 2))
		}
		txn := signedTxn(t, f.keys, f.tenant, f.lease, 1, fmt.Sprintf("capacity-%d", count))
		failedAtStage := false
		for _, store := range []*MemStore{left, right} {
			engine := testEngine(t, store, f.keys, f.layout)
			batch, err := engine.Begin(txn)
			if err != nil {
				t.Fatalf("count %d begin: %v", count, err)
			}
			var stageErr error
			for _, block := range blocks {
				if stageErr = batch.Stage(block); stageErr != nil {
					break
				}
			}
			if count > maxBlocks {
				if !errors.Is(stageErr, ErrCapacity) {
					t.Fatalf("count %d: stage err %v, want capacity", count, stageErr)
				}
				if _, err := batch.Commit(); err == nil {
					t.Fatalf("count %d: failed staging batch committed", count)
				}
				failedAtStage = true
				batch.Abort()
			} else {
				if stageErr != nil {
					t.Fatalf("count %d stage: %v", count, stageErr)
				}
				if _, err := batch.Commit(); err != nil {
					t.Fatalf("count %d commit: %v", count, err)
				}
			}
		}
		caseWant := expectedLive(2, txn.ID, blocks)
		if failedAtStage {
			caseWant = expectedLive(1, f.initialTxn.ID, f.initialBlock)
		}
		cases := []CrashCase{{
			Name:     fmt.Sprintf("capacity-%d", count),
			Left:     left.Snapshot(),
			Right:    right.Snapshot(),
			Expected: caseWant,
		}}
		if _, err := VerifyCrashPrefixes(verifyConfig(f, f.lease, 0), cases); err != nil {
			t.Fatalf("count %d verify: %v", count, err)
		}
	}

	oversized := testBlock(f.tenant, "oversized", string(make([]byte, 4096)), 2)
	left, _ := f.left.Clone()
	engine := testEngine(t, left, f.keys, f.layout)
	txn := signedTxn(t, f.keys, f.tenant, f.lease, 1, "oversized")
	batch, err := engine.Begin(txn)
	if err != nil {
		t.Fatal(err)
	}
	if err := batch.Stage(oversized); !errors.Is(err, ErrBlockTooLarge) {
		t.Fatalf("oversized stage error %v", err)
	}
	batch.Abort()
	result := mustRecover(t, left, verifyConfig(f, f.lease, 0))
	if result.Receipt == nil || result.Receipt.Generation != 1 {
		t.Fatalf("oversized failure damaged old snapshot: %+v", result)
	}
}

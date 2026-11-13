package netsec

import (
	"errors"
	"testing"
	"time"
)

func testLedger(now *time.Time, ids ...string) *Ledger {
	next := 0
	return NewLedger(LedgerConfig{
		Clock: func() time.Time { return *now },
		BatchID: func() (string, error) {
			id := ids[next%len(ids)]
			next++
			return id, nil
		},
	})
}

func TestBatchCanonicalOrderAndTenantScopedIDs(t *testing.T) {
	now := time.Unix(100, 0)
	l := testLedger(&now, "batch-1", "batch-2")

	base := Request{
		ModelGeneration: 7,
		MaxOutputTokens: 4,
		DecodeStrategy:  "greedy",
	}
	aSame := base
	aSame.TenantID = "tenant-a"
	aSame.RequestID = "same"
	aSame.ArrivalSequence = 1
	bSame := base
	bSame.TenantID = "tenant-b"
	bSame.RequestID = "same"
	bSame.ArrivalSequence = 1
	aLater := base
	aLater.TenantID = "tenant-a"
	aLater.RequestID = "later"
	aLater.InputTokens = []uint32{9, 8}
	aLater.ArrivalSequence = 2
	otherPolicy := base
	otherPolicy.TenantID = "tenant-a"
	otherPolicy.RequestID = "sampled"
	otherPolicy.DecodeStrategy = "sample"
	otherPolicy.ArrivalSequence = 3

	for _, req := range []Request{bSame, aLater, otherPolicy, aSame} {
		if err := l.Submit(req); err != nil {
			t.Fatalf("Submit(%s/%s): %v", req.TenantID, req.RequestID, err)
		}
	}
	if IdentityDigest(aSame) == IdentityDigest(bSame) {
		t.Fatal("same tenant-local request ID must not collapse across tenants")
	}
	if err := l.Submit(aSame); !errors.Is(err, ErrDuplicateRequest) {
		t.Fatalf("duplicate active request: got %v, want ErrDuplicateRequest", err)
	}

	batch, err := l.NextBatch()
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch.BatchID == "" || batch.ModelGeneration != 7 || batch.DecodeStrategy != "greedy" {
		t.Fatalf("unexpected batch metadata: %+v", batch)
	}
	if len(batch.Rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(batch.Rows))
	}
	want := []struct {
		tenant  string
		request string
	}{
		{"tenant-a", "same"},
		{"tenant-b", "same"},
		{"tenant-a", "later"},
	}
	for i, expected := range want {
		row := batch.Rows[i]
		if row.RowIndex != i || row.TenantID != expected.tenant || row.RequestID != expected.request {
			t.Fatalf("row %d = %+v, want %+v", i, row, expected)
		}
		if row.IdentityDigest == "" || row.IdentityDigest != IdentityDigest(Request{
			TenantID:         row.TenantID,
			RequestID:        row.RequestID,
			ModelGeneration:  row.ModelGeneration,
			InputTokens:      row.InputTokens,
			MaxOutputTokens:  row.MaxOutputTokens,
			DecodeStrategy:   row.DecodeStrategy,
			CancelGeneration: row.CancelGeneration,
			Deadline:         row.Deadline,
		}) {
			t.Fatalf("row %d has an invalid identity digest", i)
		}
		if row.InputLength != len(row.InputTokens) || row.MaxOutputTokens != 4 {
			t.Fatalf("row %d has inconsistent immutable metadata: %+v", i, row)
		}
	}

	second, err := l.NextBatch()
	if err != nil {
		t.Fatalf("second NextBatch: %v", err)
	}
	if len(second.Rows) != 1 || second.Rows[0].DecodeStrategy != "sample" || second.Rows[0].RowIndex != 0 {
		t.Fatalf("strategy batch = %+v", second)
	}
}

func TestResultBindingUsesIndexAndDigestNotCompletionOrder(t *testing.T) {
	now := time.Unix(200, 0)
	l := testLedger(&now, "batch-order")
	a := Request{TenantID: "tenant-a", RequestID: "r", ModelGeneration: 1, MaxOutputTokens: 3, DecodeStrategy: "greedy"}
	b := Request{TenantID: "tenant-b", RequestID: "r", ModelGeneration: 1, MaxOutputTokens: 3, DecodeStrategy: "greedy"}
	if err := l.Submit(a); err != nil {
		t.Fatal(err)
	}
	if err := l.Submit(b); err != nil {
		t.Fatal(err)
	}
	batch, err := l.NextBatch()
	if err != nil {
		t.Fatal(err)
	}

	result := NewTensorResult(batch)
	result.Rows[0].Tokens = []uint32{10, 11}
	result.Rows[0].Done = true
	result.Rows[1].Tokens = []uint32{20}
	result.Rows[1].Done = true
	result.Rows[0], result.Rows[1] = result.Rows[1], result.Rows[0]

	deliveries, err := l.ApplyResult(result)
	if err != nil {
		t.Fatalf("ApplyResult: %v", err)
	}
	if len(deliveries) != 2 {
		t.Fatalf("got %d deliveries, want 2", len(deliveries))
	}
	if deliveries[0].RowIndex != 0 || deliveries[0].TenantID != "tenant-a" || deliveries[0].Tokens[0] != 10 {
		t.Fatalf("first delivery = %+v", deliveries[0])
	}
	if deliveries[1].RowIndex != 1 || deliveries[1].TenantID != "tenant-b" || deliveries[1].Tokens[0] != 20 {
		t.Fatalf("second delivery = %+v", deliveries[1])
	}

	badBatch, err := l.NextBatch()
	if errors.Is(err, ErrNoPendingRequest) {
		// Both requests are terminal; create a fresh ledger for the rejection
		// and retry check.
		l = testLedger(&now, "batch-bad")
		if err := l.Submit(a); err != nil {
			t.Fatal(err)
		}
		if err := l.Submit(b); err != nil {
			t.Fatal(err)
		}
		badBatch, err = l.NextBatch()
	}
	if err != nil {
		t.Fatalf("setup bad batch: %v", err)
	}
	badResult := NewTensorResult(badBatch)
	badResult.Rows[0].IdentityDigest = "not-the-row-identity"
	if _, err := l.ApplyResult(badResult); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("bad digest: got %v, want ErrIdentityMismatch", err)
	}
	if _, err := l.ApplyResult(badResult); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("second bad digest: got %v", err)
	}
}

func TestCancelRevokesOnlyItsRowAndDoesNotCompact(t *testing.T) {
	now := time.Unix(300, 0)
	l := testLedger(&now, "batch-cancel")
	requests := []Request{
		{TenantID: "a", RequestID: "0", ModelGeneration: 1, InputTokens: []uint32{1}, MaxOutputTokens: 4, CancelGeneration: 8},
		{TenantID: "b", RequestID: "1", ModelGeneration: 1, InputTokens: []uint32{2}, MaxOutputTokens: 4, CancelGeneration: 9},
		{TenantID: "c", RequestID: "2", ModelGeneration: 1, InputTokens: []uint32{3}, MaxOutputTokens: 4, CancelGeneration: 10},
	}
	for _, req := range requests {
		if err := l.Submit(req); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := l.NextBatch()
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Cancel("b", "1", 8); !errors.Is(err, ErrStaleCancellation) {
		t.Fatalf("stale cancel: got %v", err)
	}
	if err := l.Cancel("b", "1", 9); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	result := NewTensorResult(batch)
	for i := range result.Rows {
		result.Rows[i].Tokens = []uint32{uint32(100 + i)}
		result.Rows[i].Done = true
	}
	// Reverse the callback order; delivery must still use rowIndex and digest.
	result.Rows[0], result.Rows[2] = result.Rows[2], result.Rows[0]
	deliveries, err := l.ApplyResult(result)
	if err != nil {
		t.Fatalf("ApplyResult: %v", err)
	}
	if len(deliveries) != 2 {
		t.Fatalf("got %d deliveries, want 2", len(deliveries))
	}
	if deliveries[0].RowIndex != 0 || deliveries[0].TenantID != "a" || deliveries[0].Tokens[0] != 100 {
		t.Fatalf("row 0 delivery = %+v", deliveries[0])
	}
	if deliveries[1].RowIndex != 2 || deliveries[1].TenantID != "c" || deliveries[1].Tokens[0] != 102 {
		t.Fatalf("row 2 delivery = %+v", deliveries[1])
	}
	if len(batch.Rows) != 3 || batch.Rows[1].TenantID != "b" || batch.Rows[2].RowIndex != 2 {
		t.Fatalf("cancellation compacted canonical rows: %+v", batch.Rows)
	}
	if _, err := l.ApplyResult(result); err != nil {
		t.Fatalf("duplicate result after cancel: %v", err)
	}
}

func TestDeadlineAndModelGenerationInvalidateDelivery(t *testing.T) {
	now := time.Unix(400, 0)
	l := testLedger(&now, "batch-time")
	req := Request{
		TenantID: "a", RequestID: "deadline", ModelGeneration: 1,
		MaxOutputTokens: 2, Deadline: now.Add(time.Minute), DecodeStrategy: "greedy",
	}
	if err := l.Submit(req); err != nil {
		t.Fatal(err)
	}
	batch, err := l.NextBatch()
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	result := MarkerModel{}.Result(batch)
	deliveries, err := l.ApplyResult(result)
	if err != nil {
		t.Fatalf("late result: %v", err)
	}
	if len(deliveries) != 0 {
		t.Fatalf("late result delivered %d rows", len(deliveries))
	}

	now = time.Unix(500, 0)
	l = testLedger(&now, "batch-model")
	if err := l.Submit(Request{
		TenantID: "a", RequestID: "model", ModelGeneration: 1, MaxOutputTokens: 2, DecodeStrategy: "greedy",
	}); err != nil {
		t.Fatal(err)
	}
	batch, err = l.NextBatch()
	if err != nil {
		t.Fatal(err)
	}
	l.SetModelGeneration(2)
	deliveries, err = l.ApplyResult(MarkerModel{}.Result(batch))
	if !errors.Is(err, ErrModelGeneration) {
		t.Fatalf("model switch: got %v, want ErrModelGeneration", err)
	}
	if len(deliveries) != 0 {
		t.Fatalf("stale model batch delivered %d rows", len(deliveries))
	}
}

func TestDuplicateCallbackResultIsIdempotent(t *testing.T) {
	now := time.Unix(600, 0)
	l := testLedger(&now, "batch-dupe")
	if err := l.Submit(Request{
		TenantID: "a", RequestID: "r", ModelGeneration: 1,
		InputTokens: []uint32{1}, MaxOutputTokens: 4, DecodeStrategy: "greedy",
	}); err != nil {
		t.Fatal(err)
	}
	batch, err := l.NextBatch()
	if err != nil {
		t.Fatal(err)
	}
	result := MarkerModel{}.Result(batch)
	first, err := l.ApplyResult(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 {
		t.Fatalf("first result delivered %d rows", len(first))
	}
	second, err := l.ApplyResult(result)
	if err != nil {
		t.Fatalf("duplicate result: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("duplicate result delivered %d rows", len(second))
	}
}

func TestBatchesDoNotMixModelGenerations(t *testing.T) {
	now := time.Unix(650, 0)
	l := testLedger(&now, "batch-g1", "batch-g2")
	requests := []Request{
		{TenantID: "a", RequestID: "one", ModelGeneration: 1, MaxOutputTokens: 2, DecodeStrategy: "greedy"},
		{TenantID: "b", RequestID: "two", ModelGeneration: 2, MaxOutputTokens: 2, DecodeStrategy: "greedy"},
		{TenantID: "c", RequestID: "three", ModelGeneration: 1, MaxOutputTokens: 2, DecodeStrategy: "greedy"},
	}
	for _, req := range requests {
		if err := l.Submit(req); err != nil {
			t.Fatal(err)
		}
	}
	first, err := l.NextBatch()
	if err != nil {
		t.Fatal(err)
	}
	if first.ModelGeneration != 1 || len(first.Rows) != 2 {
		t.Fatalf("first batch = %+v", first)
	}
	second, err := l.NextBatch()
	if err != nil {
		t.Fatal(err)
	}
	if second.ModelGeneration != 2 || len(second.Rows) != 1 {
		t.Fatalf("second batch = %+v", second)
	}
}

func TestInvalidResultDoesNotPartiallyCommit(t *testing.T) {
	now := time.Unix(675, 0)
	l := testLedger(&now, "batch-atomic")
	for _, req := range []Request{
		{TenantID: "a", RequestID: "0", ModelGeneration: 1, MaxOutputTokens: 2, DecodeStrategy: "greedy"},
		{TenantID: "b", RequestID: "1", ModelGeneration: 1, MaxOutputTokens: 2, DecodeStrategy: "greedy"},
	} {
		if err := l.Submit(req); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := l.NextBatch()
	if err != nil {
		t.Fatal(err)
	}
	bad := NewTensorResult(batch)
	bad.Rows[0].Tokens = []uint32{1}
	bad.Rows[0].Done = true
	bad.Rows[1].Tokens = []uint32{1, 2, 3}
	bad.Rows[1].Done = true
	if _, err := l.ApplyResult(bad); !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("over-limit result: got %v", err)
	}

	good := NewTensorResult(batch)
	good.Rows[0].Tokens = []uint32{1}
	good.Rows[0].Done = true
	good.Rows[1].Tokens = []uint32{2}
	good.Rows[1].Done = true
	deliveries, err := l.ApplyResult(good)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 2 {
		t.Fatalf("valid result after rejected result delivered %d rows, want 2", len(deliveries))
	}
}

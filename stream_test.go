package netsec

import (
	"errors"
	"testing"
	"time"
)

func TestStreamKeepsRowIndexAndFreezesCompletedRows(t *testing.T) {
	now := time.Unix(700, 0)
	l := testLedger(&now, "batch-stream")
	for _, req := range []Request{
		{TenantID: "a", RequestID: "0", ModelGeneration: 1, InputTokens: []uint32{1}, MaxOutputTokens: 8, DecodeStrategy: "greedy"},
		{TenantID: "b", RequestID: "1", ModelGeneration: 1, InputTokens: []uint32{2}, MaxOutputTokens: 8, DecodeStrategy: "greedy"},
	} {
		if err := l.Submit(req); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := l.NextBatch()
	if err != nil {
		t.Fatal(err)
	}

	step, err := l.PrepareStreamStep(batch.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	if step.Step != 1 || len(step.FrozenMask) != 2 || step.FrozenMask[0] || step.FrozenMask[1] {
		t.Fatalf("unexpected first step: %+v", step)
	}
	result1 := NewStreamResult(step)
	result1.Rows[0].Tokens = []uint32{10}
	result1.Rows[0].Done = true
	result1.Rows[1].Tokens = []uint32{20}
	result1.Rows[1].Done = false
	deliveries, err := l.ApplyStreamResult(result1)
	if err != nil {
		t.Fatalf("step 1: %v", err)
	}
	if len(deliveries) != 2 || deliveries[0].RowIndex != 0 || deliveries[1].RowIndex != 1 {
		t.Fatalf("step 1 deliveries = %+v", deliveries)
	}

	step2, err := l.PrepareStreamStep(batch.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	if step2.Step != 2 || !step2.FrozenMask[0] || step2.FrozenMask[1] || !step2.Rows[0].Frozen {
		t.Fatalf("unexpected second step mask: %+v", step2)
	}
	bad := NewStreamResult(step2)
	bad.Rows[0].Tokens = []uint32{10, 99}
	bad.Rows[0].Done = true
	bad.Rows[1].Tokens = []uint32{20, 21}
	bad.Rows[1].Done = true
	if _, err := l.ApplyStreamResult(bad); !errors.Is(err, ErrFrozenRowModified) {
		t.Fatalf("modified frozen row: got %v, want ErrFrozenRowModified", err)
	}

	// The whole bad step was rejected: neither its tokens nor its step number
	// may have been committed.
	retry, err := l.PrepareStreamStep(batch.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Step != 2 || !sameTokens(retry.Rows[0].OutputTokens, []uint32{10}) ||
		!sameTokens(retry.Rows[1].OutputTokens, []uint32{20}) {
		t.Fatalf("rejected step changed stream state: %+v", retry)
	}

	good := NewStreamResult(retry)
	good.Rows[0].Tokens = []uint32{10}
	good.Rows[0].Done = true
	good.Rows[1].Tokens = []uint32{20, 21}
	good.Rows[1].Done = true
	deliveries, err = l.ApplyStreamResult(good)
	if err != nil {
		t.Fatalf("step 2: %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].RowIndex != 1 || deliveries[0].Step != 2 {
		t.Fatalf("step 2 deliveries = %+v", deliveries)
	}

	// Replaying the exact callback result at the prior step is idempotent.
	replay, err := l.ApplyStreamResult(good)
	if err != nil {
		t.Fatalf("duplicate stream result: %v", err)
	}
	if len(replay) != 0 {
		t.Fatalf("duplicate stream result delivered %d rows", len(replay))
	}
	last, err := l.PrepareStreamStep(batch.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	if !last.FrozenMask[0] || !last.FrozenMask[1] {
		t.Fatalf("completed rows are not fully frozen: %+v", last.FrozenMask)
	}
}

func TestPartialCompletionAppendsWithoutRewriting(t *testing.T) {
	now := time.Unix(800, 0)
	l := testLedger(&now, "batch-partial")
	req := Request{
		TenantID: "a", RequestID: "r", ModelGeneration: 1,
		InputTokens: []uint32{7, 8}, MaxOutputTokens: 4, DecodeStrategy: "greedy",
	}
	if err := l.Submit(req); err != nil {
		t.Fatal(err)
	}
	batch, err := l.NextBatch()
	if err != nil {
		t.Fatal(err)
	}
	full := MarkerOutput(req)
	first := NewTensorResult(batch)
	first.Rows[0].Tokens = []uint32{full[0]}
	first.Rows[0].Done = false
	deliveries, err := l.ApplyResult(first)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 || deliveries[0].Done {
		t.Fatalf("partial delivery = %+v", deliveries)
	}
	second := NewTensorResult(batch)
	second.Rows[0].Tokens = full
	second.Rows[0].Done = true
	deliveries, err = l.ApplyResult(second)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 || !deliveries[0].Done || !sameTokens(deliveries[0].Tokens, full) {
		t.Fatalf("completed delivery = %+v", deliveries)
	}

	rewrite := NewTensorResult(batch)
	rewrite.Rows[0].Tokens = []uint32{999}
	rewrite.Rows[0].Done = true
	if _, err := l.ApplyResult(rewrite); !errors.Is(err, ErrFrozenRowModified) {
		t.Fatalf("rewrite after completion: got %v", err)
	}
}

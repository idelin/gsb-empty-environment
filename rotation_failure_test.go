package netsec

import (
	"context"
	"errors"
	"testing"
)

// 无法解析的引用：批次内外都找不到时保留原令牌并显式标记缺失，不阻断整批发布；
// 待映射发布后再次换代，同一引用应能通过已发布映射回退解析。
func TestRotateBatchKeepsUnresolvableReferenceExplicit(t *testing.T) {
	p := newTestPseudonymizer(t)
	store := NewMemoryStore()

	oldA := mustToken(t, p, testGenV1, "tenant-a", "event_id", "evt-A")
	ghost := mustToken(t, p, testGenV1, "tenant-a", "actor_id", "deleted-actor")
	events := []Event{
		{
			TenantID:  "tenant-a",
			FieldType: "event_id",
			Secret:    bytesOf("evt-A"),
			OldKey:    oldA,
			References: []Reference{
				{Field: "actor", Token: ghost},
			},
		},
	}

	report, err := p.RotateBatch(context.Background(), testGenV1, testGenV2, store, events)
	if err != nil {
		t.Fatalf("rotate with dangling reference: %v", err)
	}
	if len(report.Missing) != 1 || !report.Missing[0].Missing || report.Missing[0].Token != ghost {
		t.Fatalf("want one explicit missing reference, got %+v", report.Missing)
	}
	ref := report.Events[0].References[0]
	if !ref.Missing || ref.Token != ghost || ref.Field != "actor" {
		t.Fatalf("dangling reference must be preserved verbatim with Missing=true, got %+v", ref)
	}

	newA := mustToken(t, p, testGenV2, "tenant-a", "event_id", "evt-A")
	assertStoreHas(t, store, oldA, newA)

	oldActor := mustToken(t, p, testGenV1, "tenant-a", "actor_id", "deleted-actor")
	firstBatch := []Event{
		{TenantID: "tenant-a", FieldType: "actor_id", Secret: bytesOf("deleted-actor"), OldKey: oldActor},
	}
	if _, err := p.RotateBatch(context.Background(), testGenV1, testGenV2, store, firstBatch); err != nil {
		t.Fatalf("publish actor mapping: %v", err)
	}

	oldC := mustToken(t, p, testGenV1, "tenant-a", "event_id", "evt-C")
	secondBatch := []Event{
		{
			TenantID:  "tenant-a",
			FieldType: "event_id",
			Secret:    bytesOf("evt-C"),
			OldKey:    oldC,
			References: []Reference{
				{Field: "actor", Token: oldActor},
			},
		},
	}
	report2, err := p.RotateBatch(context.Background(), testGenV1, testGenV2, store, secondBatch)
	if err != nil {
		t.Fatalf("rotate with published fallback: %v", err)
	}
	if len(report2.Missing) != 0 || report2.Events[0].References[0].Missing {
		t.Fatalf("published mapping should resolve the reference, got %+v", report2.Missing)
	}
}

// 中途存储失败：Commit 注入错误时已发布映射保持原状，重试可成功且结果确定。
func TestRotateBatchAtomicUnderStorageFailure(t *testing.T) {
	p := newTestPseudonymizer(t)
	store := NewFaultInjector(NewMemoryStore())

	oldA := mustToken(t, p, testGenV1, "tenant-a", "event_id", "evt-A")
	oldB := mustToken(t, p, testGenV1, "tenant-a", "event_id", "evt-B")
	events := []Event{
		{
			TenantID:  "tenant-a",
			FieldType: "event_id",
			Secret:    bytesOf("evt-A"),
			OldKey:    oldA,
			References: []Reference{
				{Field: "parent_event", Token: oldB},
			},
		},
		{TenantID: "tenant-a", FieldType: "event_id", Secret: bytesOf("evt-B"), OldKey: oldB},
	}

	store.FailNextCommit()
	report, err := p.RotateBatch(context.Background(), testGenV1, testGenV2, store, events)
	if err == nil {
		t.Fatal("injected storage failure must surface as an error")
	}
	if report != nil {
		t.Fatal("failed rotation must not publish partial results")
	}
	assertStoreEmpty(t, store)

	retry, err := p.RotateBatch(context.Background(), testGenV1, testGenV2, store, events)
	if err != nil {
		t.Fatalf("retry after storage failure: %v", err)
	}
	newA := mustToken(t, p, testGenV2, "tenant-a", "event_id", "evt-A")
	newB := mustToken(t, p, testGenV2, "tenant-a", "event_id", "evt-B")
	assertStoreHas(t, store, oldA, newA)
	assertStoreHas(t, store, oldB, newB)
	if retry.Events[0].NewKey != newA || retry.Events[1].NewKey != newB {
		t.Fatal("retry must yield the same deterministic rewritten keys")
	}
	if retry.Events[0].References[0].Token != newB || retry.Events[0].References[0].Missing {
		t.Fatal("retry must rewrite the reference exactly once and correctly")
	}
}

func TestRotateBatchRejectsInvalidInputs(t *testing.T) {
	p := newTestPseudonymizer(t)

	t.Run("old key does not match secret", func(t *testing.T) {
		store := NewMemoryStore()
		oldA := mustToken(t, p, testGenV1, "tenant-a", "event_id", "evt-A")
		events := []Event{{
			TenantID:  "tenant-a",
			FieldType: "event_id",
			Secret:    bytesOf("attacker-controlled"),
			OldKey:    oldA,
		}}
		if _, err := p.RotateBatch(context.Background(), testGenV1, testGenV2, store, events); !errors.Is(err, ErrKeyMismatch) {
			t.Fatalf("want %v, got %v", ErrKeyMismatch, err)
		}
		assertStoreEmpty(t, store)
	})

	t.Run("identical repeated event maps consistently", func(t *testing.T) {
		store := NewMemoryStore()
		oldA := mustToken(t, p, testGenV1, "tenant-a", "event_id", "evt-A")
		events := []Event{
			{TenantID: "tenant-a", FieldType: "event_id", Secret: bytesOf("evt-A"), OldKey: oldA},
			{TenantID: "tenant-a", FieldType: "event_id", Secret: bytesOf("evt-A"), OldKey: oldA},
		}
		report, err := p.RotateBatch(context.Background(), testGenV1, testGenV2, store, events)
		if err != nil {
			t.Fatalf("identical duplicate events should map consistently: %v", err)
		}
		if len(report.Mapping) != 1 {
			t.Fatalf("want single deduplicated mapping, got %d", len(report.Mapping))
		}
	})

	t.Run("empty batch and same generation are rejected", func(t *testing.T) {
		store := NewMemoryStore()
		if _, err := p.RotateBatch(context.Background(), testGenV1, testGenV2, store, nil); !errors.Is(err, ErrEmptyBatch) {
			t.Fatalf("want %v, got %v", ErrEmptyBatch, err)
		}
		oldA := mustToken(t, p, testGenV1, "tenant-a", "event_id", "evt-A")
		events := []Event{{
			TenantID:  "tenant-a",
			FieldType: "event_id",
			Secret:    bytesOf("evt-A"),
			OldKey:    oldA,
		}}
		if _, err := p.RotateBatch(context.Background(), testGenV1, testGenV1, store, events); err == nil {
			t.Fatal("rotating a generation onto itself must be rejected")
		}
	})

	t.Run("mapping conflict is rejected without partial publish", func(t *testing.T) {
		store := NewMemoryStore()
		oldA := mustToken(t, p, testGenV1, "tenant-a", "event_id", "evt-A")
		first := []Event{{
			TenantID:  "tenant-a",
			FieldType: "event_id",
			Secret:    bytesOf("evt-A"),
			OldKey:    oldA,
		}}
		if _, err := p.RotateBatch(context.Background(), testGenV1, testGenV2, store, first); err != nil {
			t.Fatalf("first rotation: %v", err)
		}
		conflicting := map[string]string{oldA: "pn1.AAAAAAAA"}
		err := store.Commit(context.Background(), conflicting)
		if !errors.Is(err, ErrMappingConflict) {
			t.Fatalf("want %v, got %v", ErrMappingConflict, err)
		}
		assertStoreHas(t, store, oldA, mustToken(t, p, testGenV2, "tenant-a", "event_id", "evt-A"))
	})
}

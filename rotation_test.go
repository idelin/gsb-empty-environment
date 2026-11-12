package netsec

import (
	"context"
	"strings"
	"testing"
)

func mustToken(t *testing.T, p *Pseudonymizer, generation uint32, tenant, field string, secret string) string {
	t.Helper()
	token, err := p.Pseudonymize(generation, tenant, field, bytesOf(secret))
	if err != nil {
		t.Fatalf("pseudonymize %s/%s: %v", tenant, field, err)
	}
	return token.String()
}

func assertStoreHas(t *testing.T, store MappingStore, oldKey, wantNew string) {
	t.Helper()
	err := store.View(context.Background(), func(mapping map[string]string) error {
		got, ok := mapping[oldKey]
		if !ok {
			t.Fatalf("store missing mapping for %s", oldKey)
		}
		if got != wantNew {
			t.Fatalf("mapping target mismatch: got %s want %s", got, wantNew)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("view store: %v", err)
	}
}

func assertStoreEmpty(t *testing.T, store MappingStore) {
	t.Helper()
	err := store.View(context.Background(), func(mapping map[string]string) error {
		if len(mapping) != 0 {
			t.Fatalf("store must remain untouched, has %d entries: %v", len(mapping), mapping)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("view store: %v", err)
	}
}

// 跨租户相同邮箱：换代后两侧仍各自可关联，但彼此隔离，且映射表不含明文。
func TestRotateBatchCrossTenantSameValueIsolation(t *testing.T) {
	p := newTestPseudonymizer(t)
	store := NewMemoryStore()
	const sharedEmail = "shared@example.com"

	oldA := mustToken(t, p, testGenV1, "tenant-a", "email", sharedEmail)
	oldB := mustToken(t, p, testGenV1, "tenant-b", "email", sharedEmail)
	events := []Event{
		{TenantID: "tenant-a", FieldType: "email", Secret: bytesOf(sharedEmail), OldKey: oldA},
		{TenantID: "tenant-b", FieldType: "email", Secret: bytesOf(sharedEmail), OldKey: oldB},
	}

	report, err := p.RotateBatch(context.Background(), testGenV1, testGenV2, store, events)
	if err != nil {
		t.Fatalf("rotate batch: %v", err)
	}
	if len(report.Mapping) != 2 {
		t.Fatalf("want 2 mappings, got %d", len(report.Mapping))
	}

	newA := mustToken(t, p, testGenV2, "tenant-a", "email", sharedEmail)
	newB := mustToken(t, p, testGenV2, "tenant-b", "email", sharedEmail)
	if newA == newB {
		t.Fatal("new generation must keep tenants isolated")
	}
	assertStoreHas(t, store, oldA, newA)
	assertStoreHas(t, store, oldB, newB)

	if report.Events[0].NewKey != newA || report.Events[1].NewKey != newB {
		t.Fatal("rewritten event keys must match freshly derived generation-2 tokens")
	}

	err = store.View(context.Background(), func(mapping map[string]string) error {
		for oldToken, newToken := range mapping {
			if _, err := ParseToken(oldToken); err != nil {
				return err
			}
			if _, err := ParseToken(newToken); err != nil {
				return err
			}
			if strings.Contains(oldToken+newToken, sharedEmail) ||
				strings.Contains(oldToken+newToken, "shared") ||
				strings.Contains(oldToken+newToken, "@") {
				t.Fatal("raw secret must never be persisted into the mapping table")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("mapping table integrity: %v", err)
	}
}

// 循环引用：A->B、B->A 与自引用，两遍解析必须全部重写到新代次。
func TestRotateBatchResolvesCircularReferences(t *testing.T) {
	p := newTestPseudonymizer(t)
	store := NewMemoryStore()

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
				{Field: "self", Token: oldA},
			},
		},
		{
			TenantID:  "tenant-a",
			FieldType: "event_id",
			Secret:    bytesOf("evt-B"),
			OldKey:    oldB,
			References: []Reference{
				{Field: "parent_event", Token: oldA},
			},
		},
	}

	report, err := p.RotateBatch(context.Background(), testGenV1, testGenV2, store, events)
	if err != nil {
		t.Fatalf("rotate circular batch: %v", err)
	}
	if len(report.Missing) != 0 {
		t.Fatalf("circular references must resolve, missing=%v", report.Missing)
	}

	newA := mustToken(t, p, testGenV2, "tenant-a", "event_id", "evt-A")
	newB := mustToken(t, p, testGenV2, "tenant-a", "event_id", "evt-B")
	if report.Events[0].NewKey != newA || report.Events[1].NewKey != newB {
		t.Fatal("event keys must be rewritten to generation-2 tokens")
	}
	if report.Events[0].References[0].Token != newB ||
		report.Events[0].References[1].Token != newA ||
		report.Events[1].References[0].Token != newA {
		t.Fatalf("circular reference rewrite incorrect: %+v", report.Events)
	}
	for _, rotated := range report.Events {
		for _, ref := range rotated.References {
			if ref.Missing {
				t.Fatalf("reference %s unexpectedly missing", ref.Field)
			}
			parsed, err := ParseToken(ref.Token)
			if err != nil {
				t.Fatalf("rewritten reference unparseable: %v", err)
			}
			if parsed.Generation != testGenV2 {
				t.Fatalf("reference %s still on old generation", ref.Field)
			}
		}
	}
	assertStoreHas(t, store, oldA, newA)
	assertStoreHas(t, store, oldB, newB)
}

package netsec

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"strings"
	"testing"
	"time"
)

type stubResolver struct {
	grants map[Principal][]Permission
}

func (r *stubResolver) PermissionsFor(_ context.Context, p Principal) ([]Permission, error) {
	return r.grants[p], nil
}

var (
	testAlice   = Principal{ID: "alice", TenantID: "t1"}
	testBob     = Principal{ID: "bob", TenantID: "t1"}
	testMallory = Principal{ID: "mallory", TenantID: "t2"}
)

func newTestService(t *testing.T) (*Service, *MemoryTaskStore, *MemoryDataSource, *stubResolver) {
	t.Helper()

	resolver := &stubResolver{grants: map[Principal][]Permission{
		testAlice:   {{TenantID: "t1", Dataset: "orders", Constraints: Filters{"region": "east"}}},
		testBob:     {{TenantID: "t1", Dataset: "orders", Constraints: Filters{"region": "east"}}},
		testMallory: {{TenantID: "t2", Dataset: "orders", Constraints: Filters{"region": "north"}}},
	}}

	source := NewMemoryDataSource()
	source.AddTable("t1", "orders", []Row{
		{"order_id": "o1", "region": "east", "segment": "retail"},
		{"order_id": "o2", "region": "east", "segment": "wholesale"},
		{"order_id": "o3", "region": "west", "segment": "retail"},
	})
	source.AddTable("t2", "orders", []Row{
		{"order_id": "m1", "region": "north", "segment": "retail"},
	})

	store := NewMemoryTaskStore()
	master := bytes.Repeat([]byte{0x42}, keySize)
	svc, err := NewService(store, resolver, source, master)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, store, source, resolver
}

// tamperStoredFilter mutates a persisted filter directly, simulating storage
// compromise of the condition.
func tamperStoredFilter(store *MemoryTaskStore, id, key, value string) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.tasks[id].Filters[key] = value
}

func indexOf(values []string, want string) int {
	for i, v := range values {
		if v == want {
			return i
		}
	}
	return -1
}

func stripFirstTwoLines(content []byte) []byte {
	parts := bytes.SplitN(content, []byte("\n"), 3)
	return []byte(parts[2])
}

func TestConditionRewriteRejected(t *testing.T) {
	ctx := context.Background()
	svc, store, _, _ := newTestService(t)

	t.Run("rewrite at creation", func(t *testing.T) {
		_, err := svc.CreateTask(ctx, testAlice, "orders", Filters{"region": "west"})
		if !errors.Is(err, ErrConditionRewritten) {
			t.Fatalf("want ErrConditionRewritten, got %v", err)
		}
	})

	t.Run("rewrite after creation via storage tamper", func(t *testing.T) {
		task, err := svc.CreateTask(ctx, testAlice, "orders", nil)
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		tamperStoredFilter(store, task.ID, "region", "west")

		_, err = svc.ExecuteTask(ctx, testAlice, task.ID)
		if !errors.Is(err, ErrFilterTampered) {
			t.Fatalf("want ErrFilterTampered, got %v", err)
		}

		got, err := store.Get(ctx, task.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Status != StatusPending {
			t.Fatalf("failed run must release claim, status=%s", got.Status)
		}
	})
}

func TestCrossTenantRejected(t *testing.T) {
	ctx := context.Background()
	svc, store, source, _ := newTestService(t)

	task, err := svc.CreateTask(ctx, testAlice, "orders", nil)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	t.Run("foreign tenant principal cannot execute task", func(t *testing.T) {
		_, err := svc.ExecuteTask(ctx, testMallory, task.ID)
		if !errors.Is(err, ErrCrossTenant) {
			t.Fatalf("want ErrCrossTenant, got %v", err)
		}
	})

	t.Run("rejected attempt does not consume the task", func(t *testing.T) {
		got, err := store.Get(ctx, task.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Status != StatusPending {
			t.Fatalf("task must remain pending, status=%s", got.Status)
		}
	})

	t.Run("data source enforces tenant isolation", func(t *testing.T) {
		if _, _, err := source.Query(ctx, "t3", "orders", nil); !errors.Is(err, ErrCrossTenant) {
			t.Fatalf("want ErrCrossTenant from data source, got %v", err)
		}
		_, rows, err := source.Query(ctx, "t1", "orders", Filters{"region": "east"})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		for _, row := range rows {
			if row["region"] != "east" {
				t.Fatalf("filter not applied: %v", row)
			}
		}
	})
}

func TestTaskReuseRejected(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newTestService(t)

	task, err := svc.CreateTask(ctx, testAlice, "orders", nil)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	t.Run("other principal in same tenant cannot reuse", func(t *testing.T) {
		_, err := svc.ExecuteTask(ctx, testBob, task.ID)
		if !errors.Is(err, ErrTaskNotOwned) {
			t.Fatalf("want ErrTaskNotOwned, got %v", err)
		}
	})

	t.Run("single use: second execution rejected", func(t *testing.T) {
		if _, err := svc.ExecuteTask(ctx, testAlice, task.ID); err != nil {
			t.Fatalf("first execution: %v", err)
		}
		_, err := svc.ExecuteTask(ctx, testAlice, task.ID)
		if !errors.Is(err, ErrTaskReused) {
			t.Fatalf("want ErrTaskReused, got %v", err)
		}
	})

	t.Run("unknown identifier", func(t *testing.T) {
		if _, err := svc.ExecuteTask(ctx, testAlice, "deadbeef"); !errors.Is(err, ErrTaskNotFound) {
			t.Fatalf("want ErrTaskNotFound, got %v", err)
		}
	})
}

func TestLegitimateExportOnlyAuthorizedRows(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newTestService(t)

	// No explicit region filter: the service injects region=east from the
	// permission instead of trusting the caller to narrow the scope.
	task, err := svc.CreateTask(ctx, testAlice, "orders", nil)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.Filters["region"] != "east" {
		t.Fatalf("permission constraint not injected: %v", task.Filters)
	}

	result, err := svc.ExecuteTask(ctx, testAlice, task.ID)
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	if result.Rows != 2 {
		t.Fatalf("want 2 authorized rows, got %d", result.Rows)
	}

	plaintext, err := svc.VerifyExport(result.Envelope, task)
	if err != nil {
		t.Fatalf("VerifyExport: %v", err)
	}

	csvBody := stripFirstTwoLines(plaintext)
	records, err := csv.NewReader(bytes.NewReader(csvBody)).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	// header + 2 data rows.
	if len(records) != 3 {
		t.Fatalf("want 3 CSV records, got %d", len(records))
	}
	header := records[0]
	regionCol := indexOf(header, "region")
	orderCol := indexOf(header, "order_id")
	seen := map[string]bool{}
	for _, rec := range records[1:] {
		if rec[regionCol] != "east" {
			t.Fatalf("unauthorized row exported: %v", rec)
		}
		seen[rec[orderCol]] = true
	}
	if !seen["o1"] || !seen["o2"] || seen["o3"] {
		t.Fatalf("exported wrong row set: %v", seen)
	}

	got, err := svc.store.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusConsumed {
		t.Fatalf("task not consumed: %s", got.Status)
	}
}

func TestScopeRecomputedBeforeExecution(t *testing.T) {
	ctx := context.Background()
	svc, _, _, resolver := newTestService(t)

	task, err := svc.CreateTask(ctx, testAlice, "orders", nil)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Permission tightened after task creation: the new mandatory segment
	// constraint makes the stored scope too wide.
	resolver.grants[testAlice] = []Permission{{
		TenantID:    "t1",
		Dataset:     "orders",
		Constraints: Filters{"region": "east", "segment": "retail"},
	}}

	_, err = svc.ExecuteTask(ctx, testAlice, task.ID)
	if !errors.Is(err, ErrScopeExceeded) {
		t.Fatalf("want ErrScopeExceeded, got %v", err)
	}
}

func TestWatermarkAndEncryption(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newTestService(t)

	task, err := svc.CreateTask(ctx, testAlice, "orders", nil)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	result, err := svc.ExecuteTask(ctx, testAlice, task.ID)
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	t.Run("envelope structure and no plaintext leakage", func(t *testing.T) {
		blob := result.Envelope
		if string(blob[:len(EnvelopeMagic)]) != EnvelopeMagic {
			t.Fatalf("missing envelope magic")
		}
		if bytes.Contains(blob, []byte("alice")) || bytes.Contains(blob, []byte("order_id")) {
			t.Fatalf("plaintext metadata leaked into envelope")
		}
		plaintext, err := svc.DecryptExport(blob)
		if err != nil {
			t.Fatalf("DecryptExport: %v", err)
		}
		if !strings.HasPrefix(string(plaintext), watermarkPrefix) {
			t.Fatalf("missing visible watermark:\n%s", plaintext)
		}
	})

	t.Run("watermark attributes and scope", func(t *testing.T) {
		wm := result.Watermark
		if wm.PrincipalID != "alice" || wm.TenantID != "t1" || wm.Dataset != "orders" {
			t.Fatalf("watermark attribution wrong: %+v", wm)
		}
		if !strings.Contains(wm.Statement, `"region":"east"`) {
			t.Fatalf("watermark missing scope: %s", wm.Statement)
		}
		if wm.Tag == "" {
			t.Fatal("watermark HMAC tag empty")
		}
	})

	t.Run("tampered ciphertext rejected", func(t *testing.T) {
		bad := bytes.Clone(result.Envelope)
		bad[len(bad)-1] ^= 0xFF
		if _, err := svc.DecryptExport(bad); !errors.Is(err, ErrBadEnvelope) {
			t.Fatalf("want ErrBadEnvelope, got %v", err)
		}
	})

	t.Run("wrong master key rejected", func(t *testing.T) {
		other := bytes.Repeat([]byte{0x07}, keySize)
		if _, err := Open(other, result.Envelope); !errors.Is(err, ErrBadEnvelope) {
			t.Fatalf("want ErrBadEnvelope, got %v", err)
		}
	})

	t.Run("forged watermark rejected", func(t *testing.T) {
		plaintext, err := svc.DecryptExport(result.Envelope)
		if err != nil {
			t.Fatalf("DecryptExport: %v", err)
		}
		forged := append([]byte(watermarkPrefix+"forged\n"), stripFirstTwoLines(plaintext)...)
		resealed, err := Seal(nil, svc.master, forged)
		if err != nil {
			t.Fatalf("Seal: %v", err)
		}
		if _, err := svc.VerifyExport(resealed, task); !errors.Is(err, ErrWatermarkMismatch) {
			t.Fatalf("want ErrWatermarkMismatch, got %v", err)
		}
	})
}

func TestExpiredTaskRejected(t *testing.T) {
	ctx := context.Background()
	master := bytes.Repeat([]byte{0x55}, keySize)
	store := NewMemoryTaskStore()
	resolver := &stubResolver{grants: map[Principal][]Permission{
		testAlice: {{TenantID: "t1", Dataset: "orders", Constraints: Filters{"region": "east"}}},
	}}
	source := NewMemoryDataSource()
	svc, err := NewService(store, resolver, source, master, WithTaskTTL(time.Millisecond))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	task, err := svc.CreateTask(ctx, testAlice, "orders", nil)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := svc.ExecuteTask(ctx, testAlice, task.ID); !errors.Is(err, ErrTaskExpired) {
		t.Fatalf("want ErrTaskExpired, got %v", err)
	}
}

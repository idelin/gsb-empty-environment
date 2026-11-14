package netsec

import (
	"errors"
	"testing"
)

func principalWith(perms ...Permission) Principal {
	permSet := make(map[Permission]bool, len(perms))
	for _, p := range perms {
		permSet[p] = true
	}
	return Principal{ID: "user-1", TenantID: "tenant-a", Permissions: permSet}
}

func seedRecord(t *testing.T, store *MemoryStore, rec Record) StoredRecord {
	t.Helper()
	stored, err := store.Save(SaveInput{
		TenantID:    rec.TenantID,
		BusinessKey: rec.BusinessKey,
		OwnerID:     rec.OwnerID,
		Status:      rec.Status,
		Fields:      rec.Fields,
		ActorID:     "seeder",
	})
	if err != nil {
		t.Fatalf("seed save failed: %v", err)
	}
	return stored
}

func assertRejected(t *testing.T, report ImportReport, index int, sentinel error) {
	t.Helper()
	item := report.Results[index]
	if !item.Rejected || item.Applied {
		t.Fatalf("row %d: expected rejection, got applied=%v rejected=%v", index, item.Applied, item.Rejected)
	}
	if item.Violation == nil || !errors.Is(*item.Violation, sentinel) {
		t.Fatalf("row %d: expected violation %v, got %+v", index, sentinel, item.Violation)
	}
}

// TestSameNameOverwriteWithoutPermission: an attacker submitting a row with
// an existing business key must not be able to overwrite someone else's data.
func TestSameNameOverwriteWithoutPermission(t *testing.T) {
	store := NewMemoryStore()
	original := seedRecord(t, store, Record{
		TenantID: "tenant-a", BusinessKey: "invoice-1",
		OwnerID: "victim", Status: StatusDraft,
		Fields: map[string]string{"amount": "100"},
	})

	merger := NewMerger(store)
	attacker := principalWith(PermCreate)
	report := merger.Import(ImportRequest{
		Principal: attacker,
		Records: []Record{{
			TenantID: "tenant-a", BusinessKey: "invoice-1",
			OwnerID: "attacker", Status: StatusDraft,
			Fields: map[string]string{"amount": "9999"},
		}},
	})

	assertRejected(t, report, 0, ErrPermissionDenied)

	got, err := store.Get("tenant-a", "invoice-1")
	if err != nil {
		t.Fatalf("read back failed: %v", err)
	}
	if got.OwnerID != "victim" || got.Fields["amount"] != "100" || got.Version != original.Version {
		t.Fatalf("victim record was modified: %+v", got)
	}
	versions, err := store.Versions("tenant-a", "invoice-1")
	if err != nil || len(versions) != 0 {
		t.Fatalf("expected no archived versions after rejected overwrite, got %d (err=%v)", len(versions), err)
	}
	if report.AppliedCount() != 0 {
		t.Fatalf("rejected batch must not contain applied writes: %+v", report)
	}
}

// TestSameNameOverwriteWithPermissionArchivesOldVersion: the explicit
// overwrite permission allows the write and the previous revision is kept.
func TestSameNameOverwriteWithPermissionArchivesOldVersion(t *testing.T) {
	store := NewMemoryStore()
	original := seedRecord(t, store, Record{
		TenantID: "tenant-a", BusinessKey: "invoice-2",
		OwnerID: "victim", Status: StatusDraft,
		Fields: map[string]string{"amount": "100"},
	})

	merger := NewMerger(store)
	report := merger.Import(ImportRequest{
		Principal: principalWith(PermCreate, PermOverwrite),
		Records: []Record{{
			TenantID: "tenant-a", BusinessKey: "invoice-2",
			OwnerID: "victim", Status: StatusDraft,
			Fields: map[string]string{"amount": "250"},
		}},
	})

	if !report.OK() {
		t.Fatalf("expected clean import, got %+v", report.Results[0].Violation)
	}
	item := report.Results[0]
	if item.Stored.Version != original.Version+1 {
		t.Fatalf("expected version %d, got %d", original.Version+1, item.Stored.Version)
	}
	if item.ReadBack == nil || !item.ReadBack.Matches || !item.ReadBack.OldVersionArchived {
		t.Fatalf("read-back mismatch: %+v", item.ReadBack)
	}

	versions, err := store.Versions("tenant-a", "invoice-2")
	if err != nil {
		t.Fatalf("versions lookup failed: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("expected one archived revision, got %d", len(versions))
	}
	archived := versions[0].Record
	if archived.Version != original.Version || archived.Fields["amount"] != "100" || archived.OwnerID != "victim" {
		t.Fatalf("archived revision does not preserve old data: %+v", archived)
	}

	current, _ := store.Get("tenant-a", "invoice-2")
	if current.Fields["amount"] != "250" || current.UpdatedBy != "user-1" {
		t.Fatalf("current record not updated as expected: %+v", current)
	}
}

// TestStatusPromotionRequiresPublishPermission: turning a draft into a
// published record is a privilege escalation and needs explicit permission.
func TestStatusPromotionRequiresPublishPermission(t *testing.T) {
	store := NewMemoryStore()
	seedRecord(t, store, Record{
		TenantID: "tenant-a", BusinessKey: "doc-1",
		OwnerID: "user-1", Status: StatusDraft,
	})

	merger := NewMerger(store)

	// Overwrite alone is not enough to change status.
	denied := merger.Import(ImportRequest{
		Principal: principalWith(PermCreate, PermOverwrite),
		Records: []Record{{
			TenantID: "tenant-a", BusinessKey: "doc-1",
			OwnerID: "user-1", Status: StatusPublished,
		}},
	})
	assertRejected(t, denied, 0, ErrPermissionDenied)

	current, _ := store.Get("tenant-a", "doc-1")
	if current.Status != StatusDraft {
		t.Fatalf("draft was promoted without permission: %+v", current)
	}

	// With the publish permission the promotion succeeds and reads back.
	allowed := merger.Import(ImportRequest{
		Principal: principalWith(PermCreate, PermOverwrite, PermPublish),
		Records: []Record{{
			TenantID: "tenant-a", BusinessKey: "doc-1",
			OwnerID: "user-1", Status: StatusPublished,
		}},
	})
	if !allowed.OK() {
		t.Fatalf("expected authorized promotion, got %+v", allowed.Results[0].Violation)
	}
	current, _ = store.Get("tenant-a", "doc-1")
	if current.Status != StatusPublished || current.Version != 2 {
		t.Fatalf("status not promoted correctly: %+v", current)
	}

	// Creating a brand-new record directly in published state also needs
	// the publish permission.
	escalatedCreate := merger.Import(ImportRequest{
		Principal: principalWith(PermCreate),
		Records: []Record{{
			TenantID: "tenant-a", BusinessKey: "doc-2",
			OwnerID: "user-1", Status: StatusPublished,
		}},
	})
	assertRejected(t, escalatedCreate, 0, ErrPermissionDenied)
}

// TestCrossTenantRecordsRejected: rows scoped to another tenant, and
// same-key collisions owned by another tenant, are both refused.
func TestCrossTenantRecordsRejected(t *testing.T) {
	store := NewMemoryStore()
	seedRecord(t, store, Record{
		TenantID: "tenant-b", BusinessKey: "shared-key",
		OwnerID: "tenant-b-owner", Status: StatusDraft,
	})

	merger := NewMerger(store)
	principal := principalWith(PermCreate, PermOverwrite, PermPublish)
	report := merger.Import(ImportRequest{
		Principal: principal,
		Records: []Record{
			{TenantID: "tenant-b", BusinessKey: "shared-key", OwnerID: "attacker", Status: StatusPublished},
			{TenantID: "tenant-a", BusinessKey: "shared-key", OwnerID: "attacker", Status: StatusDraft},
			{TenantID: "tenant-a", BusinessKey: "local-key", OwnerID: "user-1", Status: StatusDraft},
		},
	})

	assertRejected(t, report, 0, ErrCrossTenant)
	assertRejected(t, report, 1, ErrCrossTenant)

	if !report.Results[2].Applied {
		t.Fatalf("authorized local row should be applied: %+v", report.Results[2].Violation)
	}

	foreign, err := store.Get("tenant-b", "shared-key")
	if err != nil || foreign.OwnerID != "tenant-b-owner" || foreign.Status != StatusDraft {
		t.Fatalf("foreign tenant record changed: %+v err=%v", foreign, err)
	}
	if !store.Exists("local-key") {
		t.Fatal("authorized local record was not stored")
	}

	all, _ := store.List()
	if len(all) != 2 {
		t.Fatalf("expected exactly two records, got %d", len(all))
	}
}

// TestDuplicateKeysInBatchRejected: repeated business keys inside one batch
// are rejected so the importer cannot race itself into an overwrite.
func TestDuplicateKeysInBatchRejected(t *testing.T) {
	store := NewMemoryStore()
	merger := NewMerger(store)
	report := merger.Import(ImportRequest{
		Principal: principalWith(PermCreate, PermOverwrite),
		Records: []Record{
			{TenantID: "tenant-a", BusinessKey: "dup", OwnerID: "user-1", Status: StatusDraft, Fields: map[string]string{"v": "1"}},
			{TenantID: "tenant-a", BusinessKey: "dup", OwnerID: "user-1", Status: StatusDraft, Fields: map[string]string{"v": "2"}},
			{TenantID: "tenant-a", BusinessKey: "uniq", OwnerID: "user-1", Status: StatusDraft},
		},
	})

	assertRejected(t, report, 0, ErrDuplicateKey)
	assertRejected(t, report, 1, ErrDuplicateKey)
	if !report.Results[2].Applied {
		t.Fatalf("unique row should still be applied: %+v", report.Results[2].Violation)
	}
	if store.Exists("dup") {
		t.Fatal("duplicate key must not be written at all")
	}
}

// TestLegitimateImportAndReadBack: a fully authorized mixed batch inserts,
// overwrites and reads back with per-row diffs, touching only its own rows.
func TestLegitimateImportAndReadBack(t *testing.T) {
	store := NewMemoryStore()
	existing := seedRecord(t, store, Record{
		TenantID: "tenant-a", BusinessKey: "existing",
		OwnerID: "user-1", Status: StatusDraft,
		Fields: map[string]string{"v": "old"},
	})
	untouched := seedRecord(t, store, Record{
		TenantID: "tenant-a", BusinessKey: "untouched",
		OwnerID: "someone-else", Status: StatusPublished,
		Fields: map[string]string{"v": "frozen"},
	})

	merger := NewMerger(store)
	report := merger.Import(ImportRequest{
		Principal: principalWith(PermCreate, PermOverwrite, PermPublish),
		Records: []Record{
			{TenantID: "tenant-a", BusinessKey: "existing", OwnerID: "user-1", Status: StatusPublished, Fields: map[string]string{"v": "new"}},
			{TenantID: "tenant-a", BusinessKey: "brand-new", OwnerID: "user-1", Status: StatusDraft, Fields: map[string]string{"v": "new"}},
		},
	})

	if !report.OK() {
		for _, r := range report.Results {
			if r.Violation != nil {
				t.Errorf("unexpected rejection: %v", *r.Violation)
			}
		}
		t.FailNow()
	}
	if report.AppliedCount() != 2 || report.RejectedCount() != 0 {
		t.Fatalf("unexpected counts: applied=%d rejected=%d", report.AppliedCount(), report.RejectedCount())
	}

	overwrite := report.Results[0]
	if overwrite.Stored.Version != existing.Version+1 {
		t.Fatalf("overwrite version wrong: %+v", overwrite.Stored)
	}
	if overwrite.ReadBack == nil || !overwrite.ReadBack.Matches || len(overwrite.ReadBack.Diffs) != 0 {
		t.Fatalf("overwrite read-back failed: %+v", overwrite.ReadBack)
	}

	insert := report.Results[1]
	if insert.Stored.Version != 1 || insert.ReadBack == nil || !insert.ReadBack.Matches {
		t.Fatalf("insert read-back failed: %+v", insert.ReadBack)
	}

	stillFrozen, _ := store.Get("tenant-a", "untouched")
	if !recordsEqual(stillFrozen, untouched) {
		t.Fatalf("unauthorized record changed: before=%+v after=%+v", untouched, stillFrozen)
	}

	versions, _ := store.Versions("tenant-a", "existing")
	if len(versions) != 1 || versions[0].Record.Fields["v"] != "old" {
		t.Fatalf("old revision not retained: %+v", versions)
	}
}

// tamperStore wraps MemoryStore and mutates a saved record after the write,
// letting the test prove read-back verification catches store-level drift.
type tamperStore struct {
	*MemoryStore
	tamperKey storeKey
}

func (s *tamperStore) Save(in SaveInput) (StoredRecord, error) {
	stored, err := s.MemoryStore.Save(in)
	if err != nil {
		return stored, err
	}
	if (storeKey{tenant: in.TenantID, key: in.BusinessKey}) == s.tamperKey {
		stored.Fields["amount"] = "tampered"
		s.MemoryStore.mu.Lock()
		s.MemoryStore.current[s.tamperKey] = stored
		s.MemoryStore.mu.Unlock()
	}
	return stored, nil
}

// TestReadBackDetectsDrift: if the persisted state diverges from the
// authorized write, read-back reports the diff and marks verification false.
func TestReadBackDetectsDrift(t *testing.T) {
	base := NewMemoryStore()
	store := &tamperStore{
		MemoryStore: base,
		tamperKey:   storeKey{tenant: "tenant-a", key: "invoice-9"},
	}
	merger := NewMerger(store)

	report := merger.Import(ImportRequest{
		Principal: principalWith(PermCreate),
		Records: []Record{{
			TenantID: "tenant-a", BusinessKey: "invoice-9",
			OwnerID: "user-1", Status: StatusDraft,
			Fields: map[string]string{"amount": "42"},
		}},
	})

	if report.AppliedCount() != 1 {
		t.Fatalf("row should have been written, report=%+v", report)
	}
	if report.ReadBackVerified {
		t.Fatal("read-back must fail when the stored data was tampered with")
	}
	rb := report.Results[0].ReadBack
	if rb == nil || rb.Matches {
		t.Fatalf("expected mismatching read-back, got %+v", rb)
	}
	foundDiff := false
	for _, diff := range rb.Diffs {
		if diff.Field == "fields.amount" && diff.Want == "42" && diff.Got == "tampered" {
			foundDiff = true
		}
	}
	if !foundDiff {
		t.Fatalf("expected fields.amount diff, got %+v", rb.Diffs)
	}
}

// collateralTamperStore mutates an unrelated record during an authorized
// save, exercising the snapshot check that protects untouched rows.
type collateralTamperStore struct {
	*MemoryStore
	victimKey storeKey
}

func (s *collateralTamperStore) Save(in SaveInput) (StoredRecord, error) {
	stored, err := s.MemoryStore.Save(in)
	if err != nil {
		return stored, err
	}
	if (storeKey{tenant: in.TenantID, key: in.BusinessKey}) != s.victimKey {
		s.MemoryStore.mu.Lock()
		victim := s.MemoryStore.current[s.victimKey]
		victim.Fields["v"] = "collateral-damage"
		s.MemoryStore.current[s.victimKey] = victim
		s.MemoryStore.mu.Unlock()
	}
	return stored, nil
}

func TestReadBackDetectsUnauthorizedRecordChange(t *testing.T) {
	base := NewMemoryStore()
	victim := seedRecord(t, base, Record{
		TenantID: "tenant-a", BusinessKey: "victim",
		OwnerID: "someone-else", Status: StatusDraft,
		Fields: map[string]string{"v": "original"},
	})
	store := &collateralTamperStore{
		MemoryStore: base,
		victimKey:   storeKey{tenant: "tenant-a", key: "victim"},
	}
	merger := NewMerger(store)

	report := merger.Import(ImportRequest{
		Principal: principalWith(PermCreate),
		Records: []Record{{
			TenantID: "tenant-a", BusinessKey: "new-row",
			OwnerID: "user-1", Status: StatusDraft,
		}},
	})

	if report.AppliedCount() != 1 {
		t.Fatalf("authorized row should be applied: %+v", report.Results)
	}
	if report.ReadBackVerified {
		t.Fatal("verification must fail when an unrelated record changed during the import")
	}

	changed, _ := store.Get(victim.TenantID, victim.BusinessKey)
	if changed.Fields["v"] == "original" {
		t.Fatal("test setup failed: victim record was not tampered with")
	}
}

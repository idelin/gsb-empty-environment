package netsec

import (
	"strconv"
	"time"
)

// ImportRequest is one batch import invocation.
type ImportRequest struct {
	Principal Principal
	Records   []Record
}

// FieldDiff captures one mismatched field between the intended write and the
// record read back from the store.
type FieldDiff struct {
	Field string
	Want  string
	Got   string
}

// ReadBackReport is the post-import verification of one applied row.
type ReadBackReport struct {
	TenantID    string
	BusinessKey string
	Version     int
	Matches     bool
	Diffs       []FieldDiff
	// OldVersionArchived is true when an overwrite left the previous
	// revision in the version history.
	OldVersionArchived bool
}

// ItemResult is the outcome for a single import row.
type ItemResult struct {
	Index     int
	Record    Record
	Applied   bool
	Rejected  bool
	Violation *Violation
	Stored    *StoredRecord
	ReadBack  *ReadBackReport
}

// ImportReport summarizes a whole batch.
type ImportReport struct {
	Results []ItemResult
	// ReadBackVerified is true only when every applied row reads back
	// exactly as written, including archived revisions.
	ReadBackVerified bool
}

// AppliedCount returns the number of rows that were written.
func (r ImportReport) AppliedCount() int {
	n := 0
	for _, item := range r.Results {
		if item.Applied {
			n++
		}
	}
	return n
}

// RejectedCount returns the number of rows that were refused.
func (r ImportReport) RejectedCount() int {
	n := 0
	for _, item := range r.Results {
		if item.Rejected {
			n++
		}
	}
	return n
}

// OK reports whether every row was applied and read back cleanly.
func (r ImportReport) OK() bool {
	return r.RejectedCount() == 0 && r.ReadBackVerified
}

// Merger applies import batches with tenant scoping, explicit overwrite
// authorization and post-write read-back verification.
type Merger struct {
	store Store
	now   func() time.Time
}

// NewMerger builds a Merger backed by the given store.
func NewMerger(store Store) *Merger {
	return &Merger{
		store: store,
		now:   time.Now,
	}
}

func reject(index int, rec Record, code error, detail string) ItemResult {
	return ItemResult{
		Index:    index,
		Record:   rec,
		Rejected: true,
		Violation: &Violation{
			Index:       index,
			TenantID:    rec.TenantID,
			BusinessKey: rec.BusinessKey,
			Code:        code,
			Detail:      detail,
		},
	}
}

// Import validates and applies a batch. Rows are validated independently
// so a single malicious or malformed row does not block authorized rows,
// but duplicate keys inside the batch reject all rows sharing the key.
func (m *Merger) Import(req ImportRequest) ImportReport {
	principal := req.Principal
	results := make([]ItemResult, len(req.Records))

	// Snapshot current records before any write so read-back can prove the
	// import only changed the rows the principal was authorized to touch.
	before, _ := m.store.List()

	// Phase 1: structural validation and in-batch duplicate detection.
	seen := make(map[string]int, len(req.Records))
	duplicates := make(map[int]bool)
	for i, rec := range req.Records {
		switch {
		case rec.BusinessKey == "":
			results[i] = reject(i, rec, ErrInvalidRecord, "business key is required")
			continue
		case !rec.Status.Valid():
			results[i] = reject(i, rec, ErrInvalidRecord, "unknown status "+string(rec.Status))
			continue
		case rec.TenantID == "":
			results[i] = reject(i, rec, ErrCrossTenant, "record tenant is required")
			continue
		case rec.TenantID != principal.TenantID:
			results[i] = reject(i, rec, ErrCrossTenant,
				"principal tenant "+principal.TenantID+" cannot write tenant "+rec.TenantID)
			continue
		}
		if first, ok := seen[rec.BusinessKey]; ok {
			duplicates[i] = true
			duplicates[first] = true
		} else {
			seen[rec.BusinessKey] = i
		}
	}
	for i := range duplicates {
		results[i] = reject(i, req.Records[i], ErrDuplicateKey,
			"business key appears more than once in the same batch")
	}

	// Phase 2: per-row authorization and writes.
	for i, rec := range req.Records {
		if results[i].Rejected {
			continue
		}
		existing, err := m.store.Get(rec.TenantID, rec.BusinessKey)
		switch {
		case err == nil:
			if !principal.Has(PermOverwrite) {
				results[i] = reject(i, rec, ErrPermissionDenied,
					"overwriting existing key requires permission "+string(PermOverwrite))
				continue
			}
			if rec.Status != existing.Status && !principal.Has(PermPublish) {
				results[i] = reject(i, rec, ErrPermissionDenied,
					"changing status requires permission "+string(PermPublish))
				continue
			}
		case err == ErrNotFound:
			if !principal.Has(PermCreate) {
				results[i] = reject(i, rec, ErrPermissionDenied,
					"creating a new key requires permission "+string(PermCreate))
				continue
			}
			if rec.Status != StatusDraft && !principal.Has(PermPublish) {
				results[i] = reject(i, rec, ErrPermissionDenied,
					"creating a record in status "+string(rec.Status)+" requires permission "+string(PermPublish))
				continue
			}
			// A key that only exists in another tenant must never be
			// hijacked by writing a same-named row in this tenant.
			if m.store.Exists(rec.BusinessKey) {
				results[i] = reject(i, rec, ErrCrossTenant,
					"business key already exists in another tenant")
				continue
			}
		default:
			results[i] = reject(i, rec, ErrInvalidRecord, "store lookup failed: "+err.Error())
			continue
		}

		stored, saveErr := m.store.Save(SaveInput{
			TenantID:    rec.TenantID,
			BusinessKey: rec.BusinessKey,
			OwnerID:     rec.OwnerID,
			Status:      rec.Status,
			Fields:      rec.Fields,
			ActorID:     principal.ID,
		})
		if saveErr != nil {
			results[i] = reject(i, rec, ErrInvalidRecord, saveErr.Error())
			continue
		}
		results[i] = ItemResult{
			Index:   i,
			Record:  rec,
			Applied: true,
			Stored:  &stored,
		}
	}

	// Phase 3: read back every write and verify nothing else changed.
	report := ImportReport{Results: results}
	report.ReadBackVerified = m.verifyReadBack(results, before)
	return report
}

func (m *Merger) verifyReadBack(results []ItemResult, before []StoredRecord) bool {
	verified := true
	touched := make(map[storeKey]bool)

	for i := range results {
		item := &results[i]
		if !item.Applied {
			continue
		}
		rec := item.Record
		got, err := m.store.Get(rec.TenantID, rec.BusinessKey)
		if err != nil {
			item.ReadBack = &ReadBackReport{
				TenantID:    rec.TenantID,
				BusinessKey: rec.BusinessKey,
				Matches:     false,
				Diffs:       []FieldDiff{{Field: "<readback>", Want: "stored record", Got: err.Error()}},
			}
			verified = false
			continue
		}

		diffs := compareRecord(rec, got)
		rb := &ReadBackReport{
			TenantID:    rec.TenantID,
			BusinessKey: rec.BusinessKey,
			Version:     got.Version,
			Matches:     len(diffs) == 0,
			Diffs:       diffs,
		}

		if item.Stored != nil && item.Stored.Version > 1 {
			versions, err := m.store.Versions(rec.TenantID, rec.BusinessKey)
			if err != nil || len(versions) == 0 {
				rb.Matches = false
				rb.Diffs = append(rb.Diffs, FieldDiff{
					Field: "<version-history>",
					Want:  "previous revision archived",
					Got:   "no archived revision",
				})
			} else {
				last := versions[len(versions)-1]
				rb.OldVersionArchived = last.Record.Version == item.Stored.Version-1
				if !rb.OldVersionArchived {
					rb.Matches = false
					rb.Diffs = append(rb.Diffs, FieldDiff{
						Field: "<version-history>",
						Want:  "archived revision " + strconv.Itoa(item.Stored.Version-1),
						Got:   "archived revision " + strconv.Itoa(last.Record.Version),
					})
				}
			}
		}

		if !rb.Matches {
			verified = false
		}
		item.ReadBack = rb
		touched[storeKey{rec.TenantID, rec.BusinessKey}] = true
	}

	after, err := m.store.List()
	if err != nil {
		return false
	}
	if !snapshotUnchanged(before, after, touched) {
		verified = false
	}
	return verified
}

func compareRecord(want Record, got StoredRecord) []FieldDiff {
	var diffs []FieldDiff
	if got.TenantID != want.TenantID {
		diffs = append(diffs, FieldDiff{"tenant", want.TenantID, got.TenantID})
	}
	if got.BusinessKey != want.BusinessKey {
		diffs = append(diffs, FieldDiff{"business_key", want.BusinessKey, got.BusinessKey})
	}
	if got.OwnerID != want.OwnerID {
		diffs = append(diffs, FieldDiff{"owner", want.OwnerID, got.OwnerID})
	}
	if got.Status != want.Status {
		diffs = append(diffs, FieldDiff{"status", string(want.Status), string(got.Status)})
	}

	keys := make(map[string]struct{}, len(want.Fields)+len(got.Fields))
	for k := range want.Fields {
		keys[k] = struct{}{}
	}
	for k := range got.Fields {
		keys[k] = struct{}{}
	}
	for k := range keys {
		if got.Fields[k] != want.Fields[k] {
			diffs = append(diffs, FieldDiff{Field: "fields." + k, Want: want.Fields[k], Got: got.Fields[k]})
		}
	}
	return diffs
}

func snapshotUnchanged(before, after []StoredRecord, touched map[storeKey]bool) bool {
	afterByKey := make(map[storeKey]StoredRecord, len(after))
	for _, rec := range after {
		afterByKey[storeKey{rec.TenantID, rec.BusinessKey}] = rec
	}
	for _, old := range before {
		if touched[storeKey{old.TenantID, old.BusinessKey}] {
			continue
		}
		// Every pre-existing key that the import did not touch must still
		// exist and be byte-for-byte identical. New keys are allowed, since
		// authorized inserts legitimately grow the record set.
		current, ok := afterByKey[storeKey{old.TenantID, old.BusinessKey}]
		if !ok || !recordsEqual(old, current) {
			return false
		}
	}
	return true
}

func recordsEqual(a, b StoredRecord) bool {
	if a.ID != b.ID || a.Version != b.Version || a.TenantID != b.TenantID ||
		a.BusinessKey != b.BusinessKey || a.OwnerID != b.OwnerID ||
		a.Status != b.Status || a.UpdatedBy != b.UpdatedBy || !a.UpdatedAt.Equal(b.UpdatedAt) {
		return false
	}
	if len(a.Fields) != len(b.Fields) {
		return false
	}
	for key, value := range a.Fields {
		if b.Fields[key] != value {
			return false
		}
	}
	return true
}

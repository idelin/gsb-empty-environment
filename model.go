package netsec

import "time"

// Status is the lifecycle state of a record.
type Status string

const (
	StatusDraft     Status = "draft"
	StatusPublished Status = "published"
)

var statusRank = map[Status]int{
	StatusDraft:     0,
	StatusPublished: 1,
}

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	_, ok := statusRank[s]
	return ok
}

// Rank returns the ordering of statuses; a higher rank means a more
// privileged state. It returns -1 for unknown statuses.
func (s Status) Rank() int {
	if rank, ok := statusRank[s]; ok {
		return rank
	}
	return -1
}

// Permission is an explicit capability granted to a principal.
type Permission string

const (
	// PermCreate allows inserting a record under a new business key.
	PermCreate Permission = "record:create"
	// PermOverwrite allows replacing an existing record (same business key).
	PermOverwrite Permission = "record:overwrite"
	// PermPublish allows any status change, including promoting a draft to
	// published or creating a record directly in a non-draft state.
	PermPublish Permission = "record:publish"
)

// Principal is the authenticated identity driving an import.
type Principal struct {
	ID          string
	TenantID    string
	Permissions map[Permission]bool
}

// Has reports whether the principal holds the given permission.
func (p Principal) Has(perm Permission) bool {
	return p.Permissions != nil && p.Permissions[perm]
}

// Record is one incoming import row. It is addressed by the tuple
// (tenant, business key).
type Record struct {
	TenantID    string
	BusinessKey string
	OwnerID     string
	Status      Status
	Fields      map[string]string
}

// StoredRecord is the persisted representation of an import row.
type StoredRecord struct {
	ID          int64
	TenantID    string
	BusinessKey string
	OwnerID     string
	Status      Status
	Fields      map[string]string
	Version     int
	UpdatedBy   string
	UpdatedAt   time.Time
}

// RecordVersion is an archived revision retained when a record is
// overwritten, giving an audit trail of the pre-import state.
type RecordVersion struct {
	TenantID    string
	BusinessKey string
	Record      StoredRecord
	ArchivedBy  string
	ArchivedAt  time.Time
}

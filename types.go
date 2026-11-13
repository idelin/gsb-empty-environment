package netsec

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// InodeID identifies an inode number. An inode number may be reused only after
// the previous generation has been released.
type InodeID uint64

// InodeGeneration identifies one incarnation of an inode number.
type InodeGeneration uint64

// DirectoryID identifies a directory.
type DirectoryID uint64

// DirectoryGeneration identifies one incarnation of a directory.
type DirectoryGeneration uint64

// PolicyGeneration identifies the policy revision currently attached to an
// inode.
type PolicyGeneration uint64

// TransactionID identifies a committed log transaction.
type TransactionID uint64

// LeaseID identifies one open lease.
type LeaseID uint64

// InodeKey is the complete identity of an inode incarnation.
type InodeKey struct {
	ID         InodeID         `json:"id"`
	Generation InodeGeneration `json:"generation"`
}

func (k InodeKey) String() string {
	return fmt.Sprintf("%d:%d", k.ID, k.Generation)
}

// DirectoryKey is the complete identity of a directory incarnation.
type DirectoryKey struct {
	ID         DirectoryID         `json:"id"`
	Generation DirectoryGeneration `json:"generation"`
}

func (k DirectoryKey) String() string {
	return fmt.Sprintf("%d:%d", k.ID, k.Generation)
}

// InodeStatus is the lifetime state of an inode.
type InodeStatus uint8

const (
	// InodeLive means the inode has at least one directory reference.
	InodeLive InodeStatus = iota + 1
	// InodeOrphan means the link count reached zero. Data remains available
	// while open leases exist.
	InodeOrphan
	// InodeDeleting means the inode is no longer reachable and is waiting for
	// its release callback to complete.
	InodeDeleting
)

// Short status aliases for callers that prefer the names used in the design
// specification.
const (
	Live     = InodeLive
	Orphan   = InodeOrphan
	Deleting = InodeDeleting
)

func (s InodeStatus) String() string {
	switch s {
	case InodeLive:
		return "Live"
	case InodeOrphan:
		return "Orphan"
	case InodeDeleting:
		return "Deleting"
	default:
		return fmt.Sprintf("InodeStatus(%d)", uint8(s))
	}
}

// DirectoryPermissions controls operations that create or remove names in a
// directory.
type DirectoryPermissions uint8

const (
	// PermCreate permits Link and Rename into a directory.
	PermCreate DirectoryPermissions = 1 << iota
	// PermRemove permits Unlink and Rename out of a directory.
	PermRemove
	// PermLookup permits opening a name in a directory.
	PermLookup
)

const PermAll = PermCreate | PermRemove | PermLookup

// PolicyFlags controls operations on an inode.
type PolicyFlags uint8

const (
	// PolicyAllowOpen permits new Open leases.
	PolicyAllowOpen PolicyFlags = 1 << iota
	// PolicyAllowLink permits adding a hard link.
	PolicyAllowLink
)

const PolicyAllowAll = PolicyAllowOpen | PolicyAllowLink

// Policy is the policy revision attached to an inode.
type Policy struct {
	Generation PolicyGeneration `json:"generation"`
	Flags      PolicyFlags      `json:"flags"`
}

// Inode is an immutable snapshot of inode metadata.
type Inode struct {
	ID               InodeID          `json:"id"`
	Generation       InodeGeneration  `json:"generation"`
	LinkCount        uint64           `json:"link_count"`
	OpenCount        uint64           `json:"open_count"`
	PolicyGeneration PolicyGeneration `json:"policy_generation"`
	Status           InodeStatus      `json:"status"`
}

// Key returns the inode identity.
func (i Inode) Key() InodeKey {
	return InodeKey{ID: i.ID, Generation: i.Generation}
}

// Directory is an immutable snapshot of directory metadata.
type Directory struct {
	ID          DirectoryID          `json:"id"`
	Generation  DirectoryGeneration  `json:"generation"`
	Permissions DirectoryPermissions `json:"permissions"`
}

// Key returns the directory identity.
func (d Directory) Key() DirectoryKey {
	return DirectoryKey{ID: d.ID, Generation: d.Generation}
}

// EntryKey identifies a name in one directory incarnation.
type EntryKey struct {
	DirectoryID         DirectoryID         `json:"directory_id"`
	DirectoryGeneration DirectoryGeneration `json:"directory_generation"`
	Name                string              `json:"name"`
}

// DirectoryEntry binds a canonical directory name to an exact inode
// incarnation.
type DirectoryEntry struct {
	DirectoryID         DirectoryID         `json:"directory_id"`
	DirectoryGeneration DirectoryGeneration `json:"directory_generation"`
	Name                string              `json:"name"`
	InodeID             InodeID             `json:"inode_id"`
	InodeGeneration     InodeGeneration     `json:"inode_generation"`
}

// Key returns the directory-local name key.
func (e DirectoryEntry) Key() EntryKey {
	return EntryKey{
		DirectoryID:         e.DirectoryID,
		DirectoryGeneration: e.DirectoryGeneration,
		Name:                e.Name,
	}
}

// InodeKey returns the exact inode identity referenced by the entry.
func (e DirectoryEntry) InodeKey() InodeKey {
	return InodeKey{ID: e.InodeID, Generation: e.InodeGeneration}
}

// Lease is a generation-bound capability returned by Open. It remains valid
// after the inode's last directory name is removed until the lease is closed or
// its configured grace period ends.
type Lease struct {
	ID               LeaseID          `json:"id"`
	InodeID          InodeID          `json:"inode_id"`
	InodeGeneration  InodeGeneration  `json:"inode_generation"`
	PolicyGeneration PolicyGeneration `json:"policy_generation"`
	OpenedAt         time.Time        `json:"opened_at"`
	ExpiresAt        time.Time        `json:"expires_at"`
}

// InodeKey returns the exact inode identity leased by this lease.
func (l Lease) InodeKey() InodeKey {
	return InodeKey{ID: l.InodeID, Generation: l.InodeGeneration}
}

// QuarantineReason explains why recovery refused to trust an inode or entry.
type QuarantineReason string

const (
	QuarantineLinkCountMismatch   QuarantineReason = "link_count_mismatch"
	QuarantineDuplicateEntry      QuarantineReason = "duplicate_directory_entry"
	QuarantineMissingInode        QuarantineReason = "directory_entry_missing_inode"
	QuarantineStaleGeneration     QuarantineReason = "stale_generation"
	QuarantineOpenCountMismatch   QuarantineReason = "open_count_mismatch"
	QuarantineInvalidStatus       QuarantineReason = "invalid_inode_status"
	QuarantineLogSemanticMismatch QuarantineReason = "log_semantic_mismatch"
)

// QuarantineRecord records an inode or directory entry that recovery isolated.
type QuarantineRecord struct {
	Inode  *InodeKey        `json:"inode,omitempty"`
	Entry  *EntryKey        `json:"entry,omitempty"`
	Reason QuarantineReason `json:"reason"`
	Detail string           `json:"detail,omitempty"`
}

// ReferenceProof is an independently rebuilt view of all directory entries.
// Matching contains only entries whose exact inode identity equals the inode
// being reclaimed.
type ReferenceProof struct {
	DirectoriesScanned int              `json:"directories_scanned"`
	EntriesScanned     int              `json:"entries_scanned"`
	Matching           []DirectoryEntry `json:"matching"`
}

// ReclaimEvent is passed to the reclaim callback only after a fresh complete
// directory scan proved that the inode has zero references.
type ReclaimEvent struct {
	Inode       Inode          `json:"inode"`
	Proof       ReferenceProof `json:"proof"`
	ReclaimedAt time.Time      `json:"reclaimed_at"`
}

// RecoveryReport describes what happened while rebuilding state from the
// authenticated log.
type RecoveryReport struct {
	Records            int                `json:"records"`
	CommittedTx        int                `json:"committed_transactions"`
	RolledBack         []TransactionID    `json:"rolled_back,omitempty"`
	TruncatedBytes     int64              `json:"truncated_bytes,omitempty"`
	QuarantinedInodes  []QuarantineRecord `json:"quarantined_inodes,omitempty"`
	QuarantinedEntries []QuarantineRecord `json:"quarantined_entries,omitempty"`
	SemanticProblems   []string           `json:"semantic_problems,omitempty"`
	Reclaimed          []InodeKey         `json:"reclaimed,omitempty"`
}

// RevokeMode controls existing leases when a policy is revoked. New opens are
// denied immediately in every mode.
type RevokeMode uint8

const (
	// RevokeDrain lets existing leases finish normally on Close.
	RevokeDrain RevokeMode = iota + 1
	// RevokeGrace gives existing leases a fixed grace period.
	RevokeGrace
	// RevokeEnd ends existing leases in the revocation transaction.
	RevokeEnd
)

// RevokeOptions overrides the host-wide lease policy for one revocation.
type RevokeOptions struct {
	Mode        RevokeMode
	GracePeriod time.Duration
}

// RevokePolicy is the host-wide rule for leases that already exist when an
// inode policy is revoked.
type RevokePolicy struct {
	Mode        RevokeMode
	GracePeriod time.Duration
}

// CanonicalName validates and returns a canonical directory name.
func CanonicalName(name string) (string, error) {
	switch {
	case name == "":
		return "", ErrInvalidName
	case name == "." || name == "..":
		return "", ErrInvalidName
	case strings.ContainsRune(name, '\x00'):
		return "", ErrInvalidName
	case strings.ContainsAny(name, `/\`):
		return "", ErrInvalidName
	case len(name) > 255:
		return "", ErrInvalidName
	case path.Clean(name) != name:
		return "", ErrInvalidName
	}
	return name, nil
}

func sortedEntries(entries []DirectoryEntry) {
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.DirectoryID != b.DirectoryID {
			return a.DirectoryID < b.DirectoryID
		}
		if a.DirectoryGeneration != b.DirectoryGeneration {
			return a.DirectoryGeneration < b.DirectoryGeneration
		}
		return a.Name < b.Name
	})
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

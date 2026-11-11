package journal

import (
	"fmt"
	"strings"
)

// Reason describes why a change-log record was emitted.
type Reason uint8

const (
	ReasonUnknown    Reason = iota
	ReasonCreate            // a new file object appears with its first directory entry
	ReasonLinkAdd           // an additional hard link (directory entry) is added
	ReasonRenameOld         // the old name of a rename, carrying the link being removed
	ReasonRenameNew         // the new name of a rename, carrying the link being added
	ReasonLinkRemove        // one hard link is removed
	ReasonDelete            // the object itself is deleted
)

// FileID identifies an object the way an NTFS file reference does: a stable
// reference number plus the sequence number that is bumped every time the
// reference number is reused. A reused reference number with a new sequence is
// therefore a different object, and stale records carrying the old sequence
// can never be applied to the new occupant.
type FileID struct {
	RefNum uint64
	Seq    uint64
}

func (id FileID) MarshalText() ([]byte, error) {
	return []byte(fmt.Sprintf("%d:%d", id.RefNum, id.Seq)), nil
}

func (id *FileID) UnmarshalText(data []byte) error {
	var ref, seq uint64
	if _, err := fmt.Sscanf(string(data), "%d:%d", &ref, &seq); err != nil {
		return fmt.Errorf("journal: malformed file id %q: %w", data, err)
	}
	id.RefNum, id.Seq = ref, seq
	return nil
}

func (id FileID) String() string {
	return fmt.Sprintf("%d:%d", id.RefNum, id.Seq)
}

// LinkKey is a directory entry: the parent directory reference plus the entry
// name. The set of LinkKeys belonging to one FileID is its hard-link set.
type LinkKey struct {
	ParentRef uint64
	Name      string
}

func (k LinkKey) MarshalText() ([]byte, error) {
	return []byte(fmt.Sprintf("%d\x1f%s", k.ParentRef, k.Name)), nil
}

func (k *LinkKey) UnmarshalText(data []byte) error {
	s := string(data)
	pos := strings.IndexByte(s, '\x1f')
	if pos < 0 {
		return fmt.Errorf("journal: malformed link key %q", s)
	}
	var parent uint64
	if _, err := fmt.Sscanf(s[:pos], "%d", &parent); err != nil {
		return fmt.Errorf("journal: malformed link key parent %q: %w", s, err)
	}
	k.ParentRef = parent
	k.Name = s[pos+1:]
	return nil
}

func (k LinkKey) String() string {
	return fmt.Sprintf("%d/%s", k.ParentRef, k.Name)
}

// Record is one change-log entry. USN is the monotonic log offset; JournalID
// identifies the log incarnation so log rotation cannot be mistaken for a
// contiguous continuation.
type Record struct {
	USN        uint64
	JournalID  uint64
	Reason     Reason
	ID         FileID
	ParentRef  uint64 // directory entry touched by old-side operations
	Name       string
	NewParent  uint64 // directory entry touched by rename-new
	NewName    string
	TimeMicros int64
}

func (r Record) oldKey() LinkKey { return LinkKey{ParentRef: r.ParentRef, Name: r.Name} }
func (r Record) newKey() LinkKey { return LinkKey{ParentRef: r.NewParent, Name: r.NewName} }

// Stats accumulates what the processor has done since its checkpoint lineage
// began. Counters are part of the checkpointed state.
type Stats struct {
	Creates      uint64
	LinksAdded   uint64
	LinksRemoved uint64
	Renames      uint64
	Deletes      uint64
	StaleSkipped uint64
	Conflicts    uint64
}

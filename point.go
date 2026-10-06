package retention

import "time"

// BackupType distinguishes a full baseline backup from an incremental
// (差量) backup that depends on a baseline and the preceding incrementals.
type BackupType int

const (
	Full BackupType = iota
	Incremental
)

func (t BackupType) String() string {
	switch t {
	case Full:
		return "full"
	case Incremental:
		return "incremental"
	}
	return "unknown"
}

// Point is a single recovery point in the backup catalog.
type Point struct {
	ID   string
	Time time.Time
	Type BackupType

	// DependsOn lists the IDs of the backups required to restore this
	// point: the full baseline plus any preceding incrementals.
	DependsOn []string

	// Corrupt marks a backup whose data failed integrity verification.
	// A corrupt point, or any point depending on one, is unusable.
	Corrupt       bool
	CorruptReason string
}

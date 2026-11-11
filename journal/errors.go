package journal

import "errors"

var (
	// ErrGap is returned when the log can no longer supply records starting at
	// the checkpointed USN: the journal was truncated past the resume point and
	// the index cannot be continued without a rebuild.
	ErrGap = errors.New("journal: change log truncated past checkpoint, full rescan required")
	// ErrJournalID is returned when the log incarnation changed in a way the
	// checkpoint cannot follow.
	ErrJournalID = errors.New("journal: change log journal id does not match checkpoint")
	// ErrOutOfOrder is returned when a record with a USN below the next
	// expected USN but not already applied is presented in a committed batch.
	ErrOutOfOrder = errors.New("journal: change record precedes expected usn")
)

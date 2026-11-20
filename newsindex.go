// Package newsindex implements a small, importable visibility index for a
// news archive.
//
// A message can have independent content fragments in several groups. A
// reader sees only fragments from the public group and from groups explicitly
// granted to that reader. Reply edges are visible only when both endpoint
// messages are visible, and reply counts are derived from the deduplicated set
// of visible message pairs.
//
// Every Commit creates an immutable archive version. Feed cursors resolve an
// immutable, principal-specific visible snapshot, so hidden inserts, hidden
// deletions, and edits to fragments the reader cannot see cannot change a
// page's size, ordering, counts, continuation state, or notifications.
package newsindex

import "errors"

// Group identifies a visibility group.
type Group string

// MessageID identifies a message. The same message ID may have fragments in
// multiple groups.
type MessageID string

// PrincipalID identifies a reader.
type PrincipalID string

// PublicGroup is implicitly visible to every principal. Grant and Revoke
// operations for this group are accepted but do not change authorization.
const PublicGroup Group = "public"

var (
	// ErrMessageNotFound is returned both for an unknown message and for a
	// message that has no fragment visible to the requesting principal.
	ErrMessageNotFound = errors.New("newsindex: message not found")

	// ErrInvalidCursor means a cursor is malformed or does not name a
	// snapshot known to this archive.
	ErrInvalidCursor = errors.New("newsindex: invalid cursor")

	// ErrStaleCursor means the principal's authorization generation changed
	// after the cursor was issued.
	ErrStaleCursor = errors.New("newsindex: stale cursor")

	// ErrCursorPrincipalMismatch means a cursor was presented by a different
	// principal than the one it was issued for.
	ErrCursorPrincipalMismatch = errors.New("newsindex: cursor belongs to another principal")

	// ErrInvalidPageSize means Page was called with a non-positive limit.
	ErrInvalidPageSize = errors.New("newsindex: invalid page size")

	// ErrFutureSnapshot means a notification request started after the
	// archive's latest committed version.
	ErrFutureSnapshot = errors.New("newsindex: snapshot is in the future")

	// ErrNilBatch is returned when Commit is called without a batch.
	ErrNilBatch = errors.New("newsindex: nil batch")

	// ErrInvalidOperation is returned for an operation with missing fields.
	ErrInvalidOperation = errors.New("newsindex: invalid operation")
)

// FragmentView is a visible content fragment.
type FragmentView struct {
	Group Group  `json:"group"`
	Body  string `json:"body"`
}

// MessageView is the complete visible view of one message.
//
// Replies contains the deduplicated IDs of visible direct replies.
// ReferencedBy contains the deduplicated IDs of visible messages that directly
// reply to this message. ReplyCount and ReferencedByCount are the lengths of
// those lists. Self-replies and reply cycles are permitted.
type MessageView struct {
	ID                MessageID      `json:"id"`
	Fragments         []FragmentView `json:"fragments"`
	Replies           []MessageID    `json:"replies"`
	ReferencedBy      []MessageID    `json:"referenced_by"`
	ReplyCount        int            `json:"reply_count"`
	ReferencedByCount int            `json:"referenced_by_count"`
}

// PageRequest asks for one page. An empty Cursor starts at the latest
// committed snapshot; a non-empty Cursor continues the exact visible snapshot
// that issued it.
type PageRequest struct {
	Limit  int
	Cursor string
}

// Page is one page of a principal's feed.
//
// Snapshot is a token for the principal-specific visible snapshot. Next is
// empty on the final page. AuthGeneration is the principal's authorization
// generation at that snapshot.
type Page struct {
	Items          []MessageView `json:"items"`
	Next           string        `json:"next,omitempty"`
	Snapshot       string        `json:"snapshot"`
	AuthGeneration uint64        `json:"auth_generation"`
}

// ChangeKind describes how a message's complete visible view changed.
type ChangeKind string

const (
	ChangeAdded   ChangeKind = "added"
	ChangeUpdated ChangeKind = "updated"
	ChangeRemoved ChangeKind = "removed"
)

// Change is a single notification entry. For ChangeRemoved, Message is nil
// and only the ID is reported. For ChangeAdded and ChangeUpdated, Message is
// the post-commit visible view.
type Change struct {
	Kind    ChangeKind   `json:"kind"`
	ID      MessageID    `json:"id"`
	Message *MessageView `json:"message,omitempty"`
}

// Notification contains the minimal message-level difference between the
// principal's visible views immediately before and immediately after one
// commit. Commits with no visible difference produce no notification.
type Notification struct {
	AuthGeneration uint64   `json:"auth_generation"`
	ViewToken      string   `json:"view_token"`
	Changes        []Change `json:"changes"`
}

// CommitResult identifies the version created by Commit.
type CommitResult struct {
	Snapshot uint64
}

package submigrate

// Domain tags separate every signed/hashed payload namespace.
const (
	tagEventIdentity = "submigrate/v1/event-identity"
	tagOccurrence    = "submigrate/v1/occurrence"
	tagVisibleChain  = "submigrate/v1/visible-chain"
	tagLogRoot       = "submigrate/v1/log-root"
	tagRoot          = "submigrate/v1/partition-root"
	tagReceipt       = "submigrate/v1/receipt"
	tagRevocation    = "submigrate/v1/revocation"
	tagCursor        = "submigrate/v1/cursor"
)

// Identifier aliases keep the cross-partition vocabulary explicit.
type (
	// EventID is the host-signed stable identity of an event. Dedup is legal
	// only on verified EventID values, never on content summaries.
	EventID string

	// PartitionID identifies one source subscription partition.
	PartitionID string

	// ReaderID identifies the principal that owns an old authorization.
	ReaderID string
)

// VisibleEvent is one public appearance of an event inside a partition.
// VisSeq is its 1-based index among the partition's visible events only;
// hidden events do not consume visible sequence numbers.
type VisibleEvent struct {
	Partition PartitionID
	VisSeq    uint64
	EventID   EventID
	Summary   Digest

	// IdentityPayload / OccurrencePayload are the exact signed byte strings.
	IdentityPayload   []byte
	OccurrencePayload []byte
	IdentitySig       []byte
	OccurrenceSig     []byte
}

// LogEntry is one physical partition slot. Exactly one field is set.
type LogEntry struct {
	Visible    *VisibleEvent
	HiddenBlob []byte
}

// IsHidden reports whether the slot was hidden from readers.
func (e LogEntry) IsHidden() bool { return e.Visible == nil }

// PartitionRoot is the host-signed commitment for a partition at one epoch.
// It commits to the full physical log and to the public visible prefix, while
// revealing neither the count nor the positions of hidden entries.
type PartitionRoot struct {
	Partition PartitionID
	Epoch     uint64

	LogLength        uint64
	LogRoot          Digest
	VisibleLength    uint64
	VisibleChainHead Digest
	// VisiblePrefixHeads[i] is the visible-chain hash after the first i visible
	// events (index 0 is the zero digest). It commits every visible prefix while
	// revealing nothing about hidden entries.
	VisiblePrefixHeads []Digest

	HostKey   []byte
	Signature []byte
}

// visibleChainHeadAt returns the committed visible-chain hash for the first n
// visible events. It is independent of the hidden physical layout.
func (r *PartitionRoot) visibleChainHeadAt(n uint64) Digest {
	if int(n) >= len(r.VisiblePrefixHeads) {
		return Digest{}
	}
	return r.VisiblePrefixHeads[n]
}

// Receipt is the old reader's host-signed subscription state.
//
// AuthorizedPrefix bounds what the host ever made visible to this reader:
// only visible events with VisSeq <= AuthorizedPrefix are inside the old
// authorization scope. ConfirmedPrefix <= AuthorizedPrefix is the contiguous
// prefix the reader had actually acknowledged; events after it but inside the
// authorization are undelivered/差分 candidates. No physical index appears.
type Receipt struct {
	Reader           ReaderID
	Partition        PartitionID
	Epoch            uint64
	AuthorizedPrefix uint64
	ConfirmedPrefix  uint64

	Signature []byte
}

// Revocation is a host-signed, explicit withdrawal of a reader's grant. It is
// the only artifact that may produce StatusRevoked.
type Revocation struct {
	Reader               ReaderID
	Partition            PartitionID
	Epoch                uint64
	LastAuthorizedPrefix uint64
	Signature            []byte
}

// Source records one verified appearance of a deduplicated event.
type Source struct {
	Partition PartitionID
	VisSeq    uint64
}

// Notification is a hidden-quantity-independent visible output: one entry per
// distinct, signed, in-scope event identity.
type Notification struct {
	EventID EventID
	Summary Digest
	Sources []Source
}

// PartitionProgress is the per-partition claim carried by a migration cursor.
// All numbers are visible-sequence numbers; no physical slot index exists.
type PartitionProgress struct {
	Partition            PartitionID
	Epoch                uint64
	AuthorizedPrefix     uint64
	ConfirmedVisibleHead uint64
	MergedVisSeq         uint64
}

// Cursor is the independently verifiable binding produced by a migration.
//
// It pins the reader, the service key, every host root and old receipt it
// relies on, the exact evidence replayed into the merged stream, and the
// resulting notifications. A verifier needs only public keys plus this value.
type Cursor struct {
	Reader        ReaderID
	ServiceKey    []byte
	Progress      []PartitionProgress
	Evidence      []*VisibleEvent
	Notifications []Notification
	Receipts      []*Receipt
	RootsHash     Digest

	ServiceSig []byte
}

// Migration outcome codes.
const (
	// StatusResumable: every input checked out and the merged prefix lies
	// exactly inside the old authorization scope; the result is safe to
	// resume/replay deterministically.
	StatusResumable = "resumable"

	// StatusResync: an input was malformed, inconsistent, out of scope, or
	// otherwise not provable. Nothing is migrated; the reader must resync.
	StatusResync = "needs-resync"

	// StatusRevoked: a host-signed revocation explicitly withdraws the grant.
	StatusRevoked = "revoked"
)

// Machine-stable reason codes explaining a non-resumable outcome.
const (
	ReasonOK                     = "ok"
	ReasonRootBadSignature       = "root-bad-signature"
	ReasonRootLogMismatch        = "root-log-mismatch"
	ReasonRootVisibleMismatch    = "root-visible-mismatch"
	ReasonReceiptBadSignature    = "receipt-bad-signature"
	ReasonReceiptUnknownRoot     = "receipt-unknown-root"
	ReasonReceiptEpochMismatch   = "receipt-epoch-mismatch"
	ReasonReceiptScopeOutOfRoot  = "receipt-scope-exceeds-root"
	ReasonReceiptConfirmedRange  = "receipt-confirmed-exceeds-authorized"
	ReasonReceiptForeignReader   = "receipt-foreign-reader"
	ReasonRevocationBadSignature = "revocation-bad-signature"
	ReasonRevoked                = "authorization-revoked"
	ReasonOccurrenceUnknownRoot  = "occurrence-unknown-partition"
	ReasonOccurrenceBadSignature = "occurrence-bad-signature"
	ReasonOccurrenceIdentityBad  = "occurrence-identity-bad-signature"
	ReasonOccurrenceChainGap     = "occurrence-visible-chain-gap"
	ReasonOccurrenceOutOfScope   = "occurrence-outside-authorization"
	ReasonIdentitySummaryClash   = "identity-summary-mismatch"
)

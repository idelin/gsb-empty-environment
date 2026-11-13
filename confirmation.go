package netsec

import (
	"crypto/subtle"
	"fmt"
	"time"
)

// ConfirmationMethod records how the two users compared/confirmed the
// safety number.
type ConfirmationMethod string

const (
	// MethodQRCode means the users scanned each other's QR payload.
	MethodQRCode ConfirmationMethod = "qr"

	// MethodManual means the users compared the displayed safety number.
	MethodManual ConfirmationMethod = "manual"

	// MethodOffline means the users compared out of band without a live
	// server connection.
	MethodOffline ConfirmationMethod = "offline"

	// Aliases with explicit Confirmation in the name.
	ConfirmationQRCode  ConfirmationMethod = MethodQRCode
	ConfirmationManual  ConfirmationMethod = MethodManual
	ConfirmationOffline ConfirmationMethod = MethodOffline
)

// ConfirmedParty is one account's list binding inside a confirmation.
type ConfirmedParty struct {
	AccountID     string
	ListVersion   uint64
	ListDigest    [32]byte
	AccountDigest [32]byte
}

// ConfirmationRecord is the durable record of a user's confirmation.
//
// It binds both complete account lists, the confirmation method, and the
// moment of confirmation. Device presence changes do not alter any bound
// field. Any addition, removal, public-key change, or added-version change
// causes the record to no longer match the current snapshots and therefore
// cannot continue to display Verified.
type ConfirmationRecord struct {
	AccountA string
	AccountB string

	ListVersionA uint64
	ListVersionB uint64

	ListDigestA [32]byte
	ListDigestB [32]byte

	AccountDigestA [32]byte
	AccountDigestB [32]byte

	Checksum [32]byte

	Method      ConfirmationMethod
	ConfirmedAt time.Time

	Revoked          bool
	RevokedAt        time.Time
	RevocationReason string
}

// NewConfirmationRecord binds two complete snapshots. Accounts are stored in
// lexicographic account-ID order so both clients produce the same record
// regardless of who initiated confirmation.
func NewConfirmationRecord(a, b *DeviceListSnapshot, method ConfirmationMethod, confirmedAt time.Time) (*ConfirmationRecord, error) {
	left, err := EnsureValid(a)
	if err != nil {
		return nil, err
	}
	right, err := EnsureValid(b)
	if err != nil {
		return nil, err
	}
	if left.AccountID == right.AccountID {
		return nil, ErrDuplicateAccount
	}
	if method == "" {
		return nil, fmt.Errorf("netsec: empty confirmation method")
	}
	if confirmedAt.IsZero() {
		confirmedAt = time.Now().UTC()
	} else {
		confirmedAt = confirmedAt.UTC()
	}
	if left.AccountID > right.AccountID {
		left, right = right, left
	}

	return &ConfirmationRecord{
		AccountA:       left.AccountID,
		AccountB:       right.AccountID,
		ListVersionA:   left.Version,
		ListVersionB:   right.Version,
		ListDigestA:    left.ListDigest,
		ListDigestB:    right.ListDigest,
		AccountDigestA: left.AccountDigest,
		AccountDigestB: right.AccountDigest,
		Checksum:       PairwiseChecksum(left.AccountDigest, right.AccountDigest),
		Method:         method,
		ConfirmedAt:    confirmedAt,
	}, nil
}

// Clone returns a copy of the record.
func (r *ConfirmationRecord) Clone() *ConfirmationRecord {
	if r == nil {
		return nil
	}
	clone := *r
	return &clone
}

// Party returns the binding for one account.
func (r *ConfirmationRecord) Party(accountID string) (ConfirmedParty, bool) {
	if r == nil {
		return ConfirmedParty{}, false
	}
	switch accountID {
	case r.AccountA:
		return ConfirmedParty{
			AccountID:     r.AccountA,
			ListVersion:   r.ListVersionA,
			ListDigest:    r.ListDigestA,
			AccountDigest: r.AccountDigestA,
		}, true
	case r.AccountB:
		return ConfirmedParty{
			AccountID:     r.AccountB,
			ListVersion:   r.ListVersionB,
			ListDigest:    r.ListDigestB,
			AccountDigest: r.AccountDigestB,
		}, true
	default:
		return ConfirmedParty{}, false
	}
}

// Parties returns both bindings in canonical order.
func (r *ConfirmationRecord) Parties() []ConfirmedParty {
	if r == nil {
		return nil
	}
	first, _ := r.Party(r.AccountA)
	second, _ := r.Party(r.AccountB)
	return []ConfirmedParty{first, second}
}

// Binding returns an immutable verification binding.
func (r *ConfirmationRecord) Binding() verificationBinding {
	if r == nil {
		return verificationBinding{parties: make(map[string]ConfirmedParty)}
	}
	binding := verificationBinding{checksum: r.Checksum}
	binding.parties = make(map[string]ConfirmedParty, 2)
	binding.parties[r.AccountA] = ConfirmedParty{
		AccountID:     r.AccountA,
		ListVersion:   r.ListVersionA,
		ListDigest:    r.ListDigestA,
		AccountDigest: r.AccountDigestA,
	}
	binding.parties[r.AccountB] = ConfirmedParty{
		AccountID:     r.AccountB,
		ListVersion:   r.ListVersionB,
		ListDigest:    r.ListDigestB,
		AccountDigest: r.AccountDigestB,
	}
	return binding
}

// Matches reports whether the current complete lists still match the
// confirmation binding and checksum.
func (r *ConfirmationRecord) Matches(accountA, accountB *DeviceListSnapshot) bool {
	if r == nil || r.Revoked {
		return false
	}
	left, err := EnsureValid(accountA)
	if err != nil {
		return false
	}
	right, err := EnsureValid(accountB)
	if err != nil {
		return false
	}
	first, ok := r.Party(left.AccountID)
	if !ok {
		return false
	}
	second, ok := r.Party(right.AccountID)
	if !ok {
		return false
	}
	if !partyMatchesSnapshot(first, left) || !partyMatchesSnapshot(second, right) {
		return false
	}
	expected := PairwiseChecksum(left.AccountDigest, right.AccountDigest)
	return subtle.ConstantTimeCompare(expected[:], r.Checksum[:]) == 1
}

func partyMatchesSnapshot(party ConfirmedParty, snapshot *DeviceListSnapshot) bool {
	return party.AccountID == snapshot.AccountID &&
		party.ListVersion == snapshot.Version &&
		party.ListDigest == snapshot.ListDigest &&
		party.AccountDigest == snapshot.AccountDigest
}

// verificationBinding is the internal, order-independent view of the list
// state that was verified or confirmed.
type verificationBinding struct {
	parties  map[string]ConfirmedParty
	checksum [32]byte
}

func bindingFromSnapshots(a, b *DeviceListSnapshot) (verificationBinding, error) {
	left, err := EnsureValid(a)
	if err != nil {
		return verificationBinding{}, err
	}
	right, err := EnsureValid(b)
	if err != nil {
		return verificationBinding{}, err
	}
	if left.AccountID == right.AccountID {
		return verificationBinding{}, ErrDuplicateAccount
	}
	return verificationBinding{
		parties: map[string]ConfirmedParty{
			left.AccountID: {
				AccountID:     left.AccountID,
				ListVersion:   left.Version,
				ListDigest:    left.ListDigest,
				AccountDigest: left.AccountDigest,
			},
			right.AccountID: {
				AccountID:     right.AccountID,
				ListVersion:   right.Version,
				ListDigest:    right.ListDigest,
				AccountDigest: right.AccountDigest,
			},
		},
		checksum: PairwiseChecksum(left.AccountDigest, right.AccountDigest),
	}, nil
}

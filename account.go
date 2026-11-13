package netsec

import "time"

// AccountID is the stable identifier of an authentication account.
type AccountID string

// AccountStatus controls whether sessions may be created for an account.
type AccountStatus uint8

const (
	AccountDisabled AccountStatus = iota
	AccountActive
	AccountLocked
)

func (s AccountStatus) String() string {
	switch s {
	case AccountActive:
		return "active"
	case AccountLocked:
		return "locked"
	default:
		return "disabled"
	}
}

// Account is the durable authentication record. CredentialEpoch changes
// whenever the credential itself changes. SessionEpoch is a full uint64 so it
// never silently truncates at 32 bits; every token carries the exact value.
type Account struct {
	ID                   AccountID
	CredentialEpoch      uint64
	SessionEpoch         uint64
	PasswordHash         PasswordHash
	RecoveryPolicyDigest PolicyDigest
	Status               AccountStatus
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func (a Account) clone() Account {
	a.PasswordHash = a.PasswordHash.Clone()
	return a
}

// Note that the zero value of AccountStatus is AccountDisabled. Accounts are
// only created through Service.CreateAccount, which sets AccountActive.

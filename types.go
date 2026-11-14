package netsec

import (
	"crypto/ed25519"
	"time"
)

// Authorization is the cleartext, backend-signed offline grant. Every field
// participates in both the signature and the locally stored digest, so the
// grant is cryptographically bound to one station, one connector and one
// account with a fixed quota and validity window.
type Authorization struct {
	// AuthID uniquely identifies a single issued grant on the backend.
	AuthID string
	// StationID is the charging station the grant is valid on.
	StationID string
	// ConnectorID identifies the connector of StationID the grant is valid on.
	ConnectorID string
	// AccountID is the user account that owns the quota.
	AccountID string
	// Quota is the total number of consumption units (for example charged
	// watt-minutes or credit units) covered by the grant.
	Quota int64
	// ValidFrom and ValidUntil are Unix seconds delimiting the validity window.
	ValidFrom  int64
	ValidUntil int64
}

// SignedToken is what the backend pushes to a station during synchronization.
// The station never persists this structure: only the digest of the canonical
// authorization and a remaining-quota counter are stored locally.
type SignedToken struct {
	Auth      Authorization
	Signature []byte
}

// Receipt is the atomic proof of a single offline quota deduction. Receipts
// form a per-station hash chain and carry a strictly monotonic sequence
// number, which gives the backend replay and rollback protection.
type Receipt struct {
	AuthID      string
	Digest      string
	StationID   string
	ConnectorID string
	AccountID   string
	Amount      int64
	// ConsumedAt is the Unix second at which the station performed the deduction.
	ConsumedAt int64
	// Seq is the station-wide strictly monotonic receipt sequence, starting at 1.
	Seq uint64
	// PrevHash is the hash-chain head before this receipt was created.
	PrevHash []byte
}

// SignedReceipt is a Receipt signed with the station's long-term Ed25519 key,
// ready to be enqueued for the next online synchronization.
type SignedReceipt struct {
	Receipt   Receipt
	Signature []byte
}

// StationConfig configures a local offline authorization component. The
// station private key lives in the station's secure storage and is never part
// of the copied authorization list.
type StationConfig struct {
	StationID  string
	PrivateKey ed25519.PrivateKey
	BackendKey ed25519.PublicKey
	DataDir    string
	// Now is the clock used for validity checks. When nil, time.Now is used.
	Now func() time.Time
}

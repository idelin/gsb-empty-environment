package netsec

import "errors"

// Security-relevant sentinel errors. Callers should treat every one of these
// as a hard rejection of the requested offline operation.
var (
	// ErrInvalidConfig is returned when a component is configured with missing
	// or malformed key material and identifiers.
	ErrInvalidConfig = errors.New("offlineauth: invalid component configuration")

	// ErrInvalidKey is returned when supplied key material has the wrong size.
	ErrInvalidKey = errors.New("offlineauth: invalid cryptographic key")

	// ErrInvalidAuthorization is returned when an authorization payload has
	// inconsistent business fields (zero quota, inverted validity window, ...).
	ErrInvalidAuthorization = errors.New("offlineauth: invalid authorization payload")

	// ErrAuthorizationExists is returned when the backend issues two
	// authorizations sharing the same AuthID.
	ErrAuthorizationExists = errors.New("offlineauth: authorization id already issued")

	// ErrStationExists is returned when a station id is registered twice.
	ErrStationExists = errors.New("offlineauth: station already registered")

	// ErrUnknownStation is returned when a receipt claims to originate from a
	// station the backend never registered.
	ErrUnknownStation = errors.New("offlineauth: unknown charging station")

	// ErrInvalidToken is returned when an authorization token cannot be
	// decoded from its wire representation.
	ErrInvalidToken = errors.New("offlineauth: malformed authorization token")

	// ErrInvalidReceipt is returned when a consumption receipt is nil or
	// cannot be decoded.
	ErrInvalidReceipt = errors.New("offlineauth: malformed consumption receipt")

	// ErrBadSignature is returned when a backend or station signature does not
	// verify against the canonical payload.
	ErrBadSignature = errors.New("offlineauth: signature verification failed")

	// ErrForeignStation is returned when an authorization or receipt is bound
	// to a different charging station than the one processing it. This is the
	// core guard against copied authorization lists.
	ErrForeignStation = errors.New("offlineauth: payload is bound to another charging station")

	// ErrCopiedStore is returned when a station opens a persistent store that
	// was created for a different station identifier.
	ErrCopiedStore = errors.New("offlineauth: local store was copied from another charging station")

	// ErrUnknownAuthorization is returned when a token is valid but its digest
	// is absent from the local authorization list.
	ErrUnknownAuthorization = errors.New("offlineauth: authorization not present in local list")

	// ErrNotYetValid is returned before an authorization's ValidFrom instant.
	ErrNotYetValid = errors.New("offlineauth: authorization is not yet valid")

	// ErrExpired is returned after an authorization's ValidUntil instant, or
	// when a receipt is reported outside its validity window.
	ErrExpired = errors.New("offlineauth: authorization has expired")

	// ErrInvalidAmount is returned for non-positive consumption amounts.
	ErrInvalidAmount = errors.New("offlineauth: consumption amount must be positive")

	// ErrQuotaExceeded is returned when a consumption would drive the remaining
	// quota below zero, either locally or at the backend.
	ErrQuotaExceeded = errors.New("offlineauth: authorization quota exceeded")

	// ErrReplay is returned when the backend receives a receipt whose sequence
	// number has already been applied.
	ErrReplay = errors.New("offlineauth: replayed consumption receipt")

	// ErrSequenceGap is returned when receipts arrive out of strict sequence.
	ErrSequenceGap = errors.New("offlineauth: receipt sequence gap")

	// ErrChainBroken is returned when a receipt's PrevHash does not match the
	// backend's current hash-chain head.
	ErrChainBroken = errors.New("offlineauth: receipt hash chain verification failed")

	// ErrReceiptMismatch is returned when a receipt's fields do not match the
	// authorization record held by the backend.
	ErrReceiptMismatch = errors.New("offlineauth: receipt does not match issued authorization")
)

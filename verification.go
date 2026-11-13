package netsec

import (
	"crypto/subtle"
	"fmt"
)

// VerificationStatus is the display state for a two-party session.
type VerificationStatus string

const (
	// StatusUnverified means no QR verification or user confirmation has
	// completed for the current list bindings.
	StatusUnverified VerificationStatus = "Unverified"

	// StatusVerified means both complete device sets, list versions, list
	// digests, and the recomputed checksum agree.
	StatusVerified VerificationStatus = "Verified"

	// StatusStale means an earlier verification or confirmation is bound to
	// an older/different list state. It must not be silently reused.
	StatusStale VerificationStatus = "Stale"

	// StatusMismatch means current device sets disagree with the scanned QR
	// binding at the same list version.
	StatusMismatch VerificationStatus = "Mismatch"

	// StatusInvalid means the QR payload was malformed, tampered with, or
	// referred to an unexpected account set.
	StatusInvalid VerificationStatus = "Invalid"
)

const (
	// Short aliases for the display state constants.
	Unverified = StatusUnverified
	Verified   = StatusVerified
	Stale      = StatusStale
	Mismatch   = StatusMismatch
	Invalid    = StatusInvalid
)

// String returns the display state as text.
func (s VerificationStatus) String() string {
	return string(s)
}

// VerificationResult is the outcome of checking one QR payload.
type VerificationResult struct {
	Status  VerificationStatus
	Reason  string
	Payload *QRPayload

	// SafetyNumber is present only for a fully Verified result.
	SafetyNumber *SafetyNumber

	// StaleAccounts lists accounts whose current list is newer than the
	// payload or whose old binding has been superseded.
	StaleAccounts []string
}

// OK reports whether the result displays Verified.
func (r *VerificationResult) OK() bool {
	return r != nil && r.Status == StatusVerified
}

// VerifyQRPayload checks a payload against a map containing exactly the two
// current account snapshots.
func VerifyQRPayload(payload *QRPayload, snapshots map[string]*DeviceListSnapshot) (*VerificationResult, error) {
	if payload == nil {
		return invalidResult(nil, "nil QR payload"), ErrInvalidQRPayload
	}
	if len(snapshots) != 2 {
		return invalidResult(payload, "verifier does not hold exactly two account snapshots"), ErrAccountSetMismatch
	}
	var accounts [2]string
	index := 0
	for accountID := range snapshots {
		accounts[index] = accountID
		index++
	}
	return verifyQRPayloadForAccounts(payload, accounts[0], accounts[1], snapshots)
}

// VerifyQRPayloadForAccounts checks a payload against two expected accounts.
// Extra unrelated snapshots in the map are ignored, but the payload must
// contain exactly the expected pair.
func VerifyQRPayloadForAccounts(payload *QRPayload, accountA, accountB string, snapshots map[string]*DeviceListSnapshot) (*VerificationResult, error) {
	return verifyQRPayloadForAccounts(payload, accountA, accountB, snapshots)
}

func verifyQRPayloadForAccounts(payload *QRPayload, accountA, accountB string, snapshots map[string]*DeviceListSnapshot) (*VerificationResult, error) {
	if payload == nil {
		return invalidResult(nil, "nil QR payload"), ErrInvalidQRPayload
	}
	if accountA == "" || accountB == "" {
		return invalidResult(payload, "empty expected account ID"), ErrInvalidAccountID
	}
	if accountA == accountB {
		return invalidResult(payload, "expected account IDs are identical"), ErrDuplicateAccount
	}
	if err := payload.Validate(); err != nil {
		return invalidResult(payload, err.Error()), err
	}

	payloadAccounts := make(map[string]QRParty, 2)
	for _, account := range payload.Accounts {
		if _, exists := payloadAccounts[account.AccountID]; exists {
			return invalidResult(payload, "duplicate account in QR payload"), ErrDuplicateAccount
		}
		payloadAccounts[account.AccountID] = account
	}
	if len(payloadAccounts) != 2 {
		return invalidResult(payload, "QR payload account set is not exactly two"), ErrAccountSetMismatch
	}
	if _, ok := payloadAccounts[accountA]; !ok {
		return invalidResult(payload, "expected account A absent from QR payload"), ErrAccountSetMismatch
	}
	if _, ok := payloadAccounts[accountB]; !ok {
		return invalidResult(payload, "expected account B absent from QR payload"), ErrAccountSetMismatch
	}
	if len(snapshots) < 2 {
		return invalidResult(payload, "verifier lacks one or both account snapshots"), ErrUnknownAccount
	}

	local := make(map[string]*DeviceListSnapshot, 2)
	for _, accountID := range []string{accountA, accountB} {
		snapshot, ok := snapshots[accountID]
		if !ok || snapshot == nil {
			return invalidResult(payload, fmt.Sprintf("no current snapshot for account %q", accountID)), ErrUnknownAccount
		}
		validated, err := EnsureValid(snapshot)
		if err != nil {
			return invalidResult(payload, fmt.Sprintf("invalid snapshot for account %q: %v", accountID, err)), err
		}
		if validated.AccountID != accountID {
			return invalidResult(payload, "snapshot account key does not match its AccountID"), ErrAccountSetMismatch
		}
		local[accountID] = validated
	}

	var staleAccounts []string
	for _, accountID := range []string{accountA, accountB} {
		current := local[accountID]
		remote := payloadAccounts[accountID]
		if current.Version > remote.ListVersion {
			staleAccounts = append(staleAccounts, accountID)
			continue
		}
		if current.Version < remote.ListVersion {
			staleAccounts = append(staleAccounts, accountID)
			continue
		}
	}
	if len(staleAccounts) > 0 {
		return &VerificationResult{
			Status:        StatusStale,
			Reason:        "one or both current lists differ in version from the QR payload",
			Payload:       payload,
			StaleAccounts: staleAccounts,
		}, ErrListVersionMismatch
	}

	for _, accountID := range []string{accountA, accountB} {
		current := local[accountID]
		remote := payloadAccounts[accountID]
		if current.ListDigest != remote.ListDigest {
			return &VerificationResult{
				Status:  StatusMismatch,
				Reason:  fmt.Sprintf("list digest mismatch for %q", accountID),
				Payload: payload,
			}, ErrListDigestMismatch
		}
		if !isZeroDigest(remote.AccountDigest) && current.AccountDigest != remote.AccountDigest {
			return &VerificationResult{
				Status:  StatusMismatch,
				Reason:  fmt.Sprintf("account identity digest mismatch for %q", accountID),
				Payload: payload,
			}, ErrIdentityDigestMismatch
		}
	}

	var localDigests [2][32]byte
	localDigests[0] = local[accountA].AccountDigest
	localDigests[1] = local[accountB].AccountDigest
	expectedChecksum := PairwiseChecksum(localDigests[0], localDigests[1])
	if subtle.ConstantTimeCompare(expectedChecksum[:], payload.Checksum[:]) != 1 {
		return invalidResult(payload, "checksum recomputed from current snapshots differs from QR checksum"), ErrChecksumMismatch
	}

	safetyNumber := SafetyNumberFromAccountDigests(localDigests[0], localDigests[1])
	return &VerificationResult{
		Status:       StatusVerified,
		Reason:       "complete device sets, list bindings, and checksum agree",
		Payload:      payload,
		SafetyNumber: safetyNumber,
	}, nil
}

func invalidResult(payload *QRPayload, reason string) *VerificationResult {
	return &VerificationResult{
		Status:  StatusInvalid,
		Reason:  reason,
		Payload: payload,
	}
}

package netsec

import (
	"crypto/subtle"
	"fmt"
	"sync"
	"time"
)

// Client is a Go client-side store of complete account snapshots and the
// verification/confirmation state for peers.
//
// A client may hold a different snapshot than its peer; that is exactly what
// QR verification detects. Only snapshots for the two accounts in a session
// participate in its safety number.
type Client struct {
	accountID string

	mu        sync.RWMutex
	snapshots map[string]*DeviceListSnapshot
	peers     map[string]*peerEvidence
}

type peerEvidence struct {
	baseline     *verificationBinding
	confirmation *ConfirmationRecord
	lastResult   *VerificationResult
}

// NewClient creates a client for accountID. Snapshot import operations return
// validation errors for an empty account ID.
func NewClient(accountID string) *Client {
	return &Client{
		accountID: accountID,
		snapshots: make(map[string]*DeviceListSnapshot),
		peers:     make(map[string]*peerEvidence),
	}
}

// NewClientWithSnapshots creates a client and imports all supplied snapshots.
func NewClientWithSnapshots(accountID string, snapshots ...*DeviceListSnapshot) (*Client, error) {
	client := NewClient(accountID)
	if err := client.ImportSnapshots(snapshots...); err != nil {
		return nil, err
	}
	return client, nil
}

// AccountID returns this client's account ID.
func (c *Client) AccountID() string {
	if c == nil {
		return ""
	}
	return c.accountID
}

// ImportSnapshot imports one complete, validated device-list snapshot. The
// snapshot replaces the client's previous view for its AccountID.
func (c *Client) ImportSnapshot(snapshot *DeviceListSnapshot) error {
	if c == nil {
		return ErrNilSnapshot
	}
	validated, err := EnsureValid(snapshot)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	old := c.snapshots[validated.AccountID]
	c.snapshots[validated.AccountID] = validated

	if old == nil {
		return nil
	}
	diff, err := DiffDeviceLists(old, validated)
	if err != nil {
		return err
	}
	if !diff.RevokesConfirmation() {
		return nil
	}

	// A complete-set change is a real rekey/removal/addition. Mark every
	// confirmation involving this account revoked; do not erase the audit
	// record.
	for _, evidence := range c.peers {
		record := evidence.confirmation
		if record == nil || record.Revoked {
			continue
		}
		if record.AccountA != validated.AccountID && record.AccountB != validated.AccountID {
			continue
		}
		record.Revoked = true
		record.RevokedAt = time.Now().UTC()
		record.RevocationReason = fmt.Sprintf(
			"device identity list changed for account %q (added=%v removed=%v key_changed=%v added_version_changed=%v)",
			validated.AccountID, diff.Added, diff.Removed, diff.KeyChanged, diff.VersionChanged,
		)
	}
	return nil
}

// SetSnapshot is an alias for ImportSnapshot.
func (c *Client) SetSnapshot(snapshot *DeviceListSnapshot) error {
	return c.ImportSnapshot(snapshot)
}

// UpdateSnapshot is an alias for ImportSnapshot.
func (c *Client) UpdateSnapshot(snapshot *DeviceListSnapshot) error {
	return c.ImportSnapshot(snapshot)
}

// ImportSnapshots validates and imports a batch. Validation happens before
// any snapshot is installed, so an invalid batch leaves the client unchanged.
func (c *Client) ImportSnapshots(snapshots ...*DeviceListSnapshot) error {
	validated := make([]*DeviceListSnapshot, len(snapshots))
	for i, snapshot := range snapshots {
		value, err := EnsureValid(snapshot)
		if err != nil {
			return err
		}
		validated[i] = value
	}
	for _, snapshot := range validated {
		if err := c.ImportSnapshot(snapshot); err != nil {
			return err
		}
	}
	return nil
}

// SetSnapshots is an alias for ImportSnapshots.
func (c *Client) SetSnapshots(snapshots ...*DeviceListSnapshot) error {
	return c.ImportSnapshots(snapshots...)
}

// SnapshotFor returns a deep copy of the client's current snapshot for an
// account.
func (c *Client) SnapshotFor(accountID string) (*DeviceListSnapshot, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	snapshot, ok := c.snapshots[accountID]
	if !ok {
		return nil, false
	}
	return snapshot.Clone(), true
}

// Snapshot is an alias for SnapshotFor.
func (c *Client) Snapshot(accountID string) (*DeviceListSnapshot, bool) {
	return c.SnapshotFor(accountID)
}

// OwnSnapshot returns this client's snapshot for its own account.
func (c *Client) OwnSnapshot() (*DeviceListSnapshot, bool) {
	return c.SnapshotFor(c.AccountID())
}

// Snapshots returns deep copies of every known snapshot.
func (c *Client) Snapshots() map[string]*DeviceListSnapshot {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make(map[string]*DeviceListSnapshot, len(c.snapshots))
	for accountID, snapshot := range c.snapshots {
		result[accountID] = snapshot.Clone()
	}
	return result
}

// SetDeviceOnline changes only the non-security presence bit for one device.
// It deliberately leaves the list/account digests and all verification state
// untouched.
func (c *Client) SetDeviceOnline(accountID, deviceID string, online bool) error {
	if c == nil {
		return ErrNilSnapshot
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	snapshot, ok := c.snapshots[accountID]
	if !ok {
		return ErrUnknownAccount
	}
	for i := range snapshot.Devices {
		if snapshot.Devices[i].DeviceID == deviceID {
			updated := snapshot.Clone()
			updated.Devices[i].Online = online
			c.snapshots[accountID] = updated
			return nil
		}
	}
	return ErrDeviceNotFound
}

// SafetyNumberFor computes the safety number using this client's view of its
// own and the peer's complete device lists.
func (c *Client) SafetyNumberFor(peerAccountID string) (*SafetyNumber, error) {
	if c == nil {
		return nil, ErrNilSnapshot
	}
	own, ok := c.SnapshotFor(c.AccountID())
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAccount, c.AccountID())
	}
	peer, ok := c.SnapshotFor(peerAccountID)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAccount, peerAccountID)
	}
	return ComputeSafetyNumber(own, peer)
}

// SafetyNumber computes the safety number using this client's local view of
// both accounts.
func (c *Client) SafetyNumber(peer *Client) (*SafetyNumber, error) {
	if peer == nil {
		return nil, fmt.Errorf("netsec: nil peer client")
	}
	return c.SafetyNumberFor(peer.AccountID())
}

// SafetyNumberWith is an alias for SafetyNumber.
func (c *Client) SafetyNumberWith(peer *Client) (*SafetyNumber, error) {
	return c.SafetyNumber(peer)
}

// GenerateQRPayload builds a QR payload from this client's local view of the
// two accounts. It does not consult the peer client's snapshots.
func (c *Client) GenerateQRPayload(peerAccountID string) (*QRPayload, error) {
	if c == nil {
		return nil, ErrNilSnapshot
	}
	own, ok := c.SnapshotFor(c.AccountID())
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAccount, c.AccountID())
	}
	peer, ok := c.SnapshotFor(peerAccountID)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAccount, peerAccountID)
	}
	return NewQRPayload(own, peer)
}

// GenerateQRCode returns encoded QR payload bytes.
func (c *Client) GenerateQRCode(peerAccountID string) ([]byte, error) {
	payload, err := c.GenerateQRPayload(peerAccountID)
	if err != nil {
		return nil, err
	}
	return payload.Encode()
}

// QRPayloadFor is an alias for GenerateQRPayload.
func (c *Client) QRPayloadFor(peerAccountID string) (*QRPayload, error) {
	return c.GenerateQRPayload(peerAccountID)
}

// VerifyQRPayload checks a decoded payload against this client's snapshots and
// records its status. A successful check establishes a Verified baseline; a
// later list change makes that state Stale.
func (c *Client) VerifyQRPayload(payload *QRPayload, peerAccountID string) (*VerificationResult, error) {
	if c == nil {
		return invalidResult(payload, "nil client"), ErrNilSnapshot
	}
	own, ok := c.SnapshotFor(c.AccountID())
	if !ok {
		return invalidResult(payload, "missing own snapshot"), ErrUnknownAccount
	}
	peer, ok := c.SnapshotFor(peerAccountID)
	if !ok {
		return invalidResult(payload, "missing peer snapshot"), ErrUnknownAccount
	}
	snapshots := map[string]*DeviceListSnapshot{
		c.AccountID(): own,
		peerAccountID: peer,
	}
	result, err := verifyQRPayloadForAccounts(payload, c.AccountID(), peerAccountID, snapshots)
	c.rememberVerification(peerAccountID, result, own, peer)
	return result, err
}

// VerifyQR decodes and checks QR payload bytes.
func (c *Client) VerifyQR(peerAccountID string, encoded []byte) (*VerificationResult, error) {
	payload, err := DecodeQRPayload(encoded)
	if err != nil {
		result := invalidResult(nil, err.Error())
		c.rememberVerification(peerAccountID, result, nil, nil)
		return result, err
	}
	return c.VerifyQRPayload(payload, peerAccountID)
}

// VerifyQRCode is an alias for VerifyQR.
func (c *Client) VerifyQRCode(peerAccountID string, encoded []byte) (*VerificationResult, error) {
	return c.VerifyQR(peerAccountID, encoded)
}

// ScanQR is an alias for VerifyQR.
func (c *Client) ScanQR(peerAccountID string, encoded []byte) (*VerificationResult, error) {
	return c.VerifyQR(peerAccountID, encoded)
}

// VerifyQRFrom verifies a QR generated from the peer client's local view.
func (c *Client) VerifyQRFrom(peer *Client) (*VerificationResult, error) {
	if peer == nil {
		return invalidResult(nil, "nil peer client"), fmt.Errorf("netsec: nil peer client")
	}
	payload, err := peer.GenerateQRPayload(c.AccountID())
	if err != nil {
		return invalidResult(nil, err.Error()), err
	}
	return c.VerifyQRPayload(payload, peer.AccountID())
}

func (c *Client) rememberVerification(peerAccountID string, result *VerificationResult, own, peer *DeviceListSnapshot) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	evidence := c.peerEvidenceLocked(peerAccountID)
	evidence.lastResult = result
	if result != nil && result.Status == StatusVerified && own != nil && peer != nil {
		if binding, err := bindingFromSnapshots(own, peer); err == nil {
			evidence.baseline = &binding
		}
	}
}

// Status returns the current display state for a peer.
func (c *Client) Status(peerAccountID string) VerificationStatus {
	if c == nil {
		return StatusUnverified
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	evidence := c.peers[peerAccountID]
	if evidence == nil {
		return StatusUnverified
	}
	if evidence.lastResult != nil && evidence.lastResult.Status != StatusVerified {
		return evidence.lastResult.Status
	}
	if evidence.baseline != nil {
		if c.bindingMatchesCurrentLocked(*evidence.baseline) {
			return StatusVerified
		}
		return StatusStale
	}
	if evidence.confirmation != nil {
		if !evidence.confirmation.Revoked &&
			c.bindingMatchesCurrentLocked(evidence.confirmation.Binding()) {
			return StatusVerified
		}
		return StatusStale
	}
	if evidence.lastResult != nil {
		return evidence.lastResult.Status
	}
	return StatusUnverified
}

// VerificationStatus is an alias for Status.
func (c *Client) VerificationStatus(peerAccountID string) VerificationStatus {
	return c.Status(peerAccountID)
}

// State is an alias for Status.
func (c *Client) State(peerAccountID string) VerificationStatus {
	return c.Status(peerAccountID)
}

// Confirm records a user confirmation binding both current complete account
// lists, the method, and the confirmation time.
func (c *Client) Confirm(peerAccountID string, method ConfirmationMethod, at time.Time) (*ConfirmationRecord, error) {
	if c == nil {
		return nil, ErrNilSnapshot
	}
	own, ok := c.SnapshotFor(c.AccountID())
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAccount, c.AccountID())
	}
	peer, ok := c.SnapshotFor(peerAccountID)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAccount, peerAccountID)
	}
	record, err := NewConfirmationRecord(own, peer, method, at)
	if err != nil {
		return nil, err
	}
	c.storeConfirmation(peerAccountID, record)
	return record.Clone(), nil
}

// ConfirmNow records a confirmation at the current UTC time.
func (c *Client) ConfirmNow(peerAccountID string, method ConfirmationMethod) (*ConfirmationRecord, error) {
	return c.Confirm(peerAccountID, method, time.Now().UTC())
}

// ConfirmationFor returns the stored confirmation, if any.
func (c *Client) ConfirmationFor(peerAccountID string) (*ConfirmationRecord, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	evidence := c.peers[peerAccountID]
	if evidence == nil || evidence.confirmation == nil {
		return nil, false
	}
	return evidence.confirmation.Clone(), true
}

// Confirmation is an alias for ConfirmationFor.
func (c *Client) Confirmation(peerAccountID string) (*ConfirmationRecord, bool) {
	return c.ConfirmationFor(peerAccountID)
}

// ConfirmPair confirms only if both clients currently hold the same complete
// device sets for both accounts. The record is installed on both clients.
func (c *Client) ConfirmPair(peer *Client, method ConfirmationMethod, at time.Time) (*ConfirmationRecord, error) {
	if c == nil || peer == nil {
		return nil, fmt.Errorf("netsec: nil client in pair")
	}
	if c.AccountID() == peer.AccountID() {
		return nil, ErrDuplicateAccount
	}

	leftOwn, ok := c.SnapshotFor(c.AccountID())
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAccount, c.AccountID())
	}
	leftPeer, ok := c.SnapshotFor(peer.AccountID())
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAccount, peer.AccountID())
	}
	rightOwn, ok := peer.SnapshotFor(c.AccountID())
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAccount, c.AccountID())
	}
	rightPeer, ok := peer.SnapshotFor(peer.AccountID())
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAccount, peer.AccountID())
	}
	if !leftOwn.SameDeviceSet(rightOwn) || !leftPeer.SameDeviceSet(rightPeer) {
		return nil, ErrViewsMismatch
	}

	record, err := NewConfirmationRecord(leftOwn, leftPeer, method, at)
	if err != nil {
		return nil, err
	}
	c.storeConfirmation(peer.AccountID(), record)
	peer.storeConfirmation(c.AccountID(), record)
	return record.Clone(), nil
}

// ConfirmPairNow is ConfirmPair at the current UTC time.
func (c *Client) ConfirmPairNow(peer *Client, method ConfirmationMethod) (*ConfirmationRecord, error) {
	return c.ConfirmPair(peer, method, time.Now().UTC())
}

func (c *Client) storeConfirmation(peerAccountID string, record *ConfirmationRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	evidence := c.peerEvidenceLocked(peerAccountID)
	evidence.confirmation = record.Clone()
	evidence.baseline = nil
	evidence.lastResult = nil
}

func (c *Client) peerEvidenceLocked(peerAccountID string) *peerEvidence {
	evidence := c.peers[peerAccountID]
	if evidence == nil {
		evidence = &peerEvidence{}
		c.peers[peerAccountID] = evidence
	}
	return evidence
}

func (c *Client) bindingMatchesCurrentLocked(binding verificationBinding) bool {
	if len(binding.parties) != 2 {
		return false
	}
	var digests [2][32]byte
	index := 0
	for accountID, party := range binding.parties {
		snapshot, ok := c.snapshots[accountID]
		if !ok || snapshot == nil {
			return false
		}
		if snapshot.Version != party.ListVersion ||
			snapshot.ListDigest != party.ListDigest ||
			snapshot.AccountDigest != party.AccountDigest {
			return false
		}
		digests[index] = snapshot.AccountDigest
		index++
	}
	expected := PairwiseChecksum(digests[0], digests[1])
	return subtle.ConstantTimeCompare(expected[:], binding.checksum[:]) == 1
}

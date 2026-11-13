package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
)

const (
	// DeviceListDomain is the fixed domain separator for the digest of one
	// canonical, sorted device list.
	DeviceListDomain = "example.org/netsec/q0155/device-list/v1"

	// AccountIdentityDomain is the fixed domain separator for an account's
	// multi-device identity digest.
	AccountIdentityDomain = "example.org/netsec/q0155/account-identity/v1"

	// ProtocolVersion is the QR payload protocol version implemented here.
	ProtocolVersion uint16 = 1
)

// Device is one device identity entry in a device-list snapshot.
//
// Online is deliberately not part of the canonical encoding, the list digest,
// or the account digest. Presence is useful UI state, but it is not an
// identity change and must not revoke a user confirmation.
type Device struct {
	DeviceID          string `json:"device_id"`
	IdentityPublicKey []byte `json:"identity_public_key"`
	AddedVersion      uint64 `json:"added_version"`
	Online            bool   `json:"online,omitempty"`
}

// DeviceListSnapshot is a complete, validated device list for one account.
//
// It represents one server list version, not an incremental event stream.
// Therefore a hidden/omitted device is indistinguishable from a removed
// device, and both change the identity digest.
type DeviceListSnapshot struct {
	AccountID string   `json:"account_id"`
	Version   uint64   `json:"version"`
	Devices   []Device `json:"devices"`

	// ListDigest binds the account ID, list version, and every canonical
	// device entry.
	ListDigest [32]byte `json:"list_digest"`

	// AccountDigest is the account identity digest consumed by the
	// two-party safety number.
	AccountDigest [32]byte `json:"account_digest"`
}

// DeviceList is a convenient alias for DeviceListSnapshot.
type DeviceList = DeviceListSnapshot

// Snapshot is a convenient alias for DeviceListSnapshot.
type Snapshot = DeviceListSnapshot

// NewDeviceListSnapshot validates and canonicalizes a device list.
//
// Devices are sorted by DeviceID. Device IDs must be non-empty and unique;
// each identity public key must be non-empty. The returned snapshot owns
// copies of all caller-provided byte slices.
func NewDeviceListSnapshot(accountID string, version uint64, devices []Device) (*DeviceListSnapshot, error) {
	if accountID == "" {
		return nil, ErrInvalidAccountID
	}

	canonical, err := CanonicalDevices(devices)
	if err != nil {
		return nil, err
	}

	listDigest, err := ComputeDeviceListDigest(accountID, version, canonical)
	if err != nil {
		return nil, err
	}
	accountDigest, err := ComputeAccountIdentityDigest(accountID, version, devices)
	if err != nil {
		return nil, err
	}

	return &DeviceListSnapshot{
		AccountID:     accountID,
		Version:       version,
		Devices:       canonical,
		ListDigest:    listDigest,
		AccountDigest: accountDigest,
	}, nil
}

// NewSnapshot is an alias for NewDeviceListSnapshot.
func NewSnapshot(accountID string, version uint64, devices []Device) (*DeviceListSnapshot, error) {
	return NewDeviceListSnapshot(accountID, version, devices)
}

// MustNewDeviceListSnapshot is NewDeviceListSnapshot for tests and static
// data. It panics on invalid input.
func MustNewDeviceListSnapshot(accountID string, version uint64, devices []Device) *DeviceListSnapshot {
	snapshot, err := NewDeviceListSnapshot(accountID, version, devices)
	if err != nil {
		panic(err)
	}
	return snapshot
}

// CanonicalDevices returns a deep, DeviceID-sorted copy of devices after
// enforcing the snapshot invariants.
func CanonicalDevices(devices []Device) ([]Device, error) {
	canonical := make([]Device, len(devices))
	seen := make(map[string]struct{}, len(devices))

	for i, device := range devices {
		if device.DeviceID == "" {
			return nil, ErrInvalidDeviceID
		}
		if len(device.IdentityPublicKey) == 0 {
			return nil, fmt.Errorf("%w: device %q", ErrEmptyPublicKey, device.DeviceID)
		}
		if _, exists := seen[device.DeviceID]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateDeviceID, device.DeviceID)
		}
		seen[device.DeviceID] = struct{}{}

		canonical[i] = Device{
			DeviceID:          device.DeviceID,
			IdentityPublicKey: append([]byte(nil), device.IdentityPublicKey...),
			AddedVersion:      device.AddedVersion,
			Online:            device.Online,
		}
	}

	sort.Slice(canonical, func(i, j int) bool {
		return canonical[i].DeviceID < canonical[j].DeviceID
	})
	return canonical, nil
}

// Clone returns a deep copy of the snapshot.
func (s *DeviceListSnapshot) Clone() *DeviceListSnapshot {
	if s == nil {
		return nil
	}
	clone := *s
	clone.Devices = make([]Device, len(s.Devices))
	for i, device := range s.Devices {
		clone.Devices[i] = Device{
			DeviceID:          device.DeviceID,
			IdentityPublicKey: append([]byte(nil), device.IdentityPublicKey...),
			AddedVersion:      device.AddedVersion,
			Online:            device.Online,
		}
	}
	return &clone
}

// EnsureValid validates a snapshot and returns a canonical copy.
//
// If ListDigest or AccountDigest is all zero, it is treated as uninitialized
// and is filled in from the snapshot contents. Non-zero digest fields must
// match the contents exactly; this prevents callers from accidentally
// carrying a stale digest into a new device set.
func EnsureValid(snapshot *DeviceListSnapshot) (*DeviceListSnapshot, error) {
	if snapshot == nil {
		return nil, ErrNilSnapshot
	}
	if snapshot.AccountID == "" {
		return nil, ErrInvalidAccountID
	}

	canonical, err := CanonicalDevices(snapshot.Devices)
	if err != nil {
		return nil, err
	}
	listDigest, err := ComputeDeviceListDigest(snapshot.AccountID, snapshot.Version, canonical)
	if err != nil {
		return nil, err
	}
	accountDigest, err := ComputeAccountIdentityDigest(snapshot.AccountID, snapshot.Version, canonical)
	if err != nil {
		return nil, err
	}

	if !isZeroDigest(snapshot.ListDigest) && snapshot.ListDigest != listDigest {
		return nil, fmt.Errorf("%w: account %q version %d list digest", ErrSnapshotDigestMismatch, snapshot.AccountID, snapshot.Version)
	}
	if !isZeroDigest(snapshot.AccountDigest) && snapshot.AccountDigest != accountDigest {
		return nil, fmt.Errorf("%w: account %q version %d account digest", ErrSnapshotDigestMismatch, snapshot.AccountID, snapshot.Version)
	}

	return &DeviceListSnapshot{
		AccountID:     snapshot.AccountID,
		Version:       snapshot.Version,
		Devices:       canonical,
		ListDigest:    listDigest,
		AccountDigest: accountDigest,
	}, nil
}

// Validate reports whether the snapshot contents and stored digests agree.
func (s *DeviceListSnapshot) Validate() error {
	if s == nil {
		return ErrNilSnapshot
	}
	_, err := EnsureValid(s)
	return err
}

// ListDigestValue returns the list digest.
func (s *DeviceListSnapshot) ListDigestValue() [32]byte {
	if s == nil {
		return [32]byte{}
	}
	return s.ListDigest
}

// IdentityDigest returns the account identity digest.
func (s *DeviceListSnapshot) IdentityDigest() [32]byte {
	if s == nil {
		return [32]byte{}
	}
	return s.AccountDigest
}

// AccountIdentityDigest returns the account identity digest.
func (s *DeviceListSnapshot) AccountIdentityDigest() [32]byte {
	return s.IdentityDigest()
}

// Digest is an alias for ListDigestValue.
func (s *DeviceListSnapshot) Digest() [32]byte {
	return s.ListDigestValue()
}

// SameDeviceSet compares complete identity sets, ignoring Online and the
// stored digest fields. Account ID and list version are included.
func (s *DeviceListSnapshot) SameDeviceSet(other *DeviceListSnapshot) bool {
	if s == nil || other == nil {
		return s == nil && other == nil
	}
	if s.AccountID != other.AccountID || s.Version != other.Version {
		return false
	}
	left, err := CanonicalDevices(s.Devices)
	if err != nil {
		return false
	}
	right, err := CanonicalDevices(other.Devices)
	if err != nil {
		return false
	}
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].DeviceID != right[i].DeviceID ||
			left[i].AddedVersion != right[i].AddedVersion ||
			!bytes.Equal(left[i].IdentityPublicKey, right[i].IdentityPublicKey) {
			return false
		}
	}
	return true
}

// Equal is an alias for SameDeviceSet.
func (s *DeviceListSnapshot) Equal(other *DeviceListSnapshot) bool {
	return s.SameDeviceSet(other)
}

// Device returns a copy of one device entry.
func (s *DeviceListSnapshot) Device(deviceID string) (Device, bool) {
	if s == nil {
		return Device{}, false
	}
	for _, device := range s.Devices {
		if device.DeviceID == deviceID {
			return Device{
				DeviceID:          device.DeviceID,
				IdentityPublicKey: append([]byte(nil), device.IdentityPublicKey...),
				AddedVersion:      device.AddedVersion,
				Online:            device.Online,
			}, true
		}
	}
	return Device{}, false
}

// ComputeDeviceListDigest hashes the fixed list domain, account ID, list
// version, device count, and every DeviceID-sorted device entry.
func ComputeDeviceListDigest(accountID string, version uint64, devices []Device) ([32]byte, error) {
	if accountID == "" {
		return [32]byte{}, ErrInvalidAccountID
	}
	canonical, err := CanonicalDevices(devices)
	if err != nil {
		return [32]byte{}, err
	}
	return deviceListDigest(accountID, version, canonical), nil
}

// ComputeAccountIdentityDigest hashes the fixed account domain, account ID,
// list version, list digest, and every canonical device entry.
func ComputeAccountIdentityDigest(accountID string, version uint64, devices []Device) ([32]byte, error) {
	if accountID == "" {
		return [32]byte{}, ErrInvalidAccountID
	}
	canonical, err := CanonicalDevices(devices)
	if err != nil {
		return [32]byte{}, err
	}
	listDigest := deviceListDigest(accountID, version, canonical)
	return accountIdentityDigest(accountID, version, listDigest, canonical), nil
}

// ComputeAccountIdentityDigestFromList lets a caller that already holds a
// trusted list digest recompute the account identity digest.
func ComputeAccountIdentityDigestFromList(accountID string, version uint64, listDigest [32]byte, devices []Device) ([32]byte, error) {
	if accountID == "" {
		return [32]byte{}, ErrInvalidAccountID
	}
	canonical, err := CanonicalDevices(devices)
	if err != nil {
		return [32]byte{}, err
	}
	expected := deviceListDigest(accountID, version, canonical)
	if listDigest != expected {
		return [32]byte{}, ErrListDigestMismatch
	}
	return accountIdentityDigest(accountID, version, listDigest, canonical), nil
}

// CanonicalDeviceEncoding returns the unambiguous, fixed-format encoding of
// one device entry. Online is intentionally omitted.
func CanonicalDeviceEncoding(device Device) []byte {
	buffer := make([]byte, 0, 4+len(device.DeviceID)+4+len(device.IdentityPublicKey)+8)
	buffer = appendString(buffer, device.DeviceID)
	buffer = appendBytes(buffer, device.IdentityPublicKey)
	buffer = appendUint64(buffer, device.AddedVersion)
	return buffer
}

func deviceListDigest(accountID string, version uint64, canonical []Device) [32]byte {
	buffer := make([]byte, 0, 128)
	buffer = appendString(buffer, DeviceListDomain)
	buffer = appendString(buffer, accountID)
	buffer = appendUint64(buffer, version)
	buffer = appendUint32(buffer, uint32(len(canonical)))
	for _, device := range canonical {
		buffer = append(buffer, CanonicalDeviceEncoding(device)...)
	}
	return sha256.Sum256(buffer)
}

func accountIdentityDigest(accountID string, version uint64, listDigest [32]byte, canonical []Device) [32]byte {
	buffer := make([]byte, 0, 160)
	buffer = appendString(buffer, AccountIdentityDomain)
	buffer = appendString(buffer, accountID)
	buffer = appendUint64(buffer, version)
	buffer = append(buffer, listDigest[:]...)
	buffer = appendUint32(buffer, uint32(len(canonical)))
	for _, device := range canonical {
		buffer = append(buffer, CanonicalDeviceEncoding(device)...)
	}
	return sha256.Sum256(buffer)
}

func appendString(dst []byte, value string) []byte {
	return appendBytes(dst, []byte(value))
}

func appendBytes(dst, value []byte) []byte {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	dst = append(dst, length[:]...)
	return append(dst, value...)
}

func appendUint32(dst []byte, value uint32) []byte {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	return append(dst, encoded[:]...)
}

func appendUint64(dst []byte, value uint64) []byte {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	return append(dst, encoded[:]...)
}

func isZeroDigest(digest [32]byte) bool {
	var zero [32]byte
	return digest == zero
}

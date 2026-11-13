package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// AccountID identifies an account whose device list is being synchronized.
type AccountID string

// DeviceID is a stable identifier for a device. Device IDs are never reused.
type DeviceID string

// Version is a monotonically increasing device-list version.
type Version uint64

// ContactID identifies a contact that signs an observation receipt.
type ContactID string

// Digest is a SHA-256 digest.
type Digest [sha256.Size]byte

// ZeroDigest is the previous-list digest of a fresh client. The first device
// list must use version 1 and this previous digest.
var ZeroDigest Digest

func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

func (d Digest) Hex() string {
	return d.String()
}

func (d Digest) IsZero() bool {
	return d == ZeroDigest
}

func DigestFromBytes(b []byte) (Digest, error) {
	if len(b) != sha256.Size {
		return Digest{}, fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidDigest, len(b), sha256.Size)
	}
	var out Digest
	copy(out[:], b)
	return out, nil
}

func ParseDigest(s string) (Digest, error) {
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return Digest{}, fmt.Errorf("%w: %v", ErrInvalidDigest, err)
	}
	if len(b) != sha256.Size {
		return Digest{}, fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidDigest, len(b), sha256.Size)
	}
	var out Digest
	copy(out[:], b)
	return out, nil
}

func MustParseDigest(s string) Digest {
	d, err := ParseDigest(s)
	if err != nil {
		panic(err)
	}
	return d
}

// MarshalJSON encodes a digest as a lowercase hexadecimal string.
func (d Digest) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

// UnmarshalJSON decodes a hexadecimal digest string.
func (d *Digest) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := ParseDigest(s)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// Device is one entry in an account's device list. RemovedVersion is nil for
// an active device and set exactly once when the device is removed. A removed
// entry remains in the list as a tombstone forever.
type Device struct {
	DeviceID       DeviceID          `json:"deviceID"`
	IdentityKey    ed25519.PublicKey `json:"identityPublicKey"`
	AddedVersion   Version           `json:"addedVersion"`
	RemovedVersion *Version          `json:"removedVersion,omitempty"`
}

// DeviceEntry is an alias kept for callers that use the wording "entry".
type DeviceEntry = Device

// AccountState is the complete signed state of an account's device list.
//
// Devices must be sorted by DeviceID. PreviousDigest commits to the complete
// previous list; it is ZeroDigest for version 1.
type AccountState struct {
	AccountID      AccountID `json:"accountID"`
	Version        Version   `json:"version"`
	Devices        []Device  `json:"devices"`
	PreviousDigest Digest    `json:"previousDigest"`
}

// DeviceList and ListState are aliases for AccountState.
type DeviceList = AccountState
type ListState = AccountState

// Active reports whether a device is present and has not been removed.
func (d Device) Active() bool {
	return d.RemovedVersion == nil
}

// Clone returns a deep copy.
func (d Device) Clone() Device {
	out := d
	out.IdentityKey = append(ed25519.PublicKey(nil), d.IdentityKey...)
	if d.RemovedVersion != nil {
		v := *d.RemovedVersion
		out.RemovedVersion = &v
	}
	return out
}

// Clone returns a deep copy of the account state.
func (s AccountState) Clone() AccountState {
	out := s
	out.Devices = make([]Device, len(s.Devices))
	for i := range s.Devices {
		out.Devices[i] = s.Devices[i].Clone()
	}
	return out
}

// Validate checks the static invariants of an account state. It does not check
// a signature or a transition from a previous state.
func (s AccountState) Validate() error {
	if strings.TrimSpace(string(s.AccountID)) == "" {
		return ErrInvalidAccountID
	}
	if s.Version == 0 {
		return fmt.Errorf("%w: version 0 is reserved for a fresh client", ErrInvalidVersion)
	}
	if uint64(len(s.Devices)) > uint64(^uint32(0)) {
		return fmt.Errorf("%w: too many devices", ErrInvalidVersion)
	}
	for i := range s.Devices {
		d := s.Devices[i]
		if strings.TrimSpace(string(d.DeviceID)) == "" {
			return fmt.Errorf("%w at index %d", ErrInvalidDeviceID, i)
		}
		if len(d.IdentityKey) != ed25519.PublicKeySize {
			return fmt.Errorf("%w for device %q", ErrInvalidIdentityKey, d.DeviceID)
		}
		if d.AddedVersion == 0 || d.AddedVersion > s.Version {
			return fmt.Errorf("%w for device %q", ErrInvalidAddedVersion, d.DeviceID)
		}
		if d.RemovedVersion != nil {
			if *d.RemovedVersion <= d.AddedVersion || *d.RemovedVersion > s.Version {
				return fmt.Errorf("%w for device %q", ErrInvalidRemovedVersion, d.DeviceID)
			}
		}
		if i > 0 {
			prev := s.Devices[i-1]
			if d.DeviceID == prev.DeviceID {
				return fmt.Errorf("%w: %q", ErrDuplicateDeviceID, d.DeviceID)
			}
			if d.DeviceID < prev.DeviceID {
				return fmt.Errorf("%w: %q appears after %q", ErrUnsortedDevices, d.DeviceID, prev.DeviceID)
			}
		}
	}
	return nil
}

// ValidateGenesis checks that a state can initialize a client that starts at
// version zero. Such a state must be version 1, have no previous list, and
// contain only devices added in version 1.
func (s AccountState) ValidateGenesis() error {
	if err := s.Validate(); err != nil {
		return err
	}
	if s.Version != 1 {
		return fmt.Errorf("%w: genesis list has version %d", ErrVersionJump, s.Version)
	}
	if s.PreviousDigest != ZeroDigest {
		return fmt.Errorf("%w: genesis list has a non-zero previous digest", ErrPreviousDigest)
	}
	for _, d := range s.Devices {
		if d.AddedVersion != 1 || d.RemovedVersion != nil {
			return fmt.Errorf("%w: genesis device %q is not a version-1 active device", ErrInvalidAddedVersion, d.DeviceID)
		}
	}
	return nil
}

// DevicesByID returns a copy of the devices keyed by DeviceID.
func (s AccountState) DevicesByID() map[DeviceID]Device {
	out := make(map[DeviceID]Device, len(s.Devices))
	for _, d := range s.Devices {
		out[d.DeviceID] = d.Clone()
	}
	return out
}

// Device returns the device with the requested ID.
func (s AccountState) Device(id DeviceID) (Device, bool) {
	for _, d := range s.Devices {
		if d.DeviceID == id {
			return d.Clone(), true
		}
	}
	return Device{}, false
}

// ActiveDevice returns the active device with the requested ID.
func (s AccountState) ActiveDevice(id DeviceID) (Device, bool) {
	d, ok := s.Device(id)
	if !ok || !d.Active() {
		return Device{}, false
	}
	return d, true
}

const deviceListDomain = "netsec/device-list/v1"

func appendLengthPrefixed(dst []byte, value []byte) []byte {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	dst = append(dst, length[:]...)
	return append(dst, value...)
}

// CanonicalBytes returns the deterministic byte representation covered by the
// account master signature and hashed to obtain the list digest.
//
// The representation is domain-separated and length-prefixed. Fields are:
// account ID, version, previous digest, device count, and for each sorted
// device: ID, identity key, added version, removal-present flag, removal
// version (when present).
func (s AccountState) CanonicalBytes() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	dst := make([]byte, 0, 128+len(s.Devices)*96)
	dst = appendLengthPrefixed(dst, []byte(deviceListDomain))
	dst = appendLengthPrefixed(dst, []byte(s.AccountID))

	var version [8]byte
	binary.BigEndian.PutUint64(version[:], uint64(s.Version))
	dst = append(dst, version[:]...)
	dst = append(dst, s.PreviousDigest[:]...)

	var count [4]byte
	binary.BigEndian.PutUint32(count[:], uint32(len(s.Devices)))
	dst = append(dst, count[:]...)
	for _, d := range s.Devices {
		dst = appendLengthPrefixed(dst, []byte(d.DeviceID))
		dst = appendLengthPrefixed(dst, d.IdentityKey)
		binary.BigEndian.PutUint64(version[:], uint64(d.AddedVersion))
		dst = append(dst, version[:]...)
		if d.RemovedVersion == nil {
			dst = append(dst, 0)
			continue
		}
		dst = append(dst, 1)
		binary.BigEndian.PutUint64(version[:], uint64(*d.RemovedVersion))
		dst = append(dst, version[:]...)
	}
	return dst, nil
}

// Digest returns the SHA-256 digest of CanonicalBytes.
func (s AccountState) Digest() (Digest, error) {
	canonical, err := s.CanonicalBytes()
	if err != nil {
		return Digest{}, err
	}
	return sha256.Sum256(canonical), nil
}

// ListDigest is a convenience alias for Digest.
func (s AccountState) ListDigest() (Digest, error) {
	return s.Digest()
}

// NewAccountState constructs a sorted, validated account state. The supplied
// devices are cloned and sorted in place in the returned value.
func NewAccountState(accountID AccountID, version Version, previous Digest, devices ...Device) (AccountState, error) {
	state := AccountState{
		AccountID:      accountID,
		Version:        version,
		Devices:        make([]Device, len(devices)),
		PreviousDigest: previous,
	}
	for i := range devices {
		state.Devices[i] = devices[i].Clone()
	}
	sort.Slice(state.Devices, func(i, j int) bool {
		return state.Devices[i].DeviceID < state.Devices[j].DeviceID
	})
	if err := state.Validate(); err != nil {
		return AccountState{}, err
	}
	return state, nil
}

// NewDevice constructs a device entry.
func NewDevice(id DeviceID, key ed25519.PublicKey, added Version) Device {
	return Device{
		DeviceID:     id,
		IdentityKey:  append(ed25519.PublicKey(nil), key...),
		AddedVersion: added,
	}
}

// ValidateTransition checks that next is exactly the successor of prev and
// that every device follows the append-only key and tombstone rules.
func ValidateTransition(prev, next AccountState) error {
	if err := prev.Validate(); err != nil {
		return fmt.Errorf("previous state: %w", err)
	}
	if err := next.Validate(); err != nil {
		return err
	}
	if next.AccountID != prev.AccountID {
		return ErrAccountMismatch
	}
	if prev.Version == ^Version(0) || next.Version != prev.Version+1 {
		return fmt.Errorf("%w: previous %d, next %d", ErrVersionJump, prev.Version, next.Version)
	}
	prevDigest, err := prev.Digest()
	if err != nil {
		return err
	}
	if next.PreviousDigest != prevDigest {
		return fmt.Errorf("%w: expected %s, got %s", ErrPreviousDigest, prevDigest, next.PreviousDigest)
	}

	oldByID := prev.DevicesByID()
	newByID := next.DevicesByID()
	for id, oldDevice := range oldByID {
		newDevice, ok := newByID[id]
		if !ok {
			return fmt.Errorf("%w: device %q disappeared", ErrMissingDevice, id)
		}
		if oldDevice.RemovedVersion != nil {
			if newDevice.RemovedVersion == nil ||
				*newDevice.RemovedVersion != *oldDevice.RemovedVersion ||
				newDevice.AddedVersion != oldDevice.AddedVersion ||
				!bytes.Equal(newDevice.IdentityKey, oldDevice.IdentityKey) {
				return fmt.Errorf("%w: device %q", ErrDeviceIDReuse, id)
			}
			continue
		}
		if !bytes.Equal(newDevice.IdentityKey, oldDevice.IdentityKey) {
			return fmt.Errorf("%w: device %q", ErrDeviceKeyChanged, id)
		}
		if newDevice.AddedVersion != oldDevice.AddedVersion {
			return fmt.Errorf("%w: device %q changed its added version", ErrInvalidAddedVersion, id)
		}
		if newDevice.RemovedVersion != nil {
			if *newDevice.RemovedVersion != next.Version {
				return fmt.Errorf("%w: device %q removal is stamped %d, list is %d",
					ErrInvalidRemovedVersion, id, *newDevice.RemovedVersion, next.Version)
			}
		}
	}

	for id, newDevice := range newByID {
		if _, existed := oldByID[id]; existed {
			continue
		}
		if newDevice.AddedVersion != next.Version {
			return fmt.Errorf("%w: device %q added in %d, list is %d",
				ErrInvalidAddedVersion, id, newDevice.AddedVersion, next.Version)
		}
		if newDevice.RemovedVersion != nil {
			return fmt.Errorf("%w: device %q cannot be added and removed in one version",
				ErrInvalidRemovedVersion, id)
		}
	}
	return nil
}

// FindDevice is a convenience helper over a slice of devices.
func FindDevice(devices []Device, id DeviceID) (Device, bool) {
	for _, d := range devices {
		if d.DeviceID == id {
			return d.Clone(), true
		}
	}
	return Device{}, false
}

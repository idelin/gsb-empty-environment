package netsec

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// SlotID identifies one of the two firmware boot slots.
type SlotID uint8

const (
	SlotA SlotID = iota
	SlotB

	SlotCount = 2
)

func (s SlotID) Valid() bool {
	return s == SlotA || s == SlotB
}

func (s SlotID) String() string {
	switch s {
	case SlotA:
		return "A"
	case SlotB:
		return "B"
	default:
		return fmt.Sprintf("slot(%d)", uint8(s))
	}
}

// Other returns the other slot. It panics for an invalid slot so an invalid
// state can never accidentally select a slot to overwrite.
func (s SlotID) Other() SlotID {
	switch s {
	case SlotA:
		return SlotB
	case SlotB:
		return SlotA
	default:
		panic("netsec: invalid slot")
	}
}

// Manifest is the security-relevant, slot-independent description of an
// image. The same image bytes must have the same digest in either slot.
type Manifest struct {
	DeviceModel    string `json:"deviceModel"`
	ImageDigest    string `json:"imageDigest"`
	RollbackIndex  uint32 `json:"rollbackIndex"`
	MinBootVersion uint32 `json:"minBootVersion"`
}

// ManifestVerifier authenticates the signature over manifest and confirms
// that it belongs to the supplied image. The callback must reject an
// unauthenticated manifest. The state machine independently checks the
// device model, image digest, rollback index, and minimum boot version so a
// compromised ordinary-state snapshot cannot change those bindings.
type ManifestVerifier func(manifest Manifest, signature, image []byte) error

// SlotState is the ordinary persistent state for one slot.
type SlotState struct {
	ImageDigest       string   `json:"imageDigest,omitempty"`
	Manifest          Manifest `json:"manifest,omitempty"`
	ManifestSignature []byte   `json:"manifestSignature,omitempty"`
	VersionIndex      uint32   `json:"versionIndex,omitempty"`
	Candidate         bool     `json:"candidate,omitempty"`
	Success           bool     `json:"success,omitempty"`
	Attempts          uint32   `json:"attempts,omitempty"`
}

// State is the ordinary persistent state. It can be lost or replaced with an
// old snapshot at any write boundary; it is never used to lower the fuse.
type State struct {
	SchemaVersion uint16               `json:"schemaVersion"`
	Active        SlotID               `json:"active"`
	Slots         [SlotCount]SlotState `json:"slots"`
}

const stateSchemaVersion uint16 = 1

func emptyState(active SlotID) State {
	if !active.Valid() {
		active = SlotA
	}
	return State{
		SchemaVersion: stateSchemaVersion,
		Active:        active,
	}
}

// DigestImage returns the canonical, slot-independent SHA-256 digest used in
// manifests and ordinary state.
func DigestImage(image []byte) string {
	sum := sha256.Sum256(image)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// NormalizeDigest accepts either a bare SHA-256 hex digest or the canonical
// "sha256:<hex>" form and returns the canonical representation. It rejects
// all other digest algorithms.
func NormalizeDigest(digest string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(digest))
	s = strings.TrimPrefix(s, "sha256:")
	if len(s) != sha256.Size*2 {
		return "", fmt.Errorf("%w: expected a SHA-256 digest", ErrInvalidManifest)
	}
	if _, err := hex.DecodeString(s); err != nil {
		return "", fmt.Errorf("%w: invalid SHA-256 digest", ErrInvalidManifest)
	}
	return "sha256:" + s, nil
}

func digestsEqual(a, b string) bool {
	na, errA := NormalizeDigest(a)
	nb, errB := NormalizeDigest(b)
	if errA != nil || errB != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(na), []byte(nb)) == 1
}

// Errors returned by the state machine. Callers can use errors.Is to
// distinguish policy failures from storage failures.
var (
	ErrNoState             = errors.New("netsec: no persistent state")
	ErrInvalidConfig       = errors.New("netsec: invalid configuration")
	ErrInvalidManifest     = errors.New("netsec: invalid manifest")
	ErrManifestRejected    = errors.New("netsec: manifest signature rejected")
	ErrDeviceMismatch      = errors.New("netsec: manifest device model mismatch")
	ErrImageDigestMismatch = errors.New("netsec: image digest mismatch")
	ErrMinBootVersion      = errors.New("netsec: minimum boot version not met")
	ErrRollback            = errors.New("netsec: rollback attempt rejected")
	ErrSameIndexDigest     = errors.New("netsec: same-index different-digest policy rejection")
	ErrImageWrite          = errors.New("netsec: image write failed")
	ErrImageReadback       = errors.New("netsec: image readback failed")
	ErrStateRead           = errors.New("netsec: ordinary state read failed")
	ErrStateWrite          = errors.New("netsec: ordinary state write failed")
	ErrFuseRead            = errors.New("netsec: trusted fuse read failed")
	ErrFuseRaise           = errors.New("netsec: trusted fuse raise failed")
	ErrFuseVerify          = errors.New("netsec: trusted fuse verification failed")
	ErrNoBootTarget        = errors.New("netsec: no bootable slot at or above rollback index")
	ErrNoBootSession       = errors.New("netsec: no active boot session")
	ErrInvalidNonce        = errors.New("netsec: boot confirmation nonce rejected")
	ErrConfirmation        = errors.New("netsec: boot confirmation does not match boot target")
	ErrInvalidState        = errors.New("netsec: invalid ordinary state")
)

// ErrRecoveryMode means that ordinary state and the fuse leave no bootable
// image. It also matches ErrNoBootTarget.
var ErrRecoveryMode = fmt.Errorf("%w: recovery mode", ErrNoBootTarget)

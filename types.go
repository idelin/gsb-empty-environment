package netsec

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Role names as used in the "_type" field of a signed payload. Verification of
// downloaded metadata always proceeds in this exact order.
const (
	RootRole      = "root"
	TimestampRole = "timestamp"
	SnapshotRole  = "snapshot"
	TargetsRole   = "targets"
)

// Canonical metadata file names on a repository mirror.
const (
	RootFile      = "root.json"
	TimestampFile = "timestamp.json"
	SnapshotFile  = "snapshot.json"
	TargetsFile   = "targets.json"
)

// KeyTypeEd25519 is the only key type supported by this implementation.
const KeyTypeEd25519 = "ed25519"

// Sentinel errors returned by the verifier; callers can match them with errors.Is.
var (
	// ErrMalformed means the metadata cannot be parsed or is internally inconsistent.
	ErrMalformed = errors.New("tuf: malformed metadata")
	// ErrType means the signed payload's "_type" does not match the expected role.
	ErrType = errors.New("tuf: metadata role type mismatch")
	// ErrVersion means a version number is invalid (e.g. root did not advance by one).
	ErrVersion = errors.New("tuf: invalid metadata version")
	// ErrRollback means a replay of older metadata was detected.
	ErrRollback = errors.New("tuf: metadata rollback attempt")
	// ErrExpired means the metadata's expiry is not later than the injected clock.
	ErrExpired = errors.New("tuf: metadata is expired")
	// ErrSignature means the role's signature threshold was not reached.
	ErrSignature = errors.New("tuf: signature threshold not met")
	// ErrHash means a recorded hash did not match the actual bytes.
	ErrHash = errors.New("tuf: hash verification failed")
	// ErrLength means a recorded length did not match the actual byte length.
	ErrLength = errors.New("tuf: length verification failed")
	// ErrUnavailable means a prerequisite role metadata has not been installed yet.
	ErrUnavailable = errors.New("tuf: role metadata not available")
	// ErrNotFound means the requested target is absent from targets metadata.
	ErrNotFound = errors.New("tuf: target not found")
	// ErrUnsupported means an unsupported key or hash algorithm was encountered.
	ErrUnsupported = errors.New("tuf: unsupported key or hash algorithm")
)

// Envelope is the outer signed container exchanged with the mirror.
type Envelope struct {
	Signatures []Signature     `json:"signatures"`
	Signed     json.RawMessage `json:"signed"`
}

// Signature pairs a key ID with the hex-encoded signature over the canonical
// JSON of the envelope's signed payload.
type Signature struct {
	KeyID string `json:"keyid"`
	Sig   string `json:"sig"`
}

// SignedCommon holds the fields shared by every role's signed payload.
type SignedCommon struct {
	Type    string    `json:"_type"`
	Version int64     `json:"version"`
	Expires time.Time `json:"expires"`
}

// Key describes a public key authorized inside root metadata. Value carries the
// hex-encoded Ed25519 public key.
type Key struct {
	Type  string `json:"keytype"`
	Value string `json:"keyval"`
}

// Role binds a role name to its authorized key IDs and signature threshold.
type Role struct {
	KeyIDs    []string `json:"keyids"`
	Threshold int64    `json:"threshold"`
}

// Root is the trust anchor: it lists every public key and the threshold policy
// for each role.
type Root struct {
	SignedCommon
	Keys  map[string]Key  `json:"keys"`
	Roles map[string]Role `json:"roles"`
}

// FileMeta is the version/hash/length record one role keeps about another file.
type FileMeta struct {
	Hashes  map[string]string `json:"hashes,omitempty"`
	Length  *int64            `json:"length,omitempty"`
	Version *int64            `json:"version,omitempty"`
}

// Timestamp pins the snapshot file.
type Timestamp struct {
	SignedCommon
	Meta map[string]FileMeta `json:"meta"`
}

// Snapshot pins every targets-style metadata file, at minimum targets.json.
type Snapshot struct {
	SignedCommon
	Meta map[string]FileMeta `json:"meta"`
}

// Targets lists the downloadable target files with their hash and length.
type Targets struct {
	SignedCommon
	Targets map[string]FileMeta `json:"targets"`
}

func (c SignedCommon) roleType() string     { return c.Type }
func (c SignedCommon) version() int64       { return c.Version }
func (c SignedCommon) expiresAt() time.Time { return c.Expires }

// signedMeta is the constraint on the concrete signed payload types.
type signedMeta interface {
	roleType() string
	version() int64
	expiresAt() time.Time
}

// canonical serializes a value into deterministic JSON: object keys are sorted
// recursively and insignificant whitespace is removed. Signatures and key IDs
// are computed over this exact byte sequence.
func canonical(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%w: canonical marshal: %v", ErrMalformed, err)
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("%w: canonical unmarshal: %v", ErrMalformed, err)
	}
	out, err := json.Marshal(generic)
	if err != nil {
		return nil, fmt.Errorf("%w: canonical re-marshal: %v", ErrMalformed, err)
	}
	return out, nil
}

// KeyID returns the deterministic identifier of a public key: the hex-encoded
// SHA-256 digest of the key's canonical JSON encoding.
func KeyID(k Key) (string, error) {
	raw, err := canonical(k)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// parseEnvelope decodes an envelope, then decodes and sanity-checks its signed
// payload. It returns the envelope, the typed payload and the exact signed
// bytes that signatures must verify against.
func parseEnvelope[T signedMeta](data []byte, wantRole string) (*Envelope, T, []byte, error) {
	var meta T
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, meta, nil, fmt.Errorf("%w: envelope: %v", ErrMalformed, err)
	}
	if len(env.Signed) == 0 || len(env.Signatures) == 0 {
		return nil, meta, nil, fmt.Errorf("%w: empty signed payload or signatures", ErrMalformed)
	}
	if err := json.Unmarshal(env.Signed, &meta); err != nil {
		return nil, meta, nil, fmt.Errorf("%w: signed payload: %v", ErrMalformed, err)
	}
	if meta.roleType() != wantRole {
		return nil, meta, nil, fmt.Errorf("%w: got %q, want %q", ErrType, meta.roleType(), wantRole)
	}
	if meta.version() < 1 {
		return nil, meta, nil, fmt.Errorf("%w: non-positive version for %q", ErrVersion, wantRole)
	}
	return &env, meta, env.Signed, nil
}

// checkFreshness rejects metadata whose expiry is not strictly later than now.
// An expiry equal to the injected clock is already considered expired.
func checkFreshness(now time.Time, expires time.Time) error {
	if !expires.After(now) {
		return fmt.Errorf("%w: expired at %s, current time %s",
			ErrExpired, expires.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
	}
	return nil
}

// verifyThreshold counts distinct authorized keys that produce a valid
// signature and requires the count to reach the role threshold defined by root.
// Signatures from keys not delegated to the role are ignored.
func verifyThreshold(role string, signed []byte, env *Envelope, root *Root) error {
	roleDef, ok := root.Roles[role]
	if !ok {
		return fmt.Errorf("%w: root does not define role %q", ErrMalformed, role)
	}
	if roleDef.Threshold < 1 {
		return fmt.Errorf("%w: non-positive threshold for role %q", ErrMalformed, role)
	}
	authorized := make(map[string]bool, len(roleDef.KeyIDs))
	for _, id := range roleDef.KeyIDs {
		authorized[id] = true
	}

	valid := make(map[string]bool)
	for _, sig := range env.Signatures {
		if !authorized[sig.KeyID] {
			continue
		}
		key, ok := root.Keys[sig.KeyID]
		if !ok || key.Type != KeyTypeEd25519 {
			continue
		}
		pubBytes, err := hex.DecodeString(key.Value)
		if err != nil || len(pubBytes) != ed25519.PublicKeySize {
			continue
		}
		sigBytes, err := hex.DecodeString(sig.Sig)
		if err != nil {
			continue
		}
		if ed25519.Verify(ed25519.PublicKey(pubBytes), signed, sigBytes) {
			valid[sig.KeyID] = true
		}
	}
	if int64(len(valid)) < roleDef.Threshold {
		return fmt.Errorf("%w: role %q reached %d of %d required signatures",
			ErrSignature, role, len(valid), roleDef.Threshold)
	}
	return nil
}

// verifyFileMeta checks every recorded hash (and the optional length) against
// the actual bytes. At least one usable hash is required.
func verifyFileMeta(meta FileMeta, data []byte) error {
	matched := false
	for alg, want := range meta.Hashes {
		got, err := hashHex(alg, data)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("%w: %s mismatch: want %s, got %s", ErrHash, alg, want, got)
		}
		matched = true
	}
	if !matched {
		return fmt.Errorf("%w: no usable hash recorded", ErrHash)
	}
	if meta.Length != nil && *meta.Length != int64(len(data)) {
		return fmt.Errorf("%w: want %d bytes, got %d", ErrLength, *meta.Length, len(data))
	}
	return nil
}

func hashHex(alg string, data []byte) (string, error) {
	switch alg {
	case "sha256":
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:]), nil
	default:
		return "", fmt.Errorf("%w: hash algorithm %q", ErrUnsupported, alg)
	}
}

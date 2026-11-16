package netsec

import (
	"crypto/ed25519"
	"crypto/hmac"
	"errors"
	"fmt"
	"sync"
)

// FileAttributes is the security-relevant state of a file inode at the moment
// access is requested. UID, GID and Mode are the raw numeric values from the
// inode (for example from syscall.Stat_t on Linux), not Go's os.FileMode.
type FileAttributes struct {
	IMA        []byte
	SELinux    []byte
	Inode      uint64
	Generation uint32
	UID        uint32
	GID        uint32
	Mode       uint32
}

// Status is the outcome of evaluating a security.evm value.
type Status uint8

const (
	// StatusUnspecified is the zero value. It is not an allow decision.
	StatusUnspecified Status = iota

	// StatusAllowed means the value authenticated against the supplied
	// attributes and the required key was loaded.
	StatusAllowed

	// StatusKeyUnavailable means the key required by the type byte was not
	// loaded. Access must be denied.
	StatusKeyUnavailable

	// StatusUnknownType means the security.evm type byte is not supported.
	StatusUnknownType

	// StatusMalformed means the security.evm value has an invalid length or
	// encoding. Access must be denied.
	StatusMalformed

	// StatusMismatch means a native HMAC did not match. The bound attribute
	// values, inode, i_generation, uid, gid or mode changed.
	StatusMismatch

	// StatusInvalidSignature means a portable signature did not verify under
	// any trusted public key.
	StatusInvalidSignature
)

func (s Status) String() string {
	switch s {
	case StatusUnspecified:
		return "unspecified"
	case StatusAllowed:
		return "allowed"
	case StatusKeyUnavailable:
		return "key-unavailable"
	case StatusUnknownType:
		return "unknown-type"
	case StatusMalformed:
		return "malformed"
	case StatusMismatch:
		return "mismatch"
	case StatusInvalidSignature:
		return "invalid-signature"
	default:
		return fmt.Sprintf("status(%d)", uint8(s))
	}
}

// Result describes an evaluation. The zero value is not an allow decision;
// callers must check Allowed or Err rather than relying on the zero value.
type Result struct {
	Status  Status
	EVMType byte
	Detail  string
}

// Allowed reports whether the file may be accessed.
func (r Result) Allowed() bool {
	return r.Status == StatusAllowed
}

var (
	// ErrNotEvaluated is returned for a zero-value Result that was never
	// produced by Evaluate.
	ErrNotEvaluated = errors.New("netsec: EVM value was not evaluated")

	// ErrKeyUnavailable is returned when the key required by the EVM type is
	// not loaded.
	ErrKeyUnavailable = errors.New("netsec: EVM verification key is not loaded")

	// ErrUnknownType is returned for an unsupported security.evm type byte.
	ErrUnknownType = errors.New("netsec: unknown security.evm type")

	// ErrMalformedXattr is returned for a truncated or otherwise invalid
	// security.evm value.
	ErrMalformedXattr = errors.New("netsec: malformed security.evm attribute")

	// ErrIntegrityMismatch is returned when a native HMAC does not match the
	// supplied file attributes and inode identity.
	ErrIntegrityMismatch = errors.New("netsec: EVM integrity mismatch")

	// ErrInvalidSignature is returned when a portable signature does not verify.
	ErrInvalidSignature = errors.New("netsec: invalid EVM portable signature")
)

// Err returns nil for an allowed result and a sentinel error otherwise.
func (r Result) Err() error {
	var err error
	switch r.Status {
	case StatusUnspecified:
		err = ErrNotEvaluated
	case StatusAllowed:
		return nil
	case StatusKeyUnavailable:
		err = ErrKeyUnavailable
	case StatusUnknownType:
		err = ErrUnknownType
	case StatusMalformed:
		err = ErrMalformedXattr
	case StatusMismatch:
		err = ErrIntegrityMismatch
	case StatusInvalidSignature:
		err = ErrInvalidSignature
	default:
		return fmt.Errorf("netsec: unexpected evaluation status %d", uint8(r.Status))
	}
	if r.Detail == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, r.Detail)
}

// Config supplies the keys used by an Evaluator. Empty key material is
// permitted and produces an evaluator that denies the corresponding EVM type;
// this lets the key be supplied later with SetHMACKey or AddTrustedPublicKey.
type Config struct {
	// HMACKey is the host-local EVM HMAC key.
	HMACKey []byte

	// TrustedPublicKeys are Ed25519 keys trusted to sign portable EVM values.
	TrustedPublicKeys []ed25519.PublicKey
}

// Evaluator verifies security.evm values against file attributes. It is safe
// for concurrent use; key updates are atomic with respect to evaluation.
type Evaluator struct {
	mu          sync.RWMutex
	hmacKey     []byte
	trustedKeys []ed25519.PublicKey
}

// NewEvaluator returns an evaluator configured with cfg. Invalid Ed25519 public
// key lengths are rejected; a missing key is not an error and makes the
// evaluator deny the corresponding type.
func NewEvaluator(cfg Config) (*Evaluator, error) {
	e := &Evaluator{}
	if len(cfg.HMACKey) > 0 {
		e.hmacKey = append([]byte(nil), cfg.HMACKey...)
	}
	for i, key := range cfg.TrustedPublicKeys {
		if len(key) != ed25519.PublicKeySize {
			return nil, fmt.Errorf(
				"netsec: trusted public key %d has length %d, want %d",
				i, len(key), ed25519.PublicKeySize,
			)
		}
		e.trustedKeys = append(e.trustedKeys, append(ed25519.PublicKey(nil), key...))
	}
	return e, nil
}

// SetHMACKey installs or clears the host HMAC key. A nil or empty key restores
// fail-closed behavior for type-2 values.
func (e *Evaluator) SetHMACKey(key []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(key) == 0 {
		e.hmacKey = nil
		return
	}
	e.hmacKey = append([]byte(nil), key...)
}

// AddTrustedPublicKey adds an Ed25519 trust anchor for portable signatures.
// Duplicate keys are harmless.
func (e *Evaluator) AddTrustedPublicKey(pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("netsec: invalid Ed25519 public key length %d", len(pub))
	}
	key := append(ed25519.PublicKey(nil), pub...)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.trustedKeys = append(e.trustedKeys, key)
	return nil
}

// Evaluate authenticates evmXattr against attrs. A successful result is the
// only outcome that permits access; all other statuses must deny it.
//
// A hard link to another path has the same inode and i_generation, so it
// evaluates identically to the original path. Copying a type-2 value to a file
// with another inode changes the authenticated identity and is rejected.
// Type-5 values are portable by design and therefore do not bind an inode.
func (e *Evaluator) Evaluate(attrs FileAttributes, evmXattr []byte) Result {
	if len(evmXattr) == 0 {
		return Result{Status: StatusMalformed, Detail: "empty security.evm value"}
	}

	evmType := evmXattr[0]
	switch evmType {
	case EVMTypeHMAC:
		key := e.hmacKeySnapshot()
		if len(key) == 0 {
			return Result{
				Status:  StatusKeyUnavailable,
				EVMType: evmType,
				Detail:  "host HMAC key is not loaded",
			}
		}
		if len(evmXattr) != 1+HMACDigestSize {
			return Result{
				Status:  StatusMalformed,
				EVMType: evmType,
				Detail: fmt.Sprintf(
					"type-2 value has length %d, want %d",
					len(evmXattr), 1+HMACDigestSize,
				),
			}
		}
		want := ComputeHMAC(key, attrs)
		if !hmac.Equal(want, evmXattr[1:]) {
			return Result{
				Status:  StatusMismatch,
				EVMType: evmType,
				Detail:  "HMAC does not match attributes or inode identity",
			}
		}
		return Result{Status: StatusAllowed, EVMType: evmType}

	case EVMTypePortableSignature:
		keys := e.trustedKeySnapshot()
		if len(keys) == 0 {
			return Result{
				Status:  StatusKeyUnavailable,
				EVMType: evmType,
				Detail:  "portable signature trust key is not loaded",
			}
		}
		if len(evmXattr) != 1+PortableSignatureSize {
			return Result{
				Status:  StatusMalformed,
				EVMType: evmType,
				Detail: fmt.Sprintf(
					"type-5 value has length %d, want %d",
					len(evmXattr), 1+PortableSignatureSize,
				),
			}
		}
		payload := PortableSignaturePayload(attrs)
		signature := evmXattr[1:]
		for _, key := range keys {
			if ed25519.Verify(key, payload, signature) {
				return Result{Status: StatusAllowed, EVMType: evmType}
			}
		}
		return Result{
			Status:  StatusInvalidSignature,
			EVMType: evmType,
			Detail:  "signature did not verify under any trusted key",
		}

	default:
		return Result{
			Status:  StatusUnknownType,
			EVMType: evmType,
			Detail:  fmt.Sprintf("unsupported type byte 0x%02x", evmType),
		}
	}
}

// Verify is the error-returning form of Evaluate.
func (e *Evaluator) Verify(attrs FileAttributes, evmXattr []byte) error {
	return e.Evaluate(attrs, evmXattr).Err()
}

func (e *Evaluator) hmacKeySnapshot() []byte {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return append([]byte(nil), e.hmacKey...)
}

func (e *Evaluator) trustedKeySnapshot() []ed25519.PublicKey {
	e.mu.RLock()
	defer e.mu.RUnlock()
	keys := make([]ed25519.PublicKey, len(e.trustedKeys))
	for i, key := range e.trustedKeys {
		keys[i] = append(ed25519.PublicKey(nil), key...)
	}
	return keys
}

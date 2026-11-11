package approval

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/binary"
	"errors"
	"time"

	_ "crypto/sha256" // register SHA-256 for crypto.Hash.Sum
	_ "crypto/sha512" // register SHA-384 for crypto.Hash.Sum
)

// signingDomain is a fixed prefix that separates approval-request signatures
// from any other use of the same keys, so a signature produced for a different
// protocol cannot be replayed here.
const signingDomain = "GSB-DEVICE-UPDATE/v1"

// Thresholds states how many distinct key materials must validly sign for each
// role. A zero threshold means that role contributes no requirement.
type Thresholds struct {
	Admin  int
	Device int
}

// Result reports the tally of distinct key materials after every valid
// signature has been counted once. Counts never exceed the number of distinct
// public keys, regardless of alias reuse or repeated signatures.
type Result struct {
	Admin  int
	Device int
}

// SigningBytes returns the exact byte string a signature must cover.
//
// Every variable-length field is length-prefixed and the field order is fixed,
// so two requests with concatenated field values cannot collide. The deadline
// is encoded as its absolute Unix second plus nanosecond, which is independent
// of time-zone presentation.
func SigningBytes(req UpdateRequest) []byte {
	out := make([]byte, 0, len(signingDomain)+len(req.Version)+len(req.DeviceGroup)+len(req.OperationSummary)+32)
	out = append(out, signingDomain...)
	out = appendLengthPrefixed(out, req.Version)
	out = appendLengthPrefixed(out, req.DeviceGroup)
	out = appendLengthPrefixed(out, req.OperationSummary)

	var deadline [12]byte
	binary.BigEndian.PutUint64(deadline[:8], uint64(req.Deadline.Unix()))
	binary.BigEndian.PutUint32(deadline[8:], uint32(req.Deadline.Nanosecond()))
	out = append(out, deadline[:]...)
	return out
}

func appendLengthPrefixed(dst []byte, field string) []byte {
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(field)))
	dst = append(dst, prefix[:]...)
	return append(dst, field...)
}

// Sign produces a signature over req with a standard-library private key.
//
// It is a convenience for senders and tests; the receiving service only needs
// Verify. priv must match alg: *ed25519.PrivateKey, *ecdsa.PrivateKey or
// *rsa.PrivateKey. No custom cryptography is introduced.
func Sign(priv crypto.PrivateKey, alg Algorithm, req UpdateRequest) ([]byte, error) {
	if !alg.Known() {
		return nil, ErrUnknownAlgorithm
	}
	message := SigningBytes(req)

	switch key := priv.(type) {
	case ed25519.PrivateKey:
		if alg != AlgEd25519 {
			return nil, ErrUnknownAlgorithm
		}
		return ed25519.Sign(key, message), nil
	case *ecdsa.PrivateKey:
		hash, ok := ecdsaHash(alg)
		if !ok {
			return nil, ErrUnknownAlgorithm
		}
		digest := digestMessage(hash, message)
		return ecdsa.SignASN1(rand.Reader, key, digest[:])
	case *rsa.PrivateKey:
		hash, ok := rsaHash(alg)
		if !ok {
			return nil, ErrUnknownAlgorithm
		}
		digest := digestMessage(hash, message)
		return rsa.SignPSS(rand.Reader, key, hash, digest[:], &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
	default:
		return nil, ErrInvalidKey
	}
}

// Verify authenticates every signature, deduplicates by public-key material,
// enforces the deadline and then applies the per-role thresholds.
//
// now is supplied by the caller (typically time.Now) so the expiry boundary is
// testable. A request at exactly now == Deadline is still accepted; any later
// instant is expired.
func Verify(now time.Time, req UpdateRequest, keys *KeySet, policy Thresholds) (Result, error) {
	if keys == nil {
		return Result{}, errors.New("approval: nil key set")
	}
	if policy.Admin < 0 || policy.Device < 0 {
		return Result{}, ErrPolicy
	}
	if req.Version == "" || req.DeviceGroup == "" || req.OperationSummary == "" || req.Deadline.IsZero() {
		return Result{}, ErrInvalidRequest
	}
	if len(req.Signatures) == 0 {
		return Result{}, ErrInvalidRequest
	}

	message := SigningBytes(req)
	seen := make(map[string]Role)

	for _, sig := range req.Signatures {
		entry, ok := keys.Lookup(sig.Alias)
		if !ok {
			return Result{}, ErrUnknownKey
		}
		if !sig.Algorithm.Known() || sig.Algorithm != entry.Algorithm {
			return Result{}, ErrUnknownAlgorithm
		}
		if err := verifyOne(entry, message, sig.Value); err != nil {
			return Result{}, err
		}
		material, err := canonicalKey(entry.PublicKey)
		if err != nil {
			return Result{}, err
		}
		// First valid signature for this key material wins; repeated entries,
		// duplicate aliases and claimed roles cannot add another vote.
		if _, counted := seen[material]; !counted {
			seen[material] = entry.Role
		}
	}

	if now.After(req.Deadline) {
		return Result{}, ErrExpired
	}

	var result Result
	for _, role := range seen {
		switch role {
		case RoleAdmin:
			result.Admin++
		case RoleDevice:
			result.Device++
		}
	}

	if result.Admin < policy.Admin || result.Device < policy.Device {
		return result, ErrUnauthorized
	}
	return result, nil
}

func verifyOne(entry TrustedKey, message, signature []byte) error {
	switch key := entry.PublicKey.(type) {
	case ed25519.PublicKey:
		if entry.Algorithm != AlgEd25519 || !ed25519.Verify(key, message, signature) {
			return ErrBadSignature
		}
		return nil
	case *ecdsa.PublicKey:
		hash, ok := ecdsaHash(entry.Algorithm)
		if !ok {
			return ErrUnknownAlgorithm
		}
		if !curveMatches(key, entry.Algorithm) {
			return ErrUnknownAlgorithm
		}
		digest := digestMessage(hash, message)
		if !ecdsa.VerifyASN1(key, digest[:], signature) {
			return ErrBadSignature
		}
		return nil
	case *rsa.PublicKey:
		hash, ok := rsaHash(entry.Algorithm)
		if !ok {
			return ErrUnknownAlgorithm
		}
		digest := digestMessage(hash, message)
		if err := rsa.VerifyPSS(key, hash, digest[:], signature, nil); err != nil {
			return ErrBadSignature
		}
		return nil
	default:
		return ErrInvalidKey
	}
}

func checkKey(pub any, alg Algorithm) error {
	switch key := pub.(type) {
	case ed25519.PublicKey:
		if len(key) != ed25519.PublicKeySize || alg != AlgEd25519 {
			return ErrInvalidKey
		}
		return nil
	case *ecdsa.PublicKey:
		if !curveMatches(key, alg) {
			return ErrInvalidKey
		}
		return nil
	case *rsa.PublicKey:
		if _, ok := rsaHash(alg); !ok || key.Size() == 0 {
			return ErrInvalidKey
		}
		return nil
	default:
		return ErrInvalidKey
	}
}

func curveMatches(key *ecdsa.PublicKey, alg Algorithm) bool {
	switch alg {
	case AlgES256:
		return key.Curve.Params().Name == "P-256"
	case AlgES384:
		return key.Curve.Params().Name == "P-384"
	default:
		return false
	}
}

func ecdsaHash(alg Algorithm) (crypto.Hash, bool) {
	switch alg {
	case AlgES256:
		return crypto.SHA256, true
	case AlgES384:
		return crypto.SHA384, true
	default:
		return 0, false
	}
}

func rsaHash(alg Algorithm) (crypto.Hash, bool) {
	switch alg {
	case AlgPS256:
		return crypto.SHA256, true
	case AlgPS384:
		return crypto.SHA384, true
	default:
		return 0, false
	}
}

// digestMessage hashes message with a standard-library hash algorithm.
func digestMessage(hash crypto.Hash, message []byte) []byte {
	h := hash.New()
	h.Write(message)
	return h.Sum(nil)
}

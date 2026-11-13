package netsec

import (
	"fmt"
)

// DesignatedRevokers returns the revocation keys declared by valid
// self-signatures over the entity's primary key.  Only Revocation Key
// subpackets from the hashed (signed) area are considered.
func (e *Entity) DesignatedRevokers() []RevocationKey {
	if e == nil || e.PrimaryKey == nil {
		return nil
	}

	var out []RevocationKey
	seen := make(map[string]bool)
	addSignature := func(sig *Signature, signedData []byte) {
		if err := verifySignature(e.PrimaryKey, sig, signedData); err != nil {
			return
		}
		if !hashedIssuerMatches(sig, e.PrimaryKey) {
			return
		}
		for _, revoker := range sig.RevocationKeys() {
			if revoker.Class&0x80 == 0 {
				continue
			}
			if len(revoker.Fingerprint) != 20 && len(revoker.Fingerprint) != 32 {
				continue
			}
			key := string(revoker.Fingerprint)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, revoker)
		}
	}

	for _, sig := range e.DirectSignatures {
		if sig.Type == SigDirectKey {
			addSignature(sig, canonicalKeyPrefix(e.PrimaryKey.Version, e.PrimaryKey.Body))
		}
	}
	for _, identity := range e.Identities {
		for _, sig := range identity.Signatures {
			switch sig.Type {
			case SigCertGeneric, SigCertPersona, SigCertCasual, SigCertPositive:
			default:
				continue
			}
			signedData := canonicalKeyPrefix(e.PrimaryKey.Version, e.PrimaryKey.Body)
			if identity.UserAttribute != nil {
				signedData = append(signedData, canonicalUserAttributeData(identity.UserAttribute)...)
			} else {
				signedData = append(signedData, canonicalUserIDData([]byte(identity.UserID))...)
			}
			addSignature(sig, signedData)
		}
	}
	return out
}

// DesignatedRevokerFingerprints returns the complete fingerprints of all
// valid designated revokers.
func (e *Entity) DesignatedRevokerFingerprints() [][]byte {
	revokers := e.DesignatedRevokers()
	out := make([][]byte, 0, len(revokers))
	for _, revoker := range revokers {
		out = append(out, append([]byte(nil), revoker.Fingerprint...))
	}
	return out
}

// IsDesignatedRevoker reports whether fingerprint is exactly one of the
// entity's designated revoker fingerprints.  Key IDs and fingerprint prefixes
// never match.
func (e *Entity) IsDesignatedRevoker(fingerprint []byte) bool {
	if e == nil || (len(fingerprint) != 20 && len(fingerprint) != 32) {
		return false
	}
	for _, revoker := range e.DesignatedRevokers() {
		if sameBytes(revoker.Fingerprint, fingerprint) {
			return true
		}
	}
	return false
}

// IsDesignatedRevoker reports whether fingerprint is exactly one of the
// entity's designated revoker fingerprints.
func IsDesignatedRevoker(entity *Entity, fingerprint []byte) bool {
	return entity.IsDesignatedRevoker(fingerprint)
}

// VerifyKeyRevocation verifies that sig is a valid revocation of target's
// primary key.  It accepts a revocation made by the target key itself or by a
// revoker whose complete fingerprint was declared in a valid self-signature.
//
// A matching 64-bit key ID is never sufficient.  If ring contains a
// different key with the same key ID as the designated revoker, that key
// cannot authorize the revocation.
func VerifyKeyRevocation(target *Entity, sig *Signature, ring *Keyring) error {
	if target == nil || target.PrimaryKey == nil {
		return fmt.Errorf("%w: nil target key", ErrInvalidPacket)
	}
	if sig == nil {
		return fmt.Errorf("%w: nil signature", ErrInvalidPacket)
	}
	if sig.Type != SigKeyRevocation {
		return fmt.Errorf("%w: got %#x, want key revocation", ErrWrongSignatureType, uint8(sig.Type))
	}

	signedData := canonicalKeyPrefix(target.PrimaryKey.Version, target.PrimaryKey.Body)

	// A self-revocation is authorized by the key material itself.  Trying it
	// first also handles signatures that omit issuer metadata.
	if err := verifySignature(target.PrimaryKey, sig, signedData); err == nil {
		if hashedIssuerMatches(sig, target.PrimaryKey) {
			return nil
		}
	}

	revokers := target.DesignatedRevokers()
	if len(revokers) == 0 {
		return ErrNotDesignatedRevoker
	}

	candidates := make([]*Key, 0, len(revokers))
	for _, revoker := range revokers {
		keys := ringKeysByFingerprint(ring, revoker.Fingerprint)
		for _, key := range keys {
			if revoker.Algorithm != 0 && key.Algorithm != revoker.Algorithm {
				continue
			}
			candidates = append(candidates, key)
		}
	}
	if len(candidates) == 0 {
		return fmt.Errorf("%w: designated revoker key is not in the keyring", ErrKeyNotFound)
	}

	var lastErr error
	for _, candidate := range candidates {
		if !target.IsDesignatedRevoker(candidate.Fingerprint) {
			continue
		}
		if !hashedIssuerMatches(sig, candidate) {
			lastErr = fmt.Errorf("%w: hashed issuer metadata does not match designated revoker fingerprint", ErrBadSignature)
			continue
		}
		if err := verifySignature(candidate, sig, signedData); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = ErrBadSignature
	}
	return lastErr
}

// VerifyRevocation is an alias for VerifyKeyRevocation.
func VerifyRevocation(target *Entity, sig *Signature, ring *Keyring) error {
	return VerifyKeyRevocation(target, sig, ring)
}

// VerifyDesignatedRevocationWithKey verifies a key revocation signature using
// an explicitly supplied signer.  The signer is accepted only when its complete
// fingerprint is declared by target.  Being the target key itself is not
// sufficient for this function; use VerifyKeyRevocation for self-revocations.
func VerifyDesignatedRevocationWithKey(target *Entity, signer *Key, sig *Signature) error {
	if target == nil || target.PrimaryKey == nil || signer == nil {
		return fmt.Errorf("%w: nil key", ErrInvalidPacket)
	}
	if sig == nil {
		return fmt.Errorf("%w: nil signature", ErrInvalidPacket)
	}
	if sig.Type != SigKeyRevocation {
		return fmt.Errorf("%w: got %#x, want key revocation", ErrWrongSignatureType, uint8(sig.Type))
	}
	if !target.IsDesignatedRevoker(signer.Fingerprint) {
		return ErrNotDesignatedRevoker
	}
	if !hashedIssuerMatches(sig, signer) {
		return fmt.Errorf("%w: hashed issuer metadata does not match designated revoker fingerprint", ErrBadSignature)
	}
	if err := designatedAlgorithmAllows(target, signer); err != nil {
		return err
	}
	signedData := canonicalKeyPrefix(target.PrimaryKey.Version, target.PrimaryKey.Body)
	return verifySignature(signer, sig, signedData)
}

// VerifyDesignatedRevocation is an alias for
// VerifyDesignatedRevocationWithKey.
func VerifyDesignatedRevocation(target *Entity, signer *Key, sig *Signature) error {
	return VerifyDesignatedRevocationWithKey(target, signer, sig)
}

// VerifyKeyRevocationWithKey verifies a key revocation with an explicitly
// supplied signer.  The signer may be the target key itself or a fingerprint-
// matched designated revoker.  A colliding 64-bit key ID is rejected.
func VerifyKeyRevocationWithKey(target *Entity, signer *Key, sig *Signature) error {
	if target == nil || target.PrimaryKey == nil || signer == nil {
		return fmt.Errorf("%w: nil key", ErrInvalidPacket)
	}
	if sig == nil {
		return fmt.Errorf("%w: nil signature", ErrInvalidPacket)
	}
	if sig.Type != SigKeyRevocation {
		return fmt.Errorf("%w: got %#x, want key revocation", ErrWrongSignatureType, uint8(sig.Type))
	}

	signedData := canonicalKeyPrefix(target.PrimaryKey.Version, target.PrimaryKey.Body)
	if sameBytes(signer.Fingerprint, target.PrimaryKey.Fingerprint) {
		if !hashedIssuerMatches(sig, target.PrimaryKey) {
			return fmt.Errorf("%w: hashed issuer metadata does not match target key", ErrBadSignature)
		}
		return verifySignature(target.PrimaryKey, sig, signedData)
	}
	if !target.IsDesignatedRevoker(signer.Fingerprint) {
		return ErrNotDesignatedRevoker
	}
	if !hashedIssuerMatches(sig, signer) {
		return fmt.Errorf("%w: hashed issuer metadata does not match designated revoker fingerprint", ErrBadSignature)
	}
	if err := designatedAlgorithmAllows(target, signer); err != nil {
		return err
	}
	return verifySignature(signer, sig, signedData)
}

// VerifyRevocationWithKey is an alias for VerifyKeyRevocationWithKey.
func VerifyRevocationWithKey(target *Entity, signer *Key, sig *Signature) error {
	return VerifyKeyRevocationWithKey(target, signer, sig)
}

// Revoked reports whether the entity has at least one valid revocation.
func (e *Entity) Revoked(ring *Keyring) (bool, error) {
	if e == nil || e.PrimaryKey == nil {
		return false, fmt.Errorf("%w: nil target key", ErrInvalidPacket)
	}
	var lastErr error
	for _, sig := range e.KeyRevocations {
		if err := VerifyKeyRevocation(e, sig, ring); err == nil {
			return true, nil
		} else {
			lastErr = err
		}
	}
	return false, lastErr
}

// VerifyRevocationPacket parses a detached signature packet and verifies it as
// a primary-key revocation.
func VerifyRevocationPacket(target *Entity, packetData []byte, ring *Keyring) error {
	sig, err := ParseSignature(packetData)
	if err != nil {
		return err
	}
	return VerifyKeyRevocation(target, sig, ring)
}

func hashedIssuerMatches(sig *Signature, key *Key) bool {
	fingerprints := sig.HashedIssuerFingerprints()
	if len(fingerprints) > 0 {
		matched := false
		for _, fingerprint := range fingerprints {
			if sameBytes(fingerprint, key.Fingerprint) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	keyIDs := sig.HashedIssuerKeyIDs()
	if len(keyIDs) > 0 {
		matched := false
		for _, keyID := range keyIDs {
			if sameBytes(keyID, key.KeyID) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func designatedAlgorithmAllows(target *Entity, signer *Key) error {
	for _, revoker := range target.DesignatedRevokers() {
		if !sameBytes(revoker.Fingerprint, signer.Fingerprint) {
			continue
		}
		if revoker.Algorithm != 0 && revoker.Algorithm != signer.Algorithm {
			return fmt.Errorf("%w: designated revoker algorithm %s does not match key algorithm %s",
				ErrBadSignature, revoker.Algorithm, signer.Algorithm)
		}
		return nil
	}
	return ErrNotDesignatedRevoker
}

func ringKeysByFingerprint(ring *Keyring, fingerprint []byte) []*Key {
	if ring == nil {
		return nil
	}
	keys := ring.KeysByFingerprint(fingerprint)
	for _, entity := range ring.Entities {
		for _, subkey := range entity.Subkeys {
			if sameBytes(subkey.Key.Fingerprint, fingerprint) {
				keys = append(keys, subkey.Key)
			}
		}
	}
	return keys
}

// VerifyKeyRevocationBytes verifies a detached revocation signature against a
// target selected from armored or binary keyring data by complete fingerprint.
func VerifyKeyRevocationBytes(keyringData, targetFingerprint, revocationPacket []byte) error {
	ring, err := ParseKeyring(keyringData)
	if err != nil {
		return err
	}
	target := ring.LookupByFingerprint(targetFingerprint)
	if target == nil {
		return ErrKeyNotFound
	}
	return VerifyRevocationPacket(target, revocationPacket, ring)
}

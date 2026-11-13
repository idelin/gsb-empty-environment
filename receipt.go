package netsec

import (
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
)

// Receipt is the HSM-signed evidence returned with an import.
type Receipt struct {
	CeremonyID            string
	Slot                  string
	Algorithm             string
	Purpose               string
	KeyMaterialCommitment [sha256.Size]byte
	WrappingKeyDigest     [sha256.Size]byte
	AttributesDigest      [sha256.Size]byte
	KeyHandle             string
	IssuedAtUnixNano      int64
	Signature             []byte
}

// AttributesDigest is the exact attribute fingerprint the HSM must return.
// It covers all binding fields, including slot, algorithm, purpose, key
// commitment, wrapping-key digest, start/deadline, and policy generation.
func AttributesDigest(binding CeremonyBinding) [sha256.Size]byte {
	payload := canonicalField(nil, []byte("netsec.hsm-import-attributes.v1"))
	payload = bindingSigningFields(payload, binding)
	return sha256.Sum256(payload)
}

// ReceiptSigningPayload is the exact domain-separated receipt byte string.
// HSM implementations sign this payload with their receipt key.
func ReceiptSigningPayload(receipt Receipt) []byte {
	payload := canonicalField(nil, []byte("netsec.hsm-import-receipt.v1"))
	payload = canonicalString(payload, receipt.CeremonyID)
	payload = canonicalString(payload, receipt.Slot)
	payload = canonicalString(payload, receipt.Algorithm)
	payload = canonicalString(payload, receipt.Purpose)
	payload = canonicalDigest(payload, receipt.KeyMaterialCommitment)
	payload = canonicalDigest(payload, receipt.WrappingKeyDigest)
	payload = canonicalDigest(payload, receipt.AttributesDigest)
	payload = canonicalString(payload, receipt.KeyHandle)
	payload = canonicalInt64(payload, receipt.IssuedAtUnixNano)
	return payload
}

func verifyReceipt(binding CeremonyBinding, result ImportResult, receiptKey ed25519.PublicKey) error {
	receipt := result.Receipt
	if receipt.CeremonyID != binding.CeremonyID ||
		receipt.Slot != binding.Slot ||
		receipt.Algorithm != binding.Algorithm ||
		receipt.Purpose != binding.Purpose {
		return fmt.Errorf("%w: receipt identity fields mismatch", ErrImportMismatch)
	}
	if receipt.KeyMaterialCommitment != binding.KeyMaterialCommitment {
		return fmt.Errorf("%w: receipt key commitment mismatch", ErrImportMismatch)
	}
	if receipt.WrappingKeyDigest != binding.WrappingKeyDigest {
		return fmt.Errorf("%w: receipt wrapping-key digest mismatch", ErrImportMismatch)
	}
	expectedAttributes := AttributesDigest(binding)
	if receipt.AttributesDigest != expectedAttributes || result.AttributesDigest != expectedAttributes {
		return fmt.Errorf("%w: receipt attribute digest mismatch", ErrImportMismatch)
	}
	if receipt.KeyHandle == "" || receipt.KeyHandle != result.KeyHandle {
		return fmt.Errorf("%w: receipt key handle mismatch", ErrImportMismatch)
	}
	if receipt.KeyMaterialCommitment != result.KeyMaterialCommitment {
		return fmt.Errorf("%w: receipt/result commitment mismatch", ErrImportMismatch)
	}
	if len(receipt.Signature) == 0 ||
		!ed25519.Verify(receiptKey, ReceiptSigningPayload(receipt), receipt.Signature) {
		return fmt.Errorf("%w: invalid HSM receipt signature", ErrImportMismatch)
	}
	return nil
}

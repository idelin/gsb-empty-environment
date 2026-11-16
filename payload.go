package netsec

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
)

const (
	// EVMTypeHMAC is the security.evm type byte for a host-local HMAC.
	EVMTypeHMAC byte = 0x02

	// EVMTypePortableSignature is the security.evm type byte for a portable
	// public-key signature.
	EVMTypePortableSignature byte = 0x05

	// HMACDigestSize is the size of the HMAC-SHA256 digest.
	HMACDigestSize = sha256.Size

	// PortableSignatureSize is the size of an Ed25519 signature.
	PortableSignatureSize = ed25519.SignatureSize
)

// HMACPayload returns the exact byte sequence authenticated by a native EVM
// HMAC. Values are length-prefixed and serialized in this fixed order:
//
//	security.ima, security.selinux, inode, i_generation, uid, gid, mode
//
// Integers are little-endian and use fixed widths (u64 inode, u32 otherwise).
// Length prefixes keep the encodings of the two variable-size attribute values
// unambiguous.
func HMACPayload(attrs FileAttributes) []byte {
	payload := make([]byte, 0, 64+len(attrs.IMA)+len(attrs.SELinux))
	payload = appendAttribute(payload, attrs.IMA)
	payload = appendAttribute(payload, attrs.SELinux)
	payload = binary.LittleEndian.AppendUint64(payload, attrs.Inode)
	payload = binary.LittleEndian.AppendUint32(payload, attrs.Generation)
	payload = binary.LittleEndian.AppendUint32(payload, attrs.UID)
	payload = binary.LittleEndian.AppendUint32(payload, attrs.GID)
	payload = binary.LittleEndian.AppendUint32(payload, attrs.Mode)
	return payload
}

// PortableSignaturePayload returns the byte sequence covered by a portable EVM
// signature. It covers security.ima, security.selinux, uid, gid and mode, but
// deliberately excludes the inode number and i_generation.
func PortableSignaturePayload(attrs FileAttributes) []byte {
	payload := make([]byte, 0, 32+len(attrs.IMA)+len(attrs.SELinux))
	payload = appendAttribute(payload, attrs.IMA)
	payload = appendAttribute(payload, attrs.SELinux)
	payload = binary.LittleEndian.AppendUint32(payload, attrs.UID)
	payload = binary.LittleEndian.AppendUint32(payload, attrs.GID)
	payload = binary.LittleEndian.AppendUint32(payload, attrs.Mode)
	return payload
}

// ComputeHMAC returns the HMAC-SHA256 digest for attrs using key.
func ComputeHMAC(key []byte, attrs FileAttributes) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(HMACPayload(attrs))
	return mac.Sum(nil)
}

// NewHMACXattr builds a complete type-2 security.evm value. It is intended for
// provisioning tools and tests; access-time verification uses Evaluator.
func NewHMACXattr(key []byte, attrs FileAttributes) []byte {
	xattr := make([]byte, 1, 1+HMACDigestSize)
	xattr[0] = EVMTypeHMAC
	return append(xattr, ComputeHMAC(key, attrs)...)
}

// SignPortableXattr builds a complete type-5 security.evm value signed with
// priv. The returned value is the type byte followed by a raw Ed25519
// signature over PortableSignaturePayload(attrs).
func SignPortableXattr(priv ed25519.PrivateKey, attrs FileAttributes) ([]byte, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("netsec: invalid Ed25519 private key length %d", len(priv))
	}
	signature := ed25519.Sign(priv, PortableSignaturePayload(attrs))
	xattr := make([]byte, 1, 1+len(signature))
	xattr[0] = EVMTypePortableSignature
	return append(xattr, signature...), nil
}

// VerifyPortableSignature reports whether signature is a valid Ed25519
// signature over attrs under pub.
func VerifyPortableSignature(pub ed25519.PublicKey, attrs FileAttributes, signature []byte) bool {
	if len(pub) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, PortableSignaturePayload(attrs), signature)
}

func appendAttribute(dst, value []byte) []byte {
	if uint64(len(value)) > math.MaxUint32 {
		panic("netsec: attribute value exceeds u32 length")
	}
	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

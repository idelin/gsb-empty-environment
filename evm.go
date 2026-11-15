package netsec

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
)

const (
	XAttrEVM     = "security.evm"
	XAttrIMA     = "security.ima"
	XAttrSELinux = "security.selinux"
)

const (
	EVMTypeHMAC              byte = 2
	EVMTypePortableSignature byte = 5
	EVMTypeLocalHMAC              = EVMTypeHMAC
)

const (
	EVM_XATTR_HMAC               = EVMTypeHMAC
	EVM_XATTR_PORTABLE_DIGSIG    = EVMTypePortableSignature
	EVM_XATTR_PORTABLE_SIGNATURE = EVMTypePortableSignature
)

var (
	ErrMissingEVM        = errors.New("security.evm is missing")
	ErrMalformedEVM      = errors.New("security.evm is malformed")
	ErrUnknownEVMType    = errors.New("security.evm has an unknown type")
	ErrUnsupported       = errors.New("operation is not supported on this platform")
	ErrGenerationUnavail = errors.New("inode generation is unavailable")
)

// FileState is an immutable snapshot of the metadata protected by security.evm.
// Path is informational only and is never authenticated.
type FileState struct {
	Path       string
	IMA        []byte
	SELinux    []byte
	Inode      uint64
	Generation uint64
	UID        uint32
	GID        uint32
	Mode       uint32
	EVM        []byte
}

// Snapshot is an alias kept for callers that use snapshot terminology.
type Snapshot = FileState

// EVMRecord is the decoded security.evm value.
type EVMRecord struct {
	Type    byte
	Payload []byte
}

// ParseEVM decodes the type byte and payload of security.evm.
func ParseEVM(raw []byte) (EVMRecord, error) {
	if len(raw) == 0 {
		return EVMRecord{}, ErrMissingEVM
	}
	return EVMRecord{
		Type:    raw[0],
		Payload: append([]byte(nil), raw[1:]...),
	}, nil
}

// HMACInput returns the canonical, unambiguous input bound to the inode.
// The order is security.ima, security.selinux, inode, i_generation, uid, gid,
// and mode. Byte slices are length-prefixed; scalars are big-endian.
func HMACInput(state FileState) []byte {
	size := 8 + len(state.IMA) + 8 + len(state.SELinux) + 8 + 8 + 4 + 4 + 4
	out := make([]byte, 0, size)
	out = appendBytes(out, state.IMA)
	out = appendBytes(out, state.SELinux)
	out = appendUint64(out, state.Inode)
	out = appendUint64(out, state.Generation)
	out = appendUint32(out, state.UID)
	out = appendUint32(out, state.GID)
	out = appendUint32(out, state.Mode)
	return out
}

// PortableSignatureInput returns the portable form of the authenticated
// metadata. It omits inode and i_generation by design.
func PortableSignatureInput(state FileState) []byte {
	size := 8 + len(state.IMA) + 8 + len(state.SELinux) + 4 + 4 + 4
	out := make([]byte, 0, size)
	out = appendBytes(out, state.IMA)
	out = appendBytes(out, state.SELinux)
	out = appendUint32(out, state.UID)
	out = appendUint32(out, state.GID)
	out = appendUint32(out, state.Mode)
	return out
}

// ComputeHMAC returns HMAC-SHA-256 over the local, inode-bound input.
func ComputeHMAC(state FileState, key []byte) []byte {
	return computeHMAC(sha256.New, state, key)
}

// ComputeHMACSHA1 returns the legacy 20-byte HMAC-SHA-1 form over this
// package's canonical input.
func ComputeHMACSHA1(state FileState, key []byte) []byte {
	return computeHMAC(sha1.New, state, key)
}

func computeHMAC(newHash func() hash.Hash, state FileState, key []byte) []byte {
	mac := hmac.New(newHash, key)
	_, _ = mac.Write(HMACInput(state))
	return mac.Sum(nil)
}

// SealHMAC produces a type-2 security.evm value.
func SealHMAC(state FileState, key []byte) ([]byte, error) {
	if len(key) == 0 {
		return nil, ErrKeyUnavailable
	}
	return append([]byte{EVMTypeHMAC}, ComputeHMAC(state, key)...), nil
}

// SealHMACSHA1 produces a type-2 HMAC-SHA-1 security.evm value.
func SealHMACSHA1(state FileState, key []byte) ([]byte, error) {
	if len(key) == 0 {
		return nil, ErrKeyUnavailable
	}
	return append([]byte{EVMTypeHMAC}, ComputeHMACSHA1(state, key)...), nil
}

func appendBytes(dst, value []byte) []byte {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	dst = append(dst, length[:]...)
	return append(dst, value...)
}

func appendUint64(dst []byte, value uint64) []byte {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	return append(dst, encoded[:]...)
}

func appendUint32(dst []byte, value uint32) []byte {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	return append(dst, encoded[:]...)
}

func recordTypeName(typ byte) string {
	switch typ {
	case EVMTypeHMAC:
		return "local-hmac"
	case EVMTypePortableSignature:
		return "portable-signature"
	default:
		return fmt.Sprintf("unknown(%d)", typ)
	}
}

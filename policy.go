package netsec

import "bytes"

// PolicyVersion, EncryptionMode, and PolicyFlags are aliases for the on-disk
// fscrypt numeric types.
type PolicyVersion = uint8

// EncryptionMode is an fscrypt contents or filenames encryption mode.
type EncryptionMode = uint8

// PolicyFlags is an opaque fscrypt policy flag bitset.
type PolicyFlags = uint32

// Policy describes the fscrypt protection applied to the contents of a
// directory.  The zero Policy is still a policy; a plaintext directory is
// represented by a nil *Policy.
//
// MasterKeyID is the 16-byte fscrypt key identifier.  Flags are compared as an
// opaque bitset so that policy equality does not accidentally ignore flags
// unknown to this package.
type Policy struct {
	Version       uint8
	MasterKeyID   [16]byte
	ContentsMode  uint8
	FilenamesMode uint8
	Flags         uint32
}

// Policy versions defined by fscrypt.
const (
	PolicyVersion1 uint8 = 1
	PolicyVersion2 uint8 = 2
)

// fscrypt encryption modes.  Contents and filenames modes have different
// valid subsets, but the model intentionally keeps both fields open so that
// policies produced by newer kernels can still be represented and compared.
const (
	ModeAES256XTS uint8 = 1
	ModeAES256CTS uint8 = 4
	ModeAES256HSH uint8 = 5
	ModeAES256GCM uint8 = 6
	ModeAdiantum  uint8 = 9
)

// Common fscrypt policy flags.
const (
	PolicyFlagDirectKey   uint32 = 0x00000004
	PolicyFlagIVInoLblk64 uint32 = 0x00000008
	PolicyFlagIVInoLblk32 uint32 = 0x00000010
)

// Equal reports whether two policies are exactly equal.  Every field is part
// of the identity: a different key, mode, version, or flag requires a separate
// encryption domain and therefore does not satisfy rename or link checks.
func (p Policy) Equal(other Policy) bool {
	return p.Version == other.Version &&
		p.MasterKeyID == other.MasterKeyID &&
		p.ContentsMode == other.ContentsMode &&
		p.FilenamesMode == other.FilenamesMode &&
		p.Flags == other.Flags
}

// PoliciesEqual compares two optional policies.  Two nil policies represent
// two plaintext directories and are equal.
func PoliciesEqual(a, b *Policy) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// KeyIDEqual compares only the master key identifiers.  It is primarily useful
// to diagnostics; policy enforcement must use Equal or PoliciesEqual because
// version, modes, and flags are also security-relevant.
func (p Policy) KeyIDEqual(other Policy) bool {
	return bytes.Equal(p.MasterKeyID[:], other.MasterKeyID[:])
}

func clonePolicy(policy *Policy) *Policy {
	if policy == nil {
		return nil
	}
	copy := *policy
	return &copy
}

package netsec

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"hash"
)

const (
	// Version1 is the only descriptor version supported.
	Version1 = 1

	// AlgSHA256 and AlgSHA512 identify the hash algorithm field.
	AlgSHA256 = 1
	AlgSHA512 = 2

	// BlockShift is log2 of the Merkle tree block size (fs-verity uses 4096).
	BlockShift = 12
	// BlockSize is the data/hash block size in bytes.
	BlockSize = 1 << BlockShift

	// DescriptorSize is the fixed size of the integrity descriptor.
	DescriptorSize = 256

	// MaxSaltSize matches fs-verity's FS_VERITY_MAX_SALT_SIZE.
	MaxSaltSize = 32
	// MaxDigestSize matches fs-verity's FS_VERITY_MAX_DIGEST_SIZE.
	MaxDigestSize = 64
)

// Descriptor field offsets within the 256-byte blob.
const (
	offVersion   = 0
	offAlgorithm = 1
	offLogBlock  = 2
	offSaltSize  = 3
	// bytes 4..7 reserved, must be zero
	offDataSize = 8 // 8 bytes, little-endian
	offRootHash = 16
	offSalt     = offRootHash + MaxDigestSize // 80
)

var (
	ErrInvalidDescriptor    = errors.New("netsec: invalid integrity descriptor")
	ErrUnsupportedAlgorithm = errors.New("netsec: unsupported hash algorithm")
	ErrMeasurementMismatch  = errors.New("netsec: descriptor does not match signed measurement")
	ErrSizeMismatch         = errors.New("netsec: data length does not match descriptor")
	ErrHashMismatch         = errors.New("netsec: Merkle tree verification failed")
	ErrTreeCorrupt          = errors.New("netsec: hash tree blob is malformed")
	ErrBlockOutOfRange      = errors.New("netsec: block index out of range")
)

type hashSpec struct {
	newHash   func() hash.Hash
	size      int
	blockSize int
}

func specFor(alg byte) (hashSpec, error) {
	switch alg {
	case AlgSHA256:
		return hashSpec{newHash: sha256.New, size: sha256.Size, blockSize: sha256.BlockSize}, nil
	case AlgSHA512:
		return hashSpec{newHash: sha512.New, size: sha512.Size, blockSize: sha512.BlockSize}, nil
	default:
		return hashSpec{}, ErrUnsupportedAlgorithm
	}
}

// saltPrefix returns the salt zero-padded to the hash block size, which is
// prepended to every hashed input. With no salt, nothing is prepended.
func saltPrefix(spec hashSpec, salt []byte) []byte {
	if len(salt) == 0 {
		return nil
	}
	prefix := make([]byte, spec.blockSize)
	copy(prefix, salt)
	return prefix
}

// Descriptor is the parsed form of the fixed 256-byte integrity descriptor.
type Descriptor struct {
	Algorithm byte
	Salt      []byte
	DataSize  uint64
	RootHash  []byte
}

// Marshal encodes the descriptor into its 256-byte on-disk form.
func (d *Descriptor) Marshal() []byte {
	b := make([]byte, DescriptorSize)
	b[offVersion] = Version1
	b[offAlgorithm] = d.Algorithm
	b[offLogBlock] = BlockShift
	b[offSaltSize] = byte(len(d.Salt))
	binary.LittleEndian.PutUint64(b[offDataSize:], d.DataSize)
	copy(b[offRootHash:], d.RootHash)
	copy(b[offSalt:], d.Salt)
	return b
}

// ParseDescriptor decodes and strictly validates a 256-byte descriptor blob.
func ParseDescriptor(b []byte) (*Descriptor, error) {
	if len(b) != DescriptorSize {
		return nil, ErrInvalidDescriptor
	}
	if b[offVersion] != Version1 {
		return nil, ErrInvalidDescriptor
	}
	spec, err := specFor(b[offAlgorithm])
	if err != nil {
		return nil, err
	}
	if b[offLogBlock] != BlockShift {
		return nil, ErrInvalidDescriptor
	}
	saltSize := int(b[offSaltSize])
	if saltSize > MaxSaltSize {
		return nil, ErrInvalidDescriptor
	}
	// All reserved and tail bytes must be zero.
	for _, z := range b[4:8] {
		if z != 0 {
			return nil, ErrInvalidDescriptor
		}
	}
	for _, z := range b[offRootHash+spec.size : offSalt] {
		if z != 0 {
			return nil, ErrInvalidDescriptor
		}
	}
	for _, z := range b[offSalt+saltSize:] {
		if z != 0 {
			return nil, ErrInvalidDescriptor
		}
	}
	d := &Descriptor{
		Algorithm: b[offAlgorithm],
		Salt:      append([]byte(nil), b[offSalt:offSalt+saltSize]...),
		DataSize:  binary.LittleEndian.Uint64(b[offDataSize:]),
		RootHash:  append([]byte(nil), b[offRootHash:offRootHash+spec.size]...),
	}
	return d, nil
}

// Measurement computes the signed measurement: the hash of the descriptor.
// The salt lives inside the descriptor, so swapping salt or algorithm
// changes the measurement and is detected on verification.
func Measurement(descBlob []byte, alg byte) ([]byte, error) {
	spec, err := specFor(alg)
	if err != nil {
		return nil, err
	}
	h := spec.newHash()
	h.Write(descBlob)
	return h.Sum(nil), nil
}

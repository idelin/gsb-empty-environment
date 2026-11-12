package netsec

import (
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
)

const (
	BlockSize      = 4096
	Log2BlockSize  = 12
	DescriptorSize = 256
	RootHashSize   = 64
	SaltSize       = 32
	Version        = 1
)

const (
	offsetVersion       = 0
	offsetAlgorithm     = 1
	offsetLog2BlockSize = 2
	offsetSaltSize      = 3
	offsetReserved04    = 4
	offsetDataSize      = 8
	offsetRootHash      = 16
	offsetSalt          = 80
	offsetReserved      = 112
)

// Algorithm is the hash algorithm used for both the Merkle tree and the
// descriptor measurement.
type Algorithm uint8

const (
	AlgorithmSHA256 Algorithm = 1
	AlgorithmSHA512 Algorithm = 2
)

const (
	SHA256 = AlgorithmSHA256
	SHA512 = AlgorithmSHA512

	HashSHA256 = AlgorithmSHA256
	HashSHA512 = AlgorithmSHA512
)

var (
	ErrDescriptorSize       = errors.New("invalid descriptor size")
	ErrUnsupportedVersion   = errors.New("unsupported descriptor version")
	ErrUnsupportedAlgorithm = errors.New("unsupported hash algorithm")
	ErrUnsupportedBlockSize = errors.New("unsupported block size")
	ErrInvalidSalt          = errors.New("invalid salt")
	ErrReservedBytes        = errors.New("non-zero reserved descriptor bytes")
	ErrInvalidRootHash      = errors.New("invalid root hash")
	ErrMeasurementMismatch  = errors.New("descriptor measurement mismatch")
	ErrSizeMismatch         = errors.New("data length does not match descriptor")
	ErrRootHashMismatch     = errors.New("Merkle tree root hash mismatch")
	ErrCorruptBlock         = errors.New("corrupted data block")
	ErrBlockIndex           = errors.New("data block index out of range")
	ErrEmptySource          = errors.New("nil data source")
)

// DigestSize returns the output size of the algorithm.
func (a Algorithm) DigestSize() int {
	switch a {
	case AlgorithmSHA256:
		return sha256.Size
	case AlgorithmSHA512:
		return sha512.Size
	default:
		return 0
	}
}

// HashBlockSize returns the input block size of the hash algorithm's
// compression function. The descriptor salt is zero-padded to this size.
func (a Algorithm) HashBlockSize() int {
	switch a {
	case AlgorithmSHA256:
		return sha256.BlockSize
	case AlgorithmSHA512:
		return sha512.BlockSize
	default:
		return 0
	}
}

func (a Algorithm) String() string {
	switch a {
	case AlgorithmSHA256:
		return "sha256"
	case AlgorithmSHA512:
		return "sha512"
	default:
		return fmt.Sprintf("algorithm(%d)", uint8(a))
	}
}

func (a Algorithm) newHash() (hash.Hash, error) {
	switch a {
	case AlgorithmSHA256:
		return sha256.New(), nil
	case AlgorithmSHA512:
		return sha512.New(), nil
	default:
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedAlgorithm, uint8(a))
	}
}

func (a Algorithm) validate() error {
	if a.DigestSize() == 0 {
		return fmt.Errorf("%w: %d", ErrUnsupportedAlgorithm, uint8(a))
	}
	return nil
}

// Descriptor is the fs-verity-style 256-byte metadata structure. RootHash and
// Salt hold the algorithm-sized digest and the configured salt respectively.
// The fixed on-disk fields are defined by MarshalBinary.
type Descriptor struct {
	Version       uint8
	Algorithm     Algorithm
	Log2BlockSize uint8
	SaltSize      uint8
	DataSize      uint64
	RootHash      []byte
	Salt          []byte
}

func (d *Descriptor) validate() error {
	if d.Version != Version {
		return fmt.Errorf("%w: %d", ErrUnsupportedVersion, d.Version)
	}
	if err := d.Algorithm.validate(); err != nil {
		return err
	}
	if d.Log2BlockSize != Log2BlockSize {
		return fmt.Errorf("%w: log2=%d", ErrUnsupportedBlockSize, d.Log2BlockSize)
	}
	if int(d.SaltSize) > SaltSize {
		return fmt.Errorf("%w: length=%d", ErrInvalidSalt, d.SaltSize)
	}
	if len(d.Salt) < int(d.SaltSize) || len(d.Salt) > SaltSize {
		return fmt.Errorf("%w: length=%d, field=%d", ErrInvalidSalt, len(d.Salt), d.SaltSize)
	}
	if !allZero(d.Salt[d.SaltSize:]) {
		return fmt.Errorf("%w: trailing bytes after salt", ErrInvalidSalt)
	}
	if len(d.RootHash) < d.Algorithm.DigestSize() || len(d.RootHash) > RootHashSize {
		return fmt.Errorf("%w: length=%d, algorithm=%s", ErrInvalidRootHash, len(d.RootHash), d.Algorithm)
	}
	if !allZero(d.RootHash[d.Algorithm.DigestSize():]) {
		return fmt.Errorf("%w: trailing bytes after %s digest", ErrInvalidRootHash, d.Algorithm)
	}
	return nil
}

// MarshalBinary encodes the descriptor in its canonical 256-byte form.
func (d *Descriptor) MarshalBinary() ([]byte, error) {
	if err := d.validate(); err != nil {
		return nil, err
	}

	out := make([]byte, DescriptorSize)
	out[offsetVersion] = d.Version
	out[offsetAlgorithm] = byte(d.Algorithm)
	out[offsetLog2BlockSize] = d.Log2BlockSize
	out[offsetSaltSize] = d.SaltSize
	binary.LittleEndian.PutUint64(out[offsetDataSize:], d.DataSize)
	copy(out[offsetRootHash:offsetRootHash+RootHashSize], d.RootHash)
	copy(out[offsetSalt:offsetSalt+SaltSize], d.Salt)
	return out, nil
}

// UnmarshalBinary parses and validates a canonical 256-byte descriptor.
func (d *Descriptor) UnmarshalBinary(b []byte) error {
	if len(b) != DescriptorSize {
		return fmt.Errorf("%w: got %d, want %d", ErrDescriptorSize, len(b), DescriptorSize)
	}

	version := b[offsetVersion]
	if version != Version {
		return fmt.Errorf("%w: %d", ErrUnsupportedVersion, version)
	}
	alg := Algorithm(b[offsetAlgorithm])
	if err := alg.validate(); err != nil {
		return err
	}
	if b[offsetLog2BlockSize] != Log2BlockSize {
		return fmt.Errorf("%w: log2=%d", ErrUnsupportedBlockSize, b[offsetLog2BlockSize])
	}
	saltLen := int(b[offsetSaltSize])
	if saltLen > SaltSize {
		return fmt.Errorf("%w: length=%d", ErrInvalidSalt, saltLen)
	}
	if !allZero(b[offsetReserved04:offsetDataSize]) {
		return fmt.Errorf("%w: bytes 4..8", ErrReservedBytes)
	}
	if !allZero(b[offsetReserved:]) {
		return fmt.Errorf("%w: bytes 112..256", ErrReservedBytes)
	}

	root := b[offsetRootHash : offsetRootHash+RootHashSize]
	if !allZero(root[alg.DigestSize():]) {
		return fmt.Errorf("%w: trailing bytes after %s digest", ErrInvalidRootHash, alg)
	}
	saltField := b[offsetSalt : offsetSalt+SaltSize]
	if !allZero(saltField[saltLen:]) {
		return fmt.Errorf("%w: trailing bytes after salt", ErrInvalidSalt)
	}

	*d = Descriptor{
		Version:       version,
		Algorithm:     alg,
		Log2BlockSize: b[offsetLog2BlockSize],
		SaltSize:      uint8(saltLen),
		DataSize:      binary.LittleEndian.Uint64(b[offsetDataSize:]),
		RootHash:      append([]byte(nil), root[:alg.DigestSize()]...),
		Salt:          append([]byte(nil), saltField[:saltLen]...),
	}
	return nil
}

// ParseDescriptor parses a canonical 256-byte descriptor.
func ParseDescriptor(b []byte) (*Descriptor, error) {
	d := new(Descriptor)
	if err := d.UnmarshalBinary(b); err != nil {
		return nil, err
	}
	return d, nil
}

// Measurement returns the hash of the descriptor. The descriptor's hash
// algorithm is used, so changing either the salt or the algorithm changes the
// authenticated value.
func (d *Descriptor) Measurement() ([]byte, error) {
	raw, err := d.MarshalBinary()
	if err != nil {
		return nil, err
	}
	h, err := d.Algorithm.newHash()
	if err != nil {
		return nil, err
	}
	_, _ = h.Write(raw)
	return h.Sum(nil), nil
}

// Bytes returns the canonical 256-byte descriptor encoding.
func (d *Descriptor) Bytes() ([]byte, error) {
	return d.MarshalBinary()
}

// VerifyMeasurement checks the descriptor against a trusted measurement in
// constant time.
func (d *Descriptor) VerifyMeasurement(measurement []byte) error {
	got, err := d.Measurement()
	if err != nil {
		return err
	}
	if len(got) != len(measurement) || subtle.ConstantTimeCompare(got, measurement) != 1 {
		return ErrMeasurementMismatch
	}
	return nil
}

// HashDescriptor is the package-level form of Descriptor.Measurement.
func HashDescriptor(d *Descriptor) ([]byte, error) {
	if d == nil {
		return nil, ErrInvalidRootHash
	}
	return d.Measurement()
}

// VerifyDescriptor checks a descriptor against a trusted measurement.
func VerifyDescriptor(d *Descriptor, measurement []byte) error {
	if d == nil {
		return ErrInvalidRootHash
	}
	return d.VerifyMeasurement(measurement)
}

// NewDescriptor measures data and returns a descriptor containing the Merkle
// root hash. If alg is omitted, SHA-256 is used.
func NewDescriptor(data, salt []byte, alg ...Algorithm) (*Descriptor, error) {
	algorithm := AlgorithmSHA256
	if len(alg) > 1 {
		return nil, fmt.Errorf("%w: multiple algorithms supplied", ErrUnsupportedAlgorithm)
	}
	if len(alg) == 1 {
		algorithm = alg[0]
	}
	if err := algorithm.validate(); err != nil {
		return nil, err
	}
	if len(salt) > SaltSize {
		return nil, fmt.Errorf("%w: length=%d", ErrInvalidSalt, len(salt))
	}

	root, err := rootHashFromData(data, salt, algorithm)
	if err != nil {
		return nil, err
	}
	return &Descriptor{
		Version:       Version,
		Algorithm:     algorithm,
		Log2BlockSize: Log2BlockSize,
		SaltSize:      uint8(len(salt)),
		DataSize:      uint64(len(data)),
		RootHash:      root,
		Salt:          append([]byte(nil), salt...),
	}, nil
}

// Measure builds a descriptor and its authenticated measurement.
func Measure(data, salt []byte, alg ...Algorithm) (*Descriptor, []byte, error) {
	d, err := NewDescriptor(data, salt, alg...)
	if err != nil {
		return nil, nil, err
	}
	measurement, err := d.Measurement()
	if err != nil {
		return nil, nil, err
	}
	return d, measurement, nil
}

// Measurement computes the measurement for data, salt and algorithm without
// returning the intermediate descriptor.
func Measurement(data, salt []byte, alg ...Algorithm) ([]byte, error) {
	_, measurement, err := Measure(data, salt, alg...)
	if err != nil {
		return nil, err
	}
	return measurement, nil
}

// RootHash computes the fs-verity-style Merkle root for data.
func RootHash(data, salt []byte, alg ...Algorithm) ([]byte, error) {
	algorithm := AlgorithmSHA256
	if len(alg) > 1 {
		return nil, fmt.Errorf("%w: multiple algorithms supplied", ErrUnsupportedAlgorithm)
	}
	if len(alg) == 1 {
		algorithm = alg[0]
	}
	return rootHashFromData(data, salt, algorithm)
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

package luks2

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
)

// LUKS2 binary header layout, as defined by the on-disk format. The header is
// little-endian and normally occupies 4096 bytes; header_size describes the
// full binary+JSON region that the checksum covers.
const (
	// Magic is the six-byte LUKS2 magic at the start of every header.
	Magic = "LUKS\xba\xbe"
	// Version2 is the only supported header version.
	Version2 uint16 = 2
	// ChecksumSHA256 identifies SHA-256 in the checksum_algorithm field.
	ChecksumSHA256 = "sha256"
	// ChecksumSize is the size of the stored checksum field.
	ChecksumSize = sha256.Size
	// MinHeaderSize is the smallest legal header_size.
	MinHeaderSize uint64 = 4096

	offMagic             = 0
	offVersion           = 6
	offHeaderSize        = 8
	offSeqID             = 16
	offLabel             = 24
	offChecksumAlgorithm = 72
	offSalt              = 104
	offUUID              = 136
	offSubsystem         = 176
	offHeaderOffset      = 240
	offChecksum          = 448

	lenLabel             = 48
	lenChecksumAlgorithm = 32
	lenSalt              = 32
	lenUUID              = 40
	lenSubsystem         = 64
	lenHeaderOffset      = 8

	// jsonAreaOffset is the start of the JSON metadata area within a header
	// region: one binary header followed by the JSON area.
	jsonAreaOffset = 4096
)

// ErrBadHeader classifies malformed or tampered binary headers.
var ErrBadHeader = errors.New("luks2: bad binary header")

// BinaryHeader holds the fields read from the fixed-size binary area.
type BinaryHeader struct {
	Magic             string
	Version           uint16
	HeaderSize        uint64
	SequenceID        uint64
	Label             string
	ChecksumAlgorithm string
	Salt              []byte
	UUID              string
	Subsystem         string
	HeaderOffset      uint64
	Checksum          []byte
}

// ParseBinaryHeader decodes and sanity-checks the fixed binary area of a
// LUKS2 header. At least header_size bytes of the full header must be passed
// to VerifyChecksum; this function only needs the 512-byte binary area.
func ParseBinaryHeader(raw []byte) (*BinaryHeader, error) {
	if len(raw) < offChecksum+ChecksumSize {
		return nil, fmt.Errorf("%w: %d bytes is shorter than the binary area",
			ErrBadHeader, len(raw))
	}

	magic := string(raw[offMagic : offMagic+len(Magic)])
	if magic != Magic {
		return nil, fmt.Errorf("%w: bad magic %q", ErrBadHeader, magic)
	}
	version := binary.LittleEndian.Uint16(raw[offVersion:])
	if version != Version2 {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrBadHeader, version)
	}

	headerSize := binary.LittleEndian.Uint64(raw[offHeaderSize:])
	if headerSize < MinHeaderSize {
		return nil, fmt.Errorf("%w: header_size %d below minimum %d",
			ErrBadHeader, headerSize, MinHeaderSize)
	}
	if headerSize%MinHeaderSize != 0 {
		return nil, fmt.Errorf("%w: header_size %d is not a multiple of %d",
			ErrBadHeader, headerSize, MinHeaderSize)
	}

	algorithm := cString(raw[offChecksumAlgorithm : offChecksumAlgorithm+lenChecksumAlgorithm])
	if algorithm != ChecksumSHA256 {
		return nil, fmt.Errorf("%w: unsupported checksum algorithm %q", ErrBadHeader, algorithm)
	}

	header := &BinaryHeader{
		Magic:             magic,
		Version:           version,
		HeaderSize:        headerSize,
		SequenceID:        binary.LittleEndian.Uint64(raw[offSeqID:]),
		Label:             cString(raw[offLabel : offLabel+lenLabel]),
		ChecksumAlgorithm: algorithm,
		Salt:              append([]byte(nil), raw[offSalt:offSalt+lenSalt]...),
		UUID:              cString(raw[offUUID : offUUID+lenUUID]),
		Subsystem:         cString(raw[offSubsystem : offSubsystem+lenSubsystem]),
		HeaderOffset:      binary.LittleEndian.Uint64(raw[offHeaderOffset : offHeaderOffset+lenHeaderOffset]),
		Checksum:          append([]byte(nil), raw[offChecksum:offChecksum+ChecksumSize]...),
	}
	return header, nil
}

// VerifyChecksum recomputes the SHA-256 checksum of the header region. The
// checksum field is treated as zero for the computation, exactly as LUKS2
// stores it: hash over the first header_size bytes with bytes 448..479
// cleared, then compare against the stored digest in constant time.
func VerifyChecksum(raw []byte) error {
	header, err := ParseBinaryHeader(raw)
	if err != nil {
		return err
	}
	if uint64(len(raw)) < header.HeaderSize {
		return fmt.Errorf("%w: %d bytes is shorter than header_size %d",
			ErrBadHeader, len(raw), header.HeaderSize)
	}

	region := make([]byte, header.HeaderSize)
	copy(region, raw[:header.HeaderSize])
	for i := offChecksum; i < offChecksum+ChecksumSize; i++ {
		region[i] = 0
	}
	got := sha256.Sum256(region)
	if subtle.ConstantTimeCompare(got[:], header.Checksum) != 1 {
		return fmt.Errorf("%w: checksum mismatch (header tampered?)", ErrBadHeader)
	}
	return nil
}

// JSONArea returns the JSON metadata bytes of a primary header region:
// everything between the 4096-byte binary area and the start of the secondary
// header at header_size, with trailing NUL padding removed by the parser.
func JSONArea(raw []byte, headerSize uint64) ([]byte, error) {
	if uint64(len(raw)) < headerSize {
		return nil, fmt.Errorf("%w: %d bytes is shorter than header_size %d",
			ErrBadHeader, len(raw), headerSize)
	}
	if headerSize <= jsonAreaOffset {
		return nil, fmt.Errorf("%w: header_size %d leaves no JSON area",
			ErrBadHeader, headerSize)
	}
	return raw[jsonAreaOffset:headerSize], nil
}

// ParseHeader performs the full validation of a primary LUKS2 header region:
// binary sanity checks and checksum verification first, then strict JSON
// parsing, then area, binding and KDF validation. A header whose checksum is
// invalid is rejected before any JSON content is trusted.
func ParseHeader(raw []byte, limits KDFLimits) (*BinaryHeader, *Header, error) {
	binaryHeader, err := ParseBinaryHeader(raw)
	if err != nil {
		return nil, nil, err
	}
	if err := VerifyChecksum(raw); err != nil {
		return nil, nil, err
	}
	jsonBytes, err := JSONArea(raw, binaryHeader.HeaderSize)
	if err != nil {
		return nil, nil, err
	}
	header, err := ParseMetadata(jsonBytes)
	if err != nil {
		return nil, nil, err
	}
	if err := Validate(header, binaryHeader.HeaderSize, limits); err != nil {
		return nil, nil, err
	}
	return binaryHeader, header, nil
}

func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// ExtensionCompressCertificate is the IANA TLS extension type for
	// RFC 8879 compress_certificate.
	ExtensionCompressCertificate uint16 = 27
	// HandshakeTypeCompressedCertificate is the RFC 8879 handshake message
	// type. The wire helpers in this package operate on its body.
	HandshakeTypeCompressedCertificate uint8 = 25

	// AlertBadCertificate is the TLS bad_certificate alert code.
	AlertBadCertificate uint8 = 42

	// MaxUncompressedLength is the largest value representable by the uint24
	// uncompressed_length field in RFC 8879.
	MaxUncompressedLength = (1 << 24) - 1

	// MaxCompressedCertificateLength is the maximum compressed certificate
	// message length representable by its uint16 vector.
	MaxCompressedCertificateLength = (1 << 16) - 1
)

// AlertForError maps certificate-compression parse/length failures to a TLS
// alert. Callers should abort the handshake with AlertBadCertificate when this
// returns a non-zero value.
func AlertForError(err error) uint8 {
	if err != nil && errors.Is(err, ErrBadCertificate) {
		return AlertBadCertificate
	}
	return 0
}

var (
	// ErrMalformedExtension indicates malformed extension data.
	ErrMalformedExtension = errors.New("netsec: malformed TLS extension")
	// ErrBadCertificate indicates invalid compressed certificate data or
	// length metadata. Callers on a TLS handshake path must turn this into a
	// bad_certificate alert.
	ErrBadCertificate = errors.New("netsec: bad certificate")
	// ErrUncompressedTooLarge indicates that a compression stream produced
	// more output than its declared uncompressed_length.
	ErrUncompressedTooLarge = errors.New("netsec: uncompressed data exceeds limit")
)

// ParseCompressCertificateExtension parses the body of the client
// compress_certificate extension. The wire format is:
//
//	uint16 algorithms_length;
//	uint16 algorithms[algorithms_length / 2];
func ParseCompressCertificateExtension(data []byte) ([]Algorithm, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("%w: compress_certificate is too short", ErrMalformedExtension)
	}
	n := int(binary.BigEndian.Uint16(data[:2]))
	if n != len(data)-2 {
		return nil, fmt.Errorf("%w: compress_certificate length mismatch", ErrMalformedExtension)
	}
	if n%2 != 0 {
		return nil, fmt.Errorf("%w: compress_certificate algorithm vector has odd length", ErrMalformedExtension)
	}

	algorithms := make([]Algorithm, 0, n/2)
	seen := make(map[Algorithm]struct{}, n/2)
	for offset := 2; offset < len(data); offset += 2 {
		algorithm := Algorithm(binary.BigEndian.Uint16(data[offset : offset+2]))
		if algorithm == 0 {
			return nil, fmt.Errorf("%w: algorithm zero is reserved", ErrMalformedExtension)
		}
		if _, duplicate := seen[algorithm]; duplicate {
			continue
		}
		seen[algorithm] = struct{}{}
		algorithms = append(algorithms, algorithm)
	}
	return algorithms, nil
}

// MarshalCompressCertificateExtension encodes the client
// compress_certificate extension body.
func MarshalCompressCertificateExtension(algorithms []Algorithm) ([]byte, error) {
	if len(algorithms) > (MaxCompressedCertificateLength-1)/2 {
		return nil, fmt.Errorf("%w: too many algorithms", ErrMalformedExtension)
	}
	data := make([]byte, 2+2*len(algorithms))
	binary.BigEndian.PutUint16(data[:2], uint16(2*len(algorithms)))
	for i, algorithm := range algorithms {
		if algorithm == 0 {
			return nil, fmt.Errorf("%w: algorithm zero is reserved", ErrMalformedExtension)
		}
		binary.BigEndian.PutUint16(data[2+2*i:], uint16(algorithm))
	}
	return data, nil
}

// CompressedCertificate is the RFC 8879 CompressedCertificate handshake
// message body (without the outer handshake type and length).
type CompressedCertificate struct {
	Algorithm          Algorithm
	UncompressedLength uint32
	Compressed         []byte
}

// MarshalBinary encodes the RFC 8879 CompressedCertificate message body.
func (c CompressedCertificate) MarshalBinary() ([]byte, error) {
	if c.UncompressedLength > MaxUncompressedLength {
		return nil, fmt.Errorf("%w: uncompressed_length %d exceeds %d", ErrBadCertificate, c.UncompressedLength, MaxUncompressedLength)
	}
	if len(c.Compressed) == 0 || len(c.Compressed) > MaxCompressedCertificateLength {
		return nil, fmt.Errorf("%w: compressed_certificate_message length %d is outside [1,%d]", ErrBadCertificate, len(c.Compressed), MaxCompressedCertificateLength)
	}
	if c.Algorithm == 0 {
		return nil, fmt.Errorf("%w: algorithm zero is reserved", ErrBadCertificate)
	}

	data := make([]byte, 7+len(c.Compressed))
	binary.BigEndian.PutUint16(data[0:2], uint16(c.Algorithm))
	data[2] = byte(c.UncompressedLength >> 16)
	data[3] = byte(c.UncompressedLength >> 8)
	data[4] = byte(c.UncompressedLength)
	binary.BigEndian.PutUint16(data[5:7], uint16(len(c.Compressed)))
	copy(data[7:], c.Compressed)
	return data, nil
}

// UnmarshalBinary parses the RFC 8879 CompressedCertificate message body.
func (c *CompressedCertificate) UnmarshalBinary(data []byte) error {
	if c == nil {
		return errors.New("netsec: nil CompressedCertificate")
	}
	if len(data) < 8 {
		return fmt.Errorf("%w: CompressedCertificate is too short", ErrBadCertificate)
	}

	algorithm := Algorithm(binary.BigEndian.Uint16(data[0:2]))
	if algorithm == 0 {
		return fmt.Errorf("%w: algorithm zero is reserved", ErrBadCertificate)
	}
	uncompressedLength := uint32(data[2])<<16 | uint32(data[3])<<8 | uint32(data[4])
	if uncompressedLength > MaxUncompressedLength {
		// The uint24 representation cannot exceed this value, but check the
		// invariant explicitly so callers can rely on it for allocations.
		return fmt.Errorf("%w: uncompressed_length %d exceeds %d", ErrBadCertificate, uncompressedLength, MaxUncompressedLength)
	}
	compressedLength := int(binary.BigEndian.Uint16(data[5:7]))
	if compressedLength == 0 {
		return fmt.Errorf("%w: empty compressed_certificate_message", ErrBadCertificate)
	}
	if len(data) != 7+compressedLength {
		return fmt.Errorf("%w: CompressedCertificate length mismatch", ErrBadCertificate)
	}

	c.Algorithm = algorithm
	c.UncompressedLength = uncompressedLength
	c.Compressed = append(c.Compressed[:0], data[7:]...)
	return nil
}

// DecompressCertificate validates and decompresses one CompressedCertificate.
// The result must be exactly UncompressedLength bytes. Any length or codec
// failure is reported as ErrBadCertificate.
func DecompressCertificate(message CompressedCertificate, codec Codec, dictionary []byte) ([]byte, error) {
	if codec == nil {
		return nil, fmt.Errorf("%w: unsupported compression algorithm %d", ErrBadCertificate, message.Algorithm)
	}
	if message.UncompressedLength > MaxUncompressedLength {
		return nil, fmt.Errorf("%w: uncompressed_length %d exceeds %d", ErrBadCertificate, message.UncompressedLength, MaxUncompressedLength)
	}
	if len(message.Compressed) == 0 {
		return nil, fmt.Errorf("%w: empty compressed_certificate_message", ErrBadCertificate)
	}

	plaintext, err := codec.Decompress(message.Compressed, dictionary, int(message.UncompressedLength))
	if err != nil {
		return nil, fmt.Errorf("%w: decompression failed: %v", ErrBadCertificate, err)
	}
	if len(plaintext) != int(message.UncompressedLength) {
		return nil, fmt.Errorf("%w: uncompressed length is %d, declared %d", ErrBadCertificate, len(plaintext), message.UncompressedLength)
	}
	return plaintext, nil
}

// CompressCertificate applies codec to a certificate message body. The ok
// result is false when compression is not strictly smaller or cannot fit in
// the RFC 8879 compressed_certificate_message vector, in which case the caller
// must send the original Certificate message.
func CompressCertificate(chain []byte, algorithm Algorithm, codec Codec, dictionary []byte) (message CompressedCertificate, ok bool, err error) {
	if codec == nil {
		return CompressedCertificate{}, false, fmt.Errorf("netsec: no codec for algorithm %d", algorithm)
	}
	if len(chain) > MaxUncompressedLength {
		return CompressedCertificate{}, false, nil
	}
	payload, err := codec.Compress(chain, dictionary)
	if err != nil {
		return CompressedCertificate{}, false, err
	}
	if len(payload) >= len(chain) || len(payload) > MaxCompressedCertificateLength {
		return CompressedCertificate{}, false, nil
	}
	message = CompressedCertificate{
		Algorithm:          algorithm,
		UncompressedLength: uint32(len(chain)),
		Compressed:         payload,
	}
	return message, true, nil
}

// ParseAndDecompressCertificate parses a CompressedCertificate body and
// then decompresses it.
func ParseAndDecompressCertificate(data []byte, codec Codec, dictionary []byte) ([]byte, error) {
	var message CompressedCertificate
	if err := message.UnmarshalBinary(data); err != nil {
		return nil, err
	}
	return DecompressCertificate(message, codec, dictionary)
}

// CacheKey is the exact cache identity required for a compressed certificate
// chain: algorithm, dictionary ID, and SHA-256 of the uncompressed certificate
// chain bytes.
type CacheKey struct {
	Algorithm    Algorithm
	DictionaryID string
	ChainSHA256  [sha256.Size]byte
}

// NewCacheKey derives a cache key from the certificate chain bytes.
func NewCacheKey(algorithm Algorithm, dictionaryID string, chain []byte) CacheKey {
	return CacheKey{
		Algorithm:    algorithm,
		DictionaryID: dictionaryID,
		ChainSHA256:  sha256.Sum256(chain),
	}
}

// CertificateChainBytes serializes DER-encoded certificates into the TLS
// Certificate message body that RFC 8879 compresses. The caller is responsible
// for supplying an already ordered server certificate chain.
func CertificateChainBytes(certificates [][]byte) ([]byte, error) {
	total := 0
	for _, certificate := range certificates {
		if len(certificate) == 0 || len(certificate) > MaxCompressedCertificateLength {
			return nil, fmt.Errorf("netsec: certificate length %d is outside [1,%d]", len(certificate), MaxCompressedCertificateLength)
		}
		total += 2 + len(certificate)
		if total > MaxUncompressedLength {
			return nil, fmt.Errorf("netsec: certificate chain exceeds %d bytes", MaxUncompressedLength)
		}
	}

	data := make([]byte, 2, 2+total)
	binary.BigEndian.PutUint16(data[:2], uint16(total))
	for _, certificate := range certificates {
		entry := make([]byte, 2+len(certificate))
		binary.BigEndian.PutUint16(entry[:2], uint16(len(certificate)))
		copy(entry[2:], certificate)
		data = append(data, entry...)
	}
	return data, nil
}

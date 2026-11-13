package netsec

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
)

// RFC 8879, section 3: Certificate Compression for TLS 1.3.
//
// The CompressedCertificate message has the following wire format:
//
//	enum {
//	    zlib(1), brotli(2), zstd(3), (65535)
//	} CertificateCompressionAlgorithm;
//
//	struct {
//	    CertificateCompressionAlgorithm algorithm;
//	    uint24 uncompressed_length;
//	    opaque compressed_certificate_message<1..2^24-1>;
//	} CompressedCertificate;
const (
	// AlgorithmZlib is the only compression algorithm this decoder knows.
	AlgorithmZlib uint16 = 1

	// MaxUncompressedCertificate is the absolute ceiling for the size of the
	// decompressed Certificate message. It bounds memory use even when a peer
	// declares a large uncompressed_length, so a decompression bomb can never
	// force unbounded allocation. One megabyte comfortably holds any realistic
	// chain while staying inside the 24-bit length field.
	MaxUncompressedCertificate = 1024 * 1024

	compressedCertificateHeaderLen = 8 // uint16 algorithm + uint24 length + uint24 vector length
)

// Sentinel errors returned by the decoder. Callers can use errors.Is to branch
// on the rejection reason without parsing error strings.
var (
	// ErrTruncated means the input does not contain a complete
	// CompressedCertificate message, or carries trailing bytes.
	ErrTruncated = errors.New("compressed certificate: truncated message")

	// ErrUnknownAlgorithm means the algorithm identifier is not one the
	// decoder can decompress.
	ErrUnknownAlgorithm = errors.New("compressed certificate: unknown compression algorithm")

	// ErrDeclaredLengthExceeded means decompression produced more bytes than
	// uncompressed_length declared.
	ErrDeclaredLengthExceeded = errors.New("compressed certificate: decompressed output exceeds declared length")

	// ErrAbsoluteLimitExceeded means the declared or actual decompressed size
	// exceeds MaxUncompressedCertificate.
	ErrAbsoluteLimitExceeded = errors.New("compressed certificate: decompressed output exceeds absolute limit")

	// ErrLengthMismatch means decompression finished with fewer bytes than the
	// declared uncompressed_length.
	ErrLengthMismatch = errors.New("compressed certificate: decompressed length does not match declared length")

	// ErrDecompression means the payload is not valid for the algorithm,
	// including checksum failures or truncated streams.
	ErrDecompression = errors.New("compressed certificate: decompression failed")

	// ErrMalformedCertificate means the decompressed blob is not a well-formed
	// TLS Certificate message.
	ErrMalformedCertificate = errors.New("compressed certificate: malformed Certificate message")
)

// CompressedCertificate is the parsed outer CompressedCertificate message.
type CompressedCertificate struct {
	// Algorithm is the CertificateCompressionAlgorithm identifier carried on
	// the wire.
	Algorithm uint16

	// UncompressedLength is the uint24 length the peer claims for the
	// decompressed Certificate message. It is untrusted input and is enforced
	// rather than trusted.
	UncompressedLength uint32

	// Compressed is the raw compressed_certificate_message vector.
	Compressed []byte
}

// DecodedCertificate is the result of a successful decode: the recovered
// Certificate message together with the DER certificates it carries.
type DecodedCertificate struct {
	// Message is the exact decompressed Certificate message, verified to be
	// UncompressedLength bytes long.
	Message []byte

	// CertificateRequestContext is the opaque certificate_request_context
	// from RFC 8446 section 4.4.2 (empty for TLS 1.3 connections).
	CertificateRequestContext []byte

	// Certificates is the list of DER-encoded certificates, in wire order
	// (leaf first).
	Certificates [][]byte
}

// ParseCompressedCertificate parses a complete CompressedCertificate message
// without decompressing anything. It rejects trailing bytes so that callers
// always feed exactly one message.
func ParseCompressedCertificate(data []byte) (*CompressedCertificate, error) {
	if len(data) < compressedCertificateHeaderLen {
		return nil, fmt.Errorf("%w: need at least %d bytes, got %d",
			ErrTruncated, compressedCertificateHeaderLen, len(data))
	}

	algorithm := uint16(data[0])<<8 | uint16(data[1])
	uncompressedLength := uint32(data[2])<<16 | uint32(data[3])<<8 | uint32(data[4])
	compressedLength := int(data[5])<<16 | int(data[6])<<8 | int(data[7])

	if len(data)-compressedCertificateHeaderLen < compressedLength {
		return nil, fmt.Errorf("%w: compressed vector declares %d bytes but only %d remain",
			ErrTruncated, compressedLength, len(data)-compressedCertificateHeaderLen)
	}

	remainder := len(data) - compressedCertificateHeaderLen - compressedLength
	if remainder != 0 {
		return nil, fmt.Errorf("%w: %d trailing bytes after compressed vector",
			ErrTruncated, remainder)
	}

	compressed := data[compressedCertificateHeaderLen : compressedCertificateHeaderLen+compressedLength]
	return &CompressedCertificate{
		Algorithm:          algorithm,
		UncompressedLength: uncompressedLength,
		Compressed:         compressed,
	}, nil
}

// DecodeCompressedCertificate decodes one RFC 8879 CompressedCertificate
// message and returns the recovered TLS Certificate message.
//
// It performs the security decision described for the TLS stack:
//   - An unknown algorithm identifier fails the decode (ErrUnknownAlgorithm).
//   - Decompression is bounded: it can never emit more than the declared
//     uncompressed_length, and never more than MaxUncompressedCertificate.
//     Either bound being exceeded fails the decode without allocating beyond
//     the bound.
//   - A stream that ends short of the declared length fails (ErrLengthMismatch).
//
// Only inputs whose decompressed length equals the declared length are parsed
// as a Certificate message and returned for path validation by the caller.
func DecodeCompressedCertificate(data []byte) (*DecodedCertificate, error) {
	cc, err := ParseCompressedCertificate(data)
	if err != nil {
		return nil, err
	}
	if cc.Algorithm != AlgorithmZlib {
		return nil, fmt.Errorf("%w: algorithm %d", ErrUnknownAlgorithm, cc.Algorithm)
	}
	if cc.UncompressedLength > MaxUncompressedCertificate {
		return nil, fmt.Errorf("%w: declared %d bytes, limit %d",
			ErrAbsoluteLimitExceeded, cc.UncompressedLength, MaxUncompressedCertificate)
	}

	message, err := inflateBounded(cc.Compressed, cc.UncompressedLength)
	if err != nil {
		return nil, err
	}

	return parseCertificateMessage(message)
}

// inflateBounded decompresses payload while guaranteeing that at most
// declaredLength bytes are ever written, and that the exact stream length
// matches declaredLength. Output beyond declaredLength is reported as
// ErrDeclaredLengthExceeded; reaching the hard ceiling first reports
// ErrAbsoluteLimitExceeded.
func inflateBounded(payload []byte, declaredLength uint32) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecompression, err)
	}

	out := make([]byte, 0, declaredLength)
	chunk := make([]byte, 4096)
	for {
		n, readErr := zr.Read(chunk)
		out = append(out, chunk[:n]...)

		if uint32(len(out)) > MaxUncompressedCertificate {
			zr.Close()
			return nil, fmt.Errorf("%w: at least %d bytes produced, limit %d",
				ErrAbsoluteLimitExceeded, len(out), MaxUncompressedCertificate)
		}
		if uint32(len(out)) > declaredLength {
			zr.Close()
			return nil, fmt.Errorf("%w: declared %d bytes but stream contains more",
				ErrDeclaredLengthExceeded, declaredLength)
		}

		if readErr != nil {
			if readErr != io.EOF {
				zr.Close()
				return nil, fmt.Errorf("%w: %v", ErrDecompression, readErr)
			}
			break
		}
	}
	if err := zr.Close(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecompression, err)
	}

	if uint32(len(out)) != declaredLength {
		return nil, fmt.Errorf("%w: declared %d, got %d",
			ErrLengthMismatch, declaredLength, len(out))
	}
	return out, nil
}

// parseCertificateMessage parses the TLS 1.3 Certificate message (RFC 8446
// section 4.4.2) wrapped in the compressed Certificate handshake message:
//
//	struct {
//	    opaque certificate_request_context<0..2^8-1>;
//	    CertificateEntry certificate_list<0..2^24-1>;
//	} Certificate;
//
//	struct {
//	    opaque cert_data<1..2^24-1>;
//	    Extension extensions<0..2^16-1>;
//	} CertificateEntry;
func parseCertificateMessage(message []byte) (*DecodedCertificate, error) {
	r := bytes.NewReader(message)

	contextLen, err := r.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("%w: cannot read request context length", ErrMalformedCertificate)
	}
	context := make([]byte, contextLen)
	if _, err := io.ReadFull(r, context); err != nil {
		return nil, fmt.Errorf("%w: truncated request context", ErrMalformedCertificate)
	}

	listLen, err := readUint24(r)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot read certificate list length", ErrMalformedCertificate)
	}
	if listLen > uint32(r.Len()) {
		return nil, fmt.Errorf("%w: certificate list declares %d bytes but only %d remain",
			ErrMalformedCertificate, listLen, r.Len())
	}
	listEnd := r.Len() - int(listLen)

	var certs [][]byte
	for r.Len() > listEnd {
		certLen, err := readUint24(r)
		if err != nil {
			return nil, fmt.Errorf("%w: cannot read certificate length", ErrMalformedCertificate)
		}
		if certLen == 0 || certLen > uint32(r.Len()) {
			return nil, fmt.Errorf("%w: invalid certificate length %d", ErrMalformedCertificate, certLen)
		}
		der := make([]byte, certLen)
		if _, err := io.ReadFull(r, der); err != nil {
			return nil, fmt.Errorf("%w: truncated certificate data", ErrMalformedCertificate)
		}

		extLen, err := readUint16(r)
		if err != nil {
			return nil, fmt.Errorf("%w: cannot read extension length", ErrMalformedCertificate)
		}
		if uint32(extLen) > uint32(r.Len()) {
			return nil, fmt.Errorf("%w: extensions declare %d bytes but only %d remain",
				ErrMalformedCertificate, extLen, r.Len())
		}
		if _, err := io.CopyN(io.Discard, r, int64(extLen)); err != nil {
			return nil, fmt.Errorf("%w: truncated extensions", ErrMalformedCertificate)
		}
		certs = append(certs, der)
	}

	if r.Len() != 0 {
		return nil, fmt.Errorf("%w: %d trailing bytes", ErrMalformedCertificate, r.Len())
	}

	return &DecodedCertificate{
		Message:                   message,
		CertificateRequestContext: context,
		Certificates:              certs,
	}, nil
}

func readUint24(r *bytes.Reader) (uint32, error) {
	var b [3]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2]), nil
}

func readUint16(r *bytes.Reader) (uint16, error) {
	var b [2]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return uint16(b[0])<<8 | uint16(b[1]), nil
}

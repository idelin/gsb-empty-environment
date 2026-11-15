package netsec

import (
	"encoding/binary"
	"fmt"
)

// CompressedCertificate is the body of a CompressedCertificate(25) handshake
// message as defined by RFC 8879.
type CompressedCertificate struct {
	Algorithm          Algorithm
	UncompressedLength uint32
	Compressed         []byte
}

// CompressedCertificateMessage is an alias for CompressedCertificate.
type CompressedCertificateMessage = CompressedCertificate

// MarshalCompressedCertificate encodes the CompressedCertificate message body.
func MarshalCompressedCertificate(message *CompressedCertificate) ([]byte, error) {
	if message == nil {
		return nil, badCertificate("nil CompressedCertificate", nil)
	}
	if message.UncompressedLength > MaxUncompressedLength {
		return nil, badCertificate("uncompressed_length exceeds uint24", ErrUncompressedLengthTooLarge)
	}
	if len(message.Compressed) == 0 || len(message.Compressed) > MaxUncompressedLength {
		return nil, fmt.Errorf("%w: %d", ErrInvalidCompressedLength, len(message.Compressed))
	}

	body := make([]byte, 8+len(message.Compressed))
	binary.BigEndian.PutUint16(body[0:2], uint16(message.Algorithm))
	body[2] = byte(message.UncompressedLength >> 16)
	body[3] = byte(message.UncompressedLength >> 8)
	body[4] = byte(message.UncompressedLength)
	body[5] = byte(len(message.Compressed) >> 16)
	body[6] = byte(len(message.Compressed) >> 8)
	body[7] = byte(len(message.Compressed))
	copy(body[8:], message.Compressed)
	return body, nil
}

// Marshal encodes the CompressedCertificate message body.
func (message *CompressedCertificate) Marshal() ([]byte, error) {
	return MarshalCompressedCertificate(message)
}

// ParseCompressedCertificate decodes a CompressedCertificate message body.
func ParseCompressedCertificate(body []byte) (*CompressedCertificate, error) {
	if len(body) < 8 {
		return nil, badCertificate("CompressedCertificate too short", nil)
	}
	algorithm := Algorithm(binary.BigEndian.Uint16(body[0:2]))
	uncompressedLength := uint32(body[2])<<16 | uint32(body[3])<<8 | uint32(body[4])
	if uncompressedLength > MaxUncompressedLength {
		return nil, badCertificate("uncompressed_length exceeds uint24", ErrUncompressedLengthTooLarge)
	}
	compressedLength, ok := readUint24(body[5:8])
	if !ok || compressedLength == 0 || compressedLength > MaxUncompressedLength {
		return nil, fmt.Errorf("%w: %d", ErrInvalidCompressedLength, compressedLength)
	}
	if len(body) != 8+compressedLength {
		return nil, badCertificate("compressed_certificate_message length mismatch", nil)
	}
	return &CompressedCertificate{
		Algorithm:          algorithm,
		UncompressedLength: uncompressedLength,
		Compressed:         append([]byte(nil), body[8:]...),
	}, nil
}

// UnmarshalCompressedCertificate is an alias for ParseCompressedCertificate.
func UnmarshalCompressedCertificate(body []byte) (*CompressedCertificate, error) {
	return ParseCompressedCertificate(body)
}

// DecompressCertificate decompresses a CompressedCertificate using the
// process-wide codec registry and an explicitly supplied dictionary.
//
// A declared length above 2^24-1, any decompression failure, or any output
// length that differs from UncompressedLength is reported as bad_certificate.
func DecompressCertificate(message *CompressedCertificate, dictionary []byte) ([]byte, error) {
	return decompressCertificateWithCodecs(message, dictionary, DefaultCodecRegistry())
}

// DecompressCertificateWith decompresses using codecs.
func DecompressCertificateWith(message *CompressedCertificate, dictionary []byte, codecs *CodecRegistry) ([]byte, error) {
	return decompressCertificateWithCodecs(message, dictionary, codecs)
}

func decompressCertificateWithCodecs(message *CompressedCertificate, dictionary []byte, codecs *CodecRegistry) ([]byte, error) {
	if message == nil {
		return nil, badCertificate("nil CompressedCertificate", nil)
	}
	if message.UncompressedLength > MaxUncompressedLength {
		return nil, badCertificate("uncompressed_length exceeds uint24", ErrUncompressedLengthTooLarge)
	}
	if len(message.Compressed) == 0 || len(message.Compressed) > MaxUncompressedLength {
		return nil, fmt.Errorf("%w: %d", ErrInvalidCompressedLength, len(message.Compressed))
	}
	codec, ok := codecs.Lookup(message.Algorithm)
	if !ok {
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedAlgorithm, message.Algorithm)
	}

	plain, err := codec.Decompress(message.Compressed, dictionary, int(message.UncompressedLength))
	if err != nil {
		return nil, badCertificate("decompression failed", err)
	}
	if len(plain) != int(message.UncompressedLength) {
		return nil, badCertificate(
			fmt.Sprintf("decompressed length %d does not match declared %d", len(plain), message.UncompressedLength),
			ErrUncompressedLengthMismatch,
		)
	}
	if len(plain) > MaxUncompressedLength {
		return nil, badCertificate("decompressed certificate exceeds uint24", ErrUncompressedLengthTooLarge)
	}
	return plain, nil
}

// CertificateResult is the handshake-path output of Connection.CompressCertificate.
// Body is either a Certificate message body (HandshakeTypeCertificate) or a
// CompressedCertificate message body (HandshakeTypeCompressedCertificate).
type CertificateResult struct {
	HandshakeType uint8
	Body          []byte
	Compressed    bool
	Algorithm     Algorithm
	DictionaryID  DictionaryID
	CacheHit      bool
}

// MarshalHandshake prepends the handshake type and uint24 length.
func (result CertificateResult) MarshalHandshake() ([]byte, error) {
	if len(result.Body) > MaxUncompressedLength {
		return nil, fmt.Errorf("%w: handshake body too long", ErrInvalidCertificateMessage)
	}
	message := make([]byte, 4+len(result.Body))
	message[0] = result.HandshakeType
	message[1] = byte(len(result.Body) >> 16)
	message[2] = byte(len(result.Body) >> 8)
	message[3] = byte(len(result.Body))
	copy(message[4:], result.Body)
	return message, nil
}

package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// InstallCodeCRC16Length is the number of CRC bytes appended to an
	// installation code.
	InstallCodeCRC16Length = 2

	InstallCodeLength6  = 6
	InstallCodeLength8  = 8
	InstallCodeLength12 = 12
	InstallCodeLength16 = 16
)

var (
	// ErrInvalidInstallCodeLength indicates that the payload length is not
	// one of the lengths permitted by the Zigbee install-code specification.
	ErrInvalidInstallCodeLength = errors.New("install code length must be 6, 8, 12, or 16 bytes")

	// ErrInvalidInstallCodeCRC indicates that the CRC-16/X-25 suffix does
	// not match the install-code payload.
	ErrInvalidInstallCodeCRC = errors.New("install code CRC-16/X-25 is invalid")

	// Length and CRC aliases keep callers from having to depend on one
	// particular spelling of the sentinel errors.
	ErrInstallCodeLength = ErrInvalidInstallCodeLength
	ErrInstallCodeCRC    = ErrInvalidInstallCodeCRC
	ErrInvalidCRC        = ErrInvalidInstallCodeCRC
)

// ValidInstallCodeLength reports whether n is a permitted install-code
// payload length. The two CRC bytes are not included in n.
func ValidInstallCodeLength(n int) bool {
	switch n {
	case InstallCodeLength6, InstallCodeLength8, InstallCodeLength12, InstallCodeLength16:
		return true
	default:
		return false
	}
}

// CRC16X25 computes the CRC-16/X-25 checksum used by Zigbee install codes.
//
// Parameters: polynomial 0x1021, initial value 0xFFFF, reflected input and
// output, and final XOR 0xFFFF. For example, CRC16X25([]byte("123456789"))
// returns 0x906E.
func CRC16X25(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0x8408
			} else {
				crc >>= 1
			}
		}
	}
	return crc ^ 0xFFFF
}

// CRC16X25Bytes returns the CRC-16/X-25 checksum in the little-endian form
// appended to Zigbee install codes.
func CRC16X25Bytes(data []byte) []byte {
	out := make([]byte, 2)
	binary.LittleEndian.PutUint16(out, CRC16X25(data))
	return out
}

// EncodeInstallCode validates a bare install code and returns the payload
// followed by its little-endian CRC-16/X-25 checksum.
func EncodeInstallCode(code []byte) ([]byte, error) {
	if !ValidInstallCodeLength(len(code)) {
		return nil, fmt.Errorf("%w: got %d payload bytes", ErrInvalidInstallCodeLength, len(code))
	}

	out := make([]byte, len(code)+InstallCodeCRC16Length)
	copy(out, code)
	binary.LittleEndian.PutUint16(out[len(code):], CRC16X25(code))
	return out, nil
}

// AppendCRC16X25 is an alias for EncodeInstallCode.
func AppendCRC16X25(code []byte) ([]byte, error) {
	return EncodeInstallCode(code)
}

// BuildInstallCode is an alias for EncodeInstallCode.
func BuildInstallCode(code []byte) ([]byte, error) {
	return EncodeInstallCode(code)
}

// ParseInstallCode validates a wire-format install code and returns a copy
// of its payload. A wire-format value consists of exactly 6, 8, 12, or 16
// payload bytes followed by a little-endian CRC-16/X-25 value.
func ParseInstallCode(raw []byte) ([]byte, error) {
	if len(raw) < InstallCodeCRC16Length {
		return nil, fmt.Errorf("%w: got %d total bytes", ErrInvalidInstallCodeLength, len(raw))
	}

	payloadLength := len(raw) - InstallCodeCRC16Length
	if !ValidInstallCodeLength(payloadLength) {
		return nil, fmt.Errorf("%w: got %d payload bytes", ErrInvalidInstallCodeLength, payloadLength)
	}

	code := raw[:payloadLength]
	want := binary.LittleEndian.Uint16(raw[payloadLength:])
	if CRC16X25(code) != want {
		return nil, ErrInvalidInstallCodeCRC
	}

	return bytes.Clone(code), nil
}

// DecodeInstallCode is an alias for ParseInstallCode.
func DecodeInstallCode(raw []byte) ([]byte, error) {
	return ParseInstallCode(raw)
}

// ValidateInstallCode checks the payload length and CRC of a wire-format
// install code.
func ValidateInstallCode(raw []byte) error {
	_, err := ParseInstallCode(raw)
	return err
}

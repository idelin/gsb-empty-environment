package netsec

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"testing"
)

func TestCRC16X25CheckValue(t *testing.T) {
	const want = 0x906E
	if got := CRC16X25([]byte("123456789")); got != want {
		t.Fatalf("CRC16X25 check value = %#x, want %#x", got, want)
	}
}

func TestInstallCodeCRCKnownVector(t *testing.T) {
	code, err := hex.DecodeString("83FED3407A939723A5C639B26916D505")
	if err != nil {
		t.Fatal(err)
	}
	if got := CRC16X25(code); got != 0xB5C3 {
		t.Fatalf("CRC = %#x, want 0xB5C3", got)
	}

	raw, err := EncodeInstallCode(code)
	if err != nil {
		t.Fatal(err)
	}
	if raw[len(raw)-2] != 0xC3 || raw[len(raw)-1] != 0xB5 {
		t.Fatalf("CRC suffix = %x %x, want c3 b5", raw[len(raw)-2], raw[len(raw)-1])
	}
}

func TestEncodeInstallCodeLittleEndianCRC(t *testing.T) {
	code := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	raw, err := EncodeInstallCode(code)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != len(code)+InstallCodeCRC16Length {
		t.Fatalf("encoded length = %d, want %d", len(raw), len(code)+InstallCodeCRC16Length)
	}
	if got := binary.LittleEndian.Uint16(raw[len(code):]); got != CRC16X25(code) {
		t.Fatalf("CRC suffix = %#x, want %#x", got, CRC16X25(code))
	}

	parsed, err := ParseInstallCode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(parsed, code) {
		t.Fatalf("parsed code = %x, want %x", parsed, code)
	}
}

func TestInstallCodePayloadLengths(t *testing.T) {
	valid := []int{6, 8, 12, 16}
	for _, length := range valid {
		code := bytes.Repeat([]byte{0xA5}, length)
		raw, err := EncodeInstallCode(code)
		if err != nil {
			t.Fatalf("length %d: %v", length, err)
		}
		if _, err := ParseInstallCode(raw); err != nil {
			t.Fatalf("length %d: %v", length, err)
		}
	}

	for _, length := range []int{0, 1, 5, 7, 9, 10, 11, 13, 14, 15, 17} {
		code := bytes.Repeat([]byte{0xA5}, length)
		if _, err := EncodeInstallCode(code); !errors.Is(err, ErrInvalidInstallCodeLength) {
			t.Fatalf("length %d: got %v, want length error", length, err)
		}
	}

	// 14 payload bytes plus two CRC bytes is not one of the permitted sizes.
	raw := bytes.Repeat([]byte{0xA5}, 14)
	raw = append(raw, CRC16X25Bytes(raw[:14])...)
	if _, err := ParseInstallCode(raw); !errors.Is(err, ErrInvalidInstallCodeLength) {
		t.Fatalf("14-byte payload: got %v, want length error", err)
	}
}

func TestParseInstallCodeRejectsCorruptCRC(t *testing.T) {
	code := []byte{0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x70, 0x80}
	raw, err := EncodeInstallCode(code)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0x01

	if _, err := ParseInstallCode(raw); !errors.Is(err, ErrInvalidInstallCodeCRC) {
		t.Fatalf("got %v, want CRC error", err)
	}
}

func TestParseInstallCodeRejectsExtraBytes(t *testing.T) {
	code := []byte{1, 2, 3, 4, 5, 6}
	raw, err := EncodeInstallCode(code)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, 0)

	if _, err := ParseInstallCode(raw); !errors.Is(err, ErrInvalidInstallCodeLength) {
		t.Fatalf("got %v, want length error", err)
	}
}

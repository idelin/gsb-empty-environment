package netsec

import (
	"errors"
)

// StrKey implements the Stellar account ID encoding used in G addresses.
// Format: base32(versionByte || payload || CRC16-XModem(versionByte||payload)).

const (
	strkeyVersionAccountID byte = 6 << 3 // 0x30
	ed25519PublicKeySize        = 32
)

// StrKey alphabet: RFC 4648 base32 without padding.
const strkeyAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

var strkeyDecodeTable [256]byte

func init() {
	for i := range strkeyDecodeTable {
		strkeyDecodeTable[i] = 0xFF
	}
	for i := 0; i < len(strkeyAlphabet); i++ {
		strkeyDecodeTable[strkeyAlphabet[i]] = byte(i)
	}
}

var (
	// ErrInvalidStrKey means the address failed decoding, checksum, or length checks.
	ErrInvalidStrKey = errors.New("netsec: invalid stellar strkey")
)

// EncodeAccountID renders a 32-byte ed25519 public key as a G-address.
func EncodeAccountID(publicKey []byte) (string, error) {
	if len(publicKey) != ed25519PublicKeySize {
		return "", ErrInvalidStrKey
	}
	payload := make([]byte, 0, 1+ed25519PublicKeySize+2)
	payload = append(payload, strkeyVersionAccountID)
	payload = append(payload, publicKey...)
	payload = append(payload, crc16XModem(payload)...)
	return base32NoPaddingEncode(payload), nil
}

// DecodeAccountID parses a G-address and returns the 32-byte public key.
func DecodeAccountID(address string) ([]byte, error) {
	raw, err := base32NoPaddingDecode(address)
	if err != nil {
		return nil, ErrInvalidStrKey
	}
	if len(raw) != 1+ed25519PublicKeySize+2 {
		return nil, ErrInvalidStrKey
	}
	if raw[0] != strkeyVersionAccountID {
		return nil, ErrInvalidStrKey
	}
	checksum := crc16XModem(raw[:1+ed25519PublicKeySize])
	if checksum[0] != raw[len(raw)-2] || checksum[1] != raw[len(raw)-1] {
		return nil, ErrInvalidStrKey
	}
	out := make([]byte, ed25519PublicKeySize)
	copy(out, raw[1:1+ed25519PublicKeySize])
	return out, nil
}

// crc16XModem returns the two-byte big-endian CRC-16/CCITT with init 0x0000
// (the variant Stellar uses for StrKey checksums).
func crc16XModem(data []byte) []byte {
	var crc uint16
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return []byte{byte(crc >> 8), byte(crc)}
}

func base32NoPaddingEncode(src []byte) string {
	out := make([]byte, 0, (len(src)*8+4)/5)
	var bits uint32
	var nbits uint
	for _, b := range src {
		bits = (bits << 8) | uint32(b)
		nbits += 8
		for nbits >= 5 {
			nbits -= 5
			idx := byte((bits >> nbits) & 0x1F)
			out = append(out, strkeyAlphabet[idx])
		}
	}
	if nbits > 0 {
		idx := byte((bits << (5 - nbits)) & 0x1F)
		out = append(out, strkeyAlphabet[idx])
	}
	return string(out)
}

func base32NoPaddingDecode(s string) ([]byte, error) {
	out := make([]byte, 0, len(s)*5/8)
	var bits uint32
	var nbits uint
	for i := 0; i < len(s); i++ {
		v := strkeyDecodeTable[s[i]]
		if v == 0xFF {
			return nil, ErrInvalidStrKey
		}
		bits = (bits << 5) | uint32(v)
		nbits += 5
		if nbits >= 8 {
			nbits -= 8
			out = append(out, byte((bits>>nbits)&0xFF))
		}
	}
	return out, nil
}

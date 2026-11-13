package netsec

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
)

// decodeKeyring converts either binary OpenPGP data or one or more ASCII-armored
// public key blocks into binary packet data.
func decodeKeyring(data []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("%w: empty input", ErrInvalidPacket)
	}
	if trimmed[0]&0x80 != 0 {
		return trimmed, nil
	}
	if !bytes.Contains(trimmed, []byte("-----BEGIN PGP")) {
		return nil, fmt.Errorf("%w: input is neither binary nor armored OpenPGP", ErrInvalidPacket)
	}
	return decodeArmor(trimmed)
}

func decodeArmor(data []byte) ([]byte, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	var out []byte
	found := false

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "-----BEGIN PGP ") ||
			!strings.HasSuffix(line, "-----") {
			continue
		}
		found = true
		label := strings.TrimSuffix(strings.TrimPrefix(line, "-----BEGIN PGP "), "-----")
		if strings.Contains(label, "SECRET") {
			return nil, fmt.Errorf("%w: secret key armor is not accepted", ErrInvalidPacket)
		}

		var encoded strings.Builder
		var checksum string
		for i++; i < len(lines); i++ {
			bodyLine := strings.TrimSpace(lines[i])
			if strings.HasPrefix(bodyLine, "-----END PGP ") {
				break
			}
			if bodyLine == "" || strings.Contains(bodyLine, ": ") {
				continue
			}
			if strings.HasPrefix(bodyLine, "=") {
				checksum = strings.TrimPrefix(bodyLine, "=")
				continue
			}
			encoded.WriteString(bodyLine)
		}
		if i == len(lines) {
			return nil, fmt.Errorf("%w: unterminated ASCII armor", ErrInvalidPacket)
		}

		block, err := base64.StdEncoding.DecodeString(encoded.String())
		if err != nil {
			return nil, fmt.Errorf("%w: invalid ASCII armor base64: %v", ErrInvalidPacket, err)
		}
		if checksum != "" {
			got, err := base64.StdEncoding.DecodeString(checksum)
			if err != nil || len(got) != 3 {
				return nil, fmt.Errorf("%w: invalid ASCII armor checksum", ErrInvalidPacket)
			}
			want := crc24(block)
			if uint32(got[0])<<16|uint32(got[1])<<8|uint32(got[2]) != want {
				return nil, fmt.Errorf("%w: ASCII armor checksum mismatch", ErrInvalidPacket)
			}
		}
		out = append(out, block...)
	}
	if !found || len(out) == 0 {
		return nil, fmt.Errorf("%w: no armored OpenPGP block found", ErrInvalidPacket)
	}
	return out, nil
}

func crc24(data []byte) uint32 {
	crc := uint32(0xb704ce)
	for _, b := range data {
		crc ^= uint32(b) << 16
		for i := 0; i < 8; i++ {
			crc <<= 1
			if crc&0x1000000 != 0 {
				crc ^= 0x1864cfb
			}
		}
	}
	return crc & 0xffffff
}

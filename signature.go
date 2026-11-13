package netsec

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"time"
)

const (
	subpacketSignatureCreationTime = 2
	subpacketRevocationKey         = 12
	subpacketIssuerKeyID           = 16
	subpacketIssuerFingerprint     = 33
)

// Subpacket is a signature subpacket.  Data excludes the subpacket type octet.
type Subpacket struct {
	Type     uint8
	Critical bool
	Data     []byte
}

// RevocationKey is the parsed content of a Revocation Key subpacket (type 12).
// It always identifies the revoker by full fingerprint.
type RevocationKey struct {
	Class       uint8
	Algorithm   PublicKeyAlgorithm
	Fingerprint []byte
}

// Signature is an OpenPGP signature packet.
type Signature struct {
	Version         uint8
	Type            SignatureType
	PubKeyAlgorithm PublicKeyAlgorithm
	HashAlgorithm   HashAlgorithm

	HashedSubpackets   []Subpacket
	UnhashedSubpackets []Subpacket

	HashTag  []byte
	Material []byte
	Body     []byte

	header []byte
}

// ParseSignature parses a single binary or ASCII-armored OpenPGP signature
// packet.
func ParseSignature(data []byte) (*Signature, error) {
	binaryData, err := decodeKeyring(data)
	if err != nil {
		return nil, err
	}
	packets, err := parsePackets(binaryData)
	if err != nil {
		return nil, err
	}
	if len(packets) != 1 || packets[0].tag != 2 {
		return nil, fmt.Errorf("%w: expected exactly one signature packet", ErrInvalidPacket)
	}
	return parseSignaturePacket(packets[0].body)
}

func parseSignaturePacket(body []byte) (*Signature, error) {
	if len(body) < 1 {
		return nil, fmt.Errorf("%w: empty signature packet", ErrInvalidPacket)
	}

	sig := &Signature{Version: body[0]}
	var hashedLen, unhashedLen int
	var off int
	switch sig.Version {
	case 4:
		if len(body) < 6 {
			return nil, fmt.Errorf("%w: short v4 signature packet", ErrInvalidPacket)
		}
		sig.Type = SignatureType(body[1])
		sig.PubKeyAlgorithm = PublicKeyAlgorithm(body[2])
		sig.HashAlgorithm = HashAlgorithm(body[3])
		hashedLen = int(binary.BigEndian.Uint16(body[4:6]))
		off = 6
	case 5, 6:
		if len(body) < 8 {
			return nil, fmt.Errorf("%w: short v%d signature packet", ErrInvalidPacket, sig.Version)
		}
		sig.Type = SignatureType(body[1])
		sig.PubKeyAlgorithm = PublicKeyAlgorithm(body[2])
		sig.HashAlgorithm = HashAlgorithm(body[3])
		hashedLen = int(binary.BigEndian.Uint32(body[4:8]))
		off = 8
	default:
		return nil, fmt.Errorf("%w: unsupported signature version %d", ErrUnsupportedAlgorithm, sig.Version)
	}

	if hashedLen < 0 || off+hashedLen > len(body) {
		return nil, fmt.Errorf("%w: truncated hashed signature subpackets", ErrInvalidPacket)
	}
	hashed, err := parseSubpackets(body[off : off+hashedLen])
	if err != nil {
		return nil, err
	}
	sig.HashedSubpackets = hashed
	sig.header = append([]byte(nil), body[:off+hashedLen]...)
	off += hashedLen

	if sig.Version == 4 {
		if off+2 > len(body) {
			return nil, fmt.Errorf("%w: missing unhashed subpacket length", ErrInvalidPacket)
		}
		unhashedLen = int(binary.BigEndian.Uint16(body[off : off+2]))
		off += 2
	} else {
		if off+4 > len(body) {
			return nil, fmt.Errorf("%w: missing unhashed subpacket length", ErrInvalidPacket)
		}
		unhashedLen = int(binary.BigEndian.Uint32(body[off : off+4]))
		off += 4
	}
	if unhashedLen < 0 || off+unhashedLen > len(body) {
		return nil, fmt.Errorf("%w: truncated unhashed signature subpackets", ErrInvalidPacket)
	}
	unhashed, err := parseSubpackets(body[off : off+unhashedLen])
	if err != nil {
		return nil, err
	}
	sig.UnhashedSubpackets = unhashed
	off += unhashedLen

	if off+2 > len(body) {
		return nil, fmt.Errorf("%w: missing signature hash tag", ErrInvalidPacket)
	}
	sig.HashTag = append([]byte(nil), body[off:off+2]...)
	off += 2
	sig.Material = append([]byte(nil), body[off:]...)
	sig.Body = append([]byte(nil), body...)
	return sig, nil
}

func parseSubpackets(data []byte) ([]Subpacket, error) {
	var out []Subpacket
	for off := 0; off < len(data); {
		length, headerLen, err := readSubpacketLength(data[off:])
		if err != nil {
			return nil, err
		}
		if length < 1 || off+headerLen+length > len(data) {
			return nil, fmt.Errorf("%w: invalid signature subpacket length", ErrInvalidPacket)
		}
		content := data[off+headerLen : off+headerLen+length]
		typeOctet := content[0]
		out = append(out, Subpacket{
			Type:     typeOctet & 0x7f,
			Critical: typeOctet&0x80 != 0,
			Data:     append([]byte(nil), content[1:]...),
		})
		off += headerLen + length
	}
	return out, nil
}

func readSubpacketLength(data []byte) (length, headerLen int, err error) {
	if len(data) < 1 {
		return 0, 0, fmt.Errorf("%w: empty signature subpacket", ErrInvalidPacket)
	}
	first := data[0]
	switch {
	case first < 192:
		return int(first), 1, nil
	case first < 255:
		if len(data) < 2 {
			return 0, 0, fmt.Errorf("%w: truncated signature subpacket length", ErrInvalidPacket)
		}
		return ((int(first)-192)<<8 + int(data[1]) + 192), 2, nil
	default:
		if len(data) < 5 {
			return 0, 0, fmt.Errorf("%w: truncated signature subpacket length", ErrInvalidPacket)
		}
		n := binary.BigEndian.Uint32(data[1:5])
		if uint64(n) > uint64(len(data)-5) {
			return 0, 0, fmt.Errorf("%w: signature subpacket overflows packet", ErrInvalidPacket)
		}
		return int(n), 5, nil
	}
}

// CreationTime returns the signature creation time from the hashed area.  The
// unhashed area is not trusted for security-relevant metadata.
func (s *Signature) CreationTime() (time.Time, bool) {
	for _, p := range s.HashedSubpackets {
		if p.Type == subpacketSignatureCreationTime && len(p.Data) == 4 {
			return time.Unix(int64(binary.BigEndian.Uint32(p.Data)), 0).UTC(), true
		}
	}
	return time.Time{}, false
}

// HashedIssuerKeyIDs returns issuer key IDs from the signed subpacket area.
func (s *Signature) HashedIssuerKeyIDs() [][]byte {
	return issuerKeyIDs(s.HashedSubpackets)
}

// IssuerKeyIDs returns issuer key IDs from both subpacket areas.  Values in
// the unhashed area are attacker-controlled and must not be used as the sole
// basis for trust.
func (s *Signature) IssuerKeyIDs() [][]byte {
	return issuerKeyIDs(append(append([]Subpacket(nil), s.HashedSubpackets...), s.UnhashedSubpackets...))
}

func issuerKeyIDs(packets []Subpacket) [][]byte {
	var out [][]byte
	for _, p := range packets {
		if p.Type == subpacketIssuerKeyID && len(p.Data) == 8 {
			out = append(out, append([]byte(nil), p.Data...))
		}
	}
	return out
}

// HashedIssuerFingerprints returns complete issuer fingerprints from the
// signed subpacket area.
func (s *Signature) HashedIssuerFingerprints() [][]byte {
	return issuerFingerprints(s.HashedSubpackets)
}

// IssuerFingerprints returns issuer fingerprints from both subpacket areas.
func (s *Signature) IssuerFingerprints() [][]byte {
	return issuerFingerprints(append(append([]Subpacket(nil), s.HashedSubpackets...), s.UnhashedSubpackets...))
}

func issuerFingerprints(packets []Subpacket) [][]byte {
	var out [][]byte
	for _, p := range packets {
		if p.Type != subpacketIssuerFingerprint {
			continue
		}
		if len(p.Data) == 20 || len(p.Data) == 32 {
			out = append(out, append([]byte(nil), p.Data...))
			continue
		}
		if len(p.Data) == 21 || len(p.Data) == 33 {
			version := p.Data[0]
			expected := 0
			switch version {
			case 4:
				expected = 20
			case 5, 6:
				expected = 32
			}
			if expected != 0 && len(p.Data) == expected+1 {
				out = append(out, append([]byte(nil), p.Data[1:]...))
			}
		}
	}
	return out
}

// RevocationKeys returns designated-revoker declarations from the hashed
// subpacket area.  Declarations in the unhashed area are intentionally ignored:
// they are not covered by the self-signature and could be injected by an
// attacker.
func (s *Signature) RevocationKeys() []RevocationKey {
	var out []RevocationKey
	for _, p := range s.HashedSubpackets {
		if p.Type != subpacketRevocationKey || len(p.Data) < 2 {
			continue
		}
		fingerprint := append([]byte(nil), p.Data[2:]...)
		if len(fingerprint) != 20 && len(fingerprint) != 32 {
			continue
		}
		out = append(out, RevocationKey{
			Class:       p.Data[0],
			Algorithm:   PublicKeyAlgorithm(p.Data[1]),
			Fingerprint: fingerprint,
		})
	}
	return out
}

func (s *Signature) hashTrailer() []byte {
	if s.Version == 4 {
		trailer := make([]byte, 6)
		trailer[0] = 4
		trailer[1] = 0xff
		binary.BigEndian.PutUint32(trailer[2:], uint32(len(s.header)))
		return trailer
	}
	trailer := make([]byte, 10)
	trailer[0] = s.Version
	trailer[1] = 0xff
	binary.BigEndian.PutUint64(trailer[2:], uint64(len(s.header)))
	return trailer
}

func (s *Signature) hashInput(signedData []byte) []byte {
	buf := make([]byte, 0, len(signedData)+len(s.header)+len(s.hashTrailer()))
	buf = append(buf, signedData...)
	buf = append(buf, s.header...)
	buf = append(buf, s.hashTrailer()...)
	return buf
}

func canonicalUserIDData(userID []byte) []byte {
	data := make([]byte, 5+len(userID))
	data[0] = 0xb4
	binary.BigEndian.PutUint32(data[1:5], uint32(len(userID)))
	copy(data[5:], userID)
	return data
}

func canonicalUserAttributeData(attribute []byte) []byte {
	data := make([]byte, 5+len(attribute))
	data[0] = 0xd1
	binary.BigEndian.PutUint32(data[1:5], uint32(len(attribute)))
	copy(data[5:], attribute)
	return data
}

func sameBytes(a, b []byte) bool {
	return len(a) == len(b) && bytes.Equal(a, b)
}

package netsec

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	magic   = "NETSC-TR"
	version = byte(1)
)

// Wire layouts (all integers big-endian, all variable fields length-prefixed).
//
//   capabilities: header | u16 count | repeat(u16 len | algorithm bytes)
//   selection:    header | u16 len | chosen | u32 hashA | u32 hashB
//   confirmation: header | u32 chainHash
//
// header = "NETSC-TR" | version | type.

func encodeHeader(recordType RecordType) []byte {
	out := make([]byte, 0, len(magic)+2)
	out = append(out, magic...)
	out = append(out, version, byte(recordType))
	return out
}

func decodeHeader(raw []byte) (RecordType, error) {
	if len(raw) < len(magic)+2 {
		return 0, errors.New("netsec: record shorter than header")
	}
	if string(raw[:len(magic)]) != magic {
		return 0, errors.New("netsec: bad record magic")
	}
	if raw[len(magic)] != version {
		return 0, fmt.Errorf("netsec: unsupported version %d", raw[len(magic)])
	}
	return RecordType(raw[len(magic)+1]), nil
}

func putUint16(dst []byte, value int) {
	binary.BigEndian.PutUint16(dst, uint16(value))
}

func putUint32(dst []byte, value int) {
	binary.BigEndian.PutUint32(dst, uint32(value))
}

func appendString16(dst []byte, value string) []byte {
	var prefix [2]byte
	putUint16(prefix[:], len(value))
	dst = append(dst, prefix[:]...)
	return append(dst, value...)
}

func readUint16(raw []byte, offset int) (int, int, error) {
	if offset+2 > len(raw) {
		return 0, offset, errors.New("netsec: truncated uint16 length")
	}
	return int(binary.BigEndian.Uint16(raw[offset:])), offset + 2, nil
}

func readBytes16(raw []byte, offset int) ([]byte, int, error) {
	length, next, err := readUint16(raw, offset)
	if err != nil {
		return nil, next, err
	}
	if next+length > len(raw) {
		return nil, next, errors.New("netsec: length-prefixed field exceeds record")
	}
	return raw[next : next+length], next + length, nil
}

func readHash32(raw []byte, offset int) ([sha256.Size]byte, int, error) {
	var digest [sha256.Size]byte
	if offset+sha256.Size > len(raw) {
		return digest, offset, errors.New("netsec: truncated hash")
	}
	copy(digest[:], raw[offset:offset+sha256.Size])
	return digest, offset + sha256.Size, nil
}

// EncodeCapabilities serializes an ordered capability list into raw bytes.
func EncodeCapabilities(recordType RecordType, algorithms []string) []byte {
	length := len(magic) + 2 + 2
	for _, alg := range algorithms {
		length += 2 + len(alg)
	}
	out := make([]byte, 0, length)
	out = append(out, encodeHeader(recordType)...)
	var count [2]byte
	putUint16(count[:], len(algorithms))
	out = append(out, count[:]...)
	for _, alg := range algorithms {
		out = appendString16(out, alg)
	}
	return out
}

func decodeCapsRaw(raw []byte) (RecordType, []string, error) {
	recordType, err := decodeHeader(raw)
	if err != nil {
		return 0, nil, err
	}
	headerLen := len(magic) + 2
	count, offset, err := readUint16(raw, headerLen)
	if err != nil {
		return recordType, nil, err
	}
	algorithms := make([]string, 0, count)
	for index := 0; index < count; index++ {
		value, next, readErr := readBytes16(raw, offset)
		if readErr != nil {
			return recordType, nil, readErr
		}
		algorithms = append(algorithms, string(value))
		offset = next
	}
	if offset != len(raw) {
		return recordType, nil, errors.New("netsec: trailing bytes after capability list")
	}
	return recordType, algorithms, nil
}

// EncodeSelection serializes a selection binding both observed capability
// byte strings via their SHA-256 hashes.
func EncodeSelection(chosen string, capsARaw, capsBRaw []byte) []byte {
	hashA := sha256.Sum256(capsARaw)
	hashB := sha256.Sum256(capsBRaw)
	out := make([]byte, 0, len(magic)+2+2+len(chosen)+2*sha256.Size)
	out = append(out, encodeHeader(TypeSelection)...)
	out = appendString16(out, chosen)
	out = append(out, hashA[:]...)
	out = append(out, hashB[:]...)
	return out
}

func decodeSelectionRaw(raw []byte) (string, [sha256.Size]byte, [sha256.Size]byte, error) {
	var zeroA, zeroB [sha256.Size]byte
	recordType, err := decodeHeader(raw)
	if err != nil {
		return "", zeroA, zeroB, err
	}
	if recordType != TypeSelection {
		return "", zeroA, zeroB, errors.New("netsec: selection has wrong record type")
	}
	headerLen := len(magic) + 2
	chosen, offset, err := readBytes16(raw, headerLen)
	if err != nil {
		return "", zeroA, zeroB, err
	}
	hashA, offset, err := readHash32(raw, offset)
	if err != nil {
		return "", zeroA, zeroB, err
	}
	hashB, offset, err := readHash32(raw, offset)
	if err != nil {
		return "", zeroA, zeroB, err
	}
	if offset != len(raw) {
		return "", zeroA, zeroB, errors.New("netsec: trailing bytes after selection")
	}
	return string(chosen), hashA, hashB, nil
}

// EncodeConfirmation serializes the final confirmation, binding the entire
// preceding transcript via chainHashOf.
func EncodeConfirmation(chain [sha256.Size]byte) []byte {
	out := make([]byte, 0, len(magic)+2+sha256.Size)
	out = append(out, encodeHeader(TypeConfirmation)...)
	out = append(out, chain[:]...)
	return out
}

func decodeConfirmRaw(raw []byte) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	recordType, err := decodeHeader(raw)
	if err != nil {
		return digest, err
	}
	if recordType != TypeConfirmation {
		return digest, errors.New("netsec: confirmation has wrong record type")
	}
	headerLen := len(magic) + 2
	hash, offset, err := readHash32(raw, headerLen)
	if err != nil {
		return digest, err
	}
	if offset != len(raw) {
		return digest, errors.New("netsec: trailing bytes after confirmation")
	}
	return hash, nil
}

// sha256Sum is a small helper that returns a slice rather than an array.
func sha256Sum(data []byte) []byte {
	digest := sha256.Sum256(data)
	return digest[:]
}

// chainHashOf binds the three authenticated transcript records in order.
func chainHashOf(capsARaw, capsBRaw, selectionRaw []byte) [sha256.Size]byte {
	hasher := sha256.New()
	hasher.Write([]byte("NETSC-CHAIN-V1"))
	writeLengthPrefixed(hasher, capsARaw)
	writeLengthPrefixed(hasher, capsBRaw)
	writeLengthPrefixed(hasher, selectionRaw)
	var out [sha256.Size]byte
	copy(out[:], hasher.Sum(nil))
	return out
}

func writeLengthPrefixed(hasher interface{ Write([]byte) (int, error) }, data []byte) {
	var prefix [4]byte
	putUint32(prefix[:], len(data))
	_, _ = hasher.Write(prefix[:])
	_, _ = hasher.Write(data)
}

// signingPayload builds the canonical byte string that is signed. It binds
// the domain (record type and signer role), the session identifier, the
// transcript position and the exact raw bytes, so a signature is invalid for
// any reordered, deleted, replayed or cross-session use.
func signingPayload(sessionID []byte, index int, raw []byte, recordType RecordType, party Party) []byte {
	payload := make([]byte, 0, 64+len(sessionID)+len(raw))
	payload = append(payload, "NETSC-SIG-V1|"...)
	payload = append(payload, byte(recordType), '|', byte(party), '|')
	var indexBytes [4]byte
	putUint32(indexBytes[:], index)
	payload = append(payload, indexBytes[:]...)
	payload = appendString16(payload, string(sessionID))
	var rawLength [4]byte
	putUint32(rawLength[:], len(raw))
	payload = append(payload, rawLength[:]...)
	payload = append(payload, raw...)
	return payload
}

// signRaw produces an Ed25519 mark over the canonical signing payload.
func signRaw(private ed25519.PrivateKey, sessionID []byte, index int, raw []byte, recordType RecordType, party Party) Mark {
	return Mark{
		Signer:    party,
		Signature: ed25519.Sign(private, signingPayload(sessionID, index, raw, recordType, party)),
	}
}

func (v *Verifier) trustedKey(party Party) (ed25519.PublicKey, bool) {
	switch party {
	case PartyA:
		return v.policy.PartyAKey, true
	case PartyB:
		return v.policy.PartyBKey, true
	default:
		return nil, false
	}
}

// requiredSigners returns the identities whose marks are mandatory for a type.
func requiredSigners(recordType RecordType) []Party {
	switch recordType {
	case TypeCapabilitiesA:
		return []Party{PartyA}
	case TypeCapabilitiesB, TypeSelection:
		return []Party{PartyB}
	case TypeConfirmation:
		return []Party{PartyA, PartyB}
	default:
		return nil
	}
}

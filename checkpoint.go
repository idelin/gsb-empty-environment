package mtf

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// Checkpoint binary format (little-endian):
//
//	offset 0   magic "MTFC"          4 bytes
//	offset 4   format version u16    2 bytes (currently 1)
//	offset 6   alphabet version u32  4 bytes
//	offset 10  state length u32      4 bytes (must equal alphabet size)
//	offset 14  MTF order payload     stateLen bytes (front first)
//	offset 14+stateLen  CRC-32/IEEE  4 bytes, over all preceding bytes
const (
	checkpointMagic       = "MTFC"
	checkpointFmtVersion  = 1
	checkpointHeaderLen   = 14
	checkpointChecksumLen = 4
)

// Checkpoint serializes the MTF state at a block boundary. The result is
// bound to the alphabet version: restoring requires an alphabet that has
// reached the same version through the same extension sequence.
func (m *Model) Checkpoint() []byte {
	order := m.Order()
	buf := make([]byte, 0, checkpointHeaderLen+len(order)+checkpointChecksumLen)
	buf = append(buf, checkpointMagic...)
	buf = binary.LittleEndian.AppendUint16(buf, checkpointFmtVersion)
	buf = binary.LittleEndian.AppendUint32(buf, m.alpha.Version())
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(order)))
	buf = append(buf, order...)
	crc := crc32.ChecksumIEEE(buf)
	return binary.LittleEndian.AppendUint32(buf, crc)
}

// Restore rebuilds a model from a checkpoint against the given alphabet.
// The magic, format version, total length, CRC checksum, alphabet version
// and state permutation are all verified; any mismatch is rejected.
func Restore(a *Alphabet, data []byte) (*Model, error) {
	if len(data) < checkpointHeaderLen+checkpointChecksumLen {
		return nil, fmt.Errorf("%w: truncated header", ErrBadCheckpoint)
	}
	if string(data[:4]) != checkpointMagic {
		return nil, fmt.Errorf("%w: bad magic", ErrBadCheckpoint)
	}
	if v := binary.LittleEndian.Uint16(data[4:6]); v != checkpointFmtVersion {
		return nil, fmt.Errorf("%w: unsupported format version %d", ErrBadCheckpoint, v)
	}
	alphaVer := binary.LittleEndian.Uint32(data[6:10])
	stateLen := binary.LittleEndian.Uint32(data[10:14])
	if uint64(len(data)) != uint64(checkpointHeaderLen)+uint64(stateLen)+checkpointChecksumLen {
		return nil, fmt.Errorf("%w: length mismatch (stateLen=%d, total=%d)", ErrBadCheckpoint, stateLen, len(data))
	}
	wantCRC := binary.LittleEndian.Uint32(data[len(data)-4:])
	if got := crc32.ChecksumIEEE(data[:len(data)-4]); got != wantCRC {
		return nil, fmt.Errorf("%w: checksum mismatch", ErrBadCheckpoint)
	}
	if alphaVer != a.Version() {
		return nil, fmt.Errorf("%w: alphabet version %d, checkpoint wants %d", ErrBadCheckpoint, a.Version(), alphaVer)
	}
	if int(stateLen) != a.Size() {
		return nil, fmt.Errorf("%w: state length %d, alphabet size %d", ErrBadCheckpoint, stateLen, a.Size())
	}
	order := data[checkpointHeaderLen : checkpointHeaderLen+stateLen]
	seen := make(map[byte]struct{}, stateLen)
	for _, s := range order {
		if !a.Contains(s) {
			return nil, fmt.Errorf("%w: symbol 0x%02x not in alphabet", ErrBadCheckpoint, s)
		}
		if _, dup := seen[s]; dup {
			return nil, fmt.Errorf("%w: symbol 0x%02x repeated in state", ErrBadCheckpoint, s)
		}
		seen[s] = struct{}{}
	}
	return newModelFromOrder(a, append([]byte(nil), order...)), nil
}

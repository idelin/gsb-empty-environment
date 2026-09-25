package rootstore

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

const (
	// SlotSize is the size of one metadata slot and the atomic publish
	// unit: a slot is always written with a single WriteAt of SlotSize
	// bytes at a SlotSize-aligned offset.
	SlotSize     = 128
	slotMagic    = 0x524f4f54534c4f54 // "ROOTSLOT"
	slotVersion  = 1
	slotCRCOff   = 40
	slotDataSize = slotCRCOff // bytes covered by the checksum
)

var crcTable = crc32.MakeTable(crc32.Castagnoli)

func checksum(b []byte) uint32 { return crc32.Checksum(b, crcTable) }

// Slot is the payload of one metadata slot: the published root pointer
// plus the monotonic sequence used to order the two slots.
type Slot struct {
	Seq    uint64
	RootID uint64
	Pages  uint64 // page count of the page file at publish time
}

func (s Slot) String() string {
	return fmt.Sprintf("seq=%d root=%d pages=%d", s.Seq, s.RootID, s.Pages)
}

// Slot layout (little-endian), zero-padded to SlotSize:
//
//	[0:8]   magic
//	[8:16]  format version
//	[16:24] sequence number
//	[24:32] root page id
//	[32:40] page count
//	[40:44] CRC32-Castagnoli over [0:40]
func encodeSlot(s Slot) []byte {
	buf := make([]byte, SlotSize)
	binary.LittleEndian.PutUint64(buf[0:], slotMagic)
	binary.LittleEndian.PutUint64(buf[8:], slotVersion)
	binary.LittleEndian.PutUint64(buf[16:], s.Seq)
	binary.LittleEndian.PutUint64(buf[24:], s.RootID)
	binary.LittleEndian.PutUint64(buf[32:], s.Pages)
	binary.LittleEndian.PutUint32(buf[slotCRCOff:], checksum(buf[:slotDataSize]))
	return buf
}

// ErrSlotInvalid marks a slot that failed structural validation. A corrupt
// slot is never guessed to be valid: any magic, version, length or
// checksum mismatch makes it unusable.
var ErrSlotInvalid = errors.New("invalid slot")

func decodeSlot(buf []byte) (Slot, error) {
	var s Slot
	if len(buf) < SlotSize {
		return s, fmt.Errorf("%w: short read (%d bytes)", ErrSlotInvalid, len(buf))
	}
	if binary.LittleEndian.Uint64(buf[0:]) != slotMagic {
		return s, fmt.Errorf("%w: bad magic", ErrSlotInvalid)
	}
	if binary.LittleEndian.Uint64(buf[8:]) != slotVersion {
		return s, fmt.Errorf("%w: unsupported version", ErrSlotInvalid)
	}
	want := binary.LittleEndian.Uint32(buf[slotCRCOff:])
	if got := checksum(buf[:slotDataSize]); want != got {
		return s, fmt.Errorf("%w: checksum mismatch", ErrSlotInvalid)
	}
	s.Seq = binary.LittleEndian.Uint64(buf[16:])
	s.RootID = binary.LittleEndian.Uint64(buf[24:])
	s.Pages = binary.LittleEndian.Uint64(buf[32:])
	return s, nil
}

// seqNewer reports whether sequence a is newer than b using RFC-1982-style
// modular comparison. The ordering is well-defined while the two sequences
// differ by less than 2^63, so wrapped sequences read from disk are still
// ordered correctly and stale (replayed) slots compare as older.
func seqNewer(a, b uint64) bool { return a != b && a-b < 1<<63 }

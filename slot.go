package slotstore

import (
	"encoding/binary"
	"hash/crc32"
)

// On-disk layout.
//
// Meta file ("superblock"):
//   0:4   magic "SPB1"
//   4:8   version (1)
//   8:12  slotCount
//   12:16 payloadCap (max payload bytes per slot)
//   16:20 freeHead (int32, -1 = empty)
//   20:28 reserved (zero)
//   28:32 crc32c over bytes [0:28]
//
// Data file ("slots.dat"): slotCount fixed-stride slots, slot i at i*stride.
// Slot layout (stride = slotOverhead + payloadCap):
//   0:4   magic "SLT1"
//   4     state (0=free, 1=occupied, 2=writing)
//   5:8   reserved (zero)
//   8:16  generation (uint64)
//   16:20 freeNext (int32, valid while free)
//   20:24 freePrev (int32, backlink for bidirectional validation)
//   24:28 payloadLen
//   28:.. payload (payloadCap bytes, zero padded)
//   ..    crc32c trailer over everything before it

const (
	slotMagic  uint32 = 0x534C5431 // "SLT1"
	superMagic uint32 = 0x53504231 // "SPB1"

	superVersion = 1
	superSize    = 32

	slotHeaderSize  = 28
	slotTrailerSize = 4
	slotOverhead    = slotHeaderSize + slotTrailerSize
)

var crcTable = crc32.MakeTable(crc32.Castagnoli)

type slotState uint8

const (
	stateFree     slotState = 0
	stateOccupied slotState = 1
	stateWriting  slotState = 2 // half-written marker; never a valid record
)

type slot struct {
	state   slotState
	gen     uint64
	next    int32
	prev    int32
	payload []byte
}

func encodeSlot(buf []byte, s slot, payloadCap uint32) {
	binary.LittleEndian.PutUint32(buf[0:4], slotMagic)
	buf[4] = byte(s.state)
	buf[5], buf[6], buf[7] = 0, 0, 0
	binary.LittleEndian.PutUint64(buf[8:16], s.gen)
	binary.LittleEndian.PutUint32(buf[16:20], uint32(s.next))
	binary.LittleEndian.PutUint32(buf[20:24], uint32(s.prev))
	binary.LittleEndian.PutUint32(buf[24:28], uint32(len(s.payload)))
	body := buf[slotHeaderSize : slotHeaderSize+payloadCap]
	for i := range body {
		body[i] = 0
	}
	copy(body, s.payload)
	crc := crc32.Checksum(buf[:slotHeaderSize+payloadCap], crcTable)
	binary.LittleEndian.PutUint32(buf[slotHeaderSize+payloadCap:], crc)
}

// decodeSlot parses a raw slot. ok=false means magic/state/length/crc did not
// validate: the slot must never be treated as a valid record.
func decodeSlot(buf []byte, payloadCap uint32) (s slot, ok bool) {
	if uint32(len(buf)) < slotOverhead+payloadCap {
		return slot{}, false
	}
	if binary.LittleEndian.Uint32(buf[0:4]) != slotMagic {
		return slot{}, false
	}
	stored := binary.LittleEndian.Uint32(buf[slotHeaderSize+payloadCap:])
	if crc32.Checksum(buf[:slotHeaderSize+payloadCap], crcTable) != stored {
		return slot{}, false
	}
	st := slotState(buf[4])
	if st != stateFree && st != stateOccupied && st != stateWriting {
		return slot{}, false
	}
	n := binary.LittleEndian.Uint32(buf[24:28])
	if n > payloadCap {
		return slot{}, false
	}
	s = slot{
		state: st,
		gen:   binary.LittleEndian.Uint64(buf[8:16]),
		next:  int32(binary.LittleEndian.Uint32(buf[16:20])),
		prev:  int32(binary.LittleEndian.Uint32(buf[20:24])),
	}
	s.payload = append([]byte(nil), buf[slotHeaderSize:slotHeaderSize+n]...)
	return s, true
}

type superblock struct {
	slotCount  uint32
	payloadCap uint32
	freeHead   int32
}

func encodeSuper(sb superblock) []byte {
	buf := make([]byte, superSize)
	binary.LittleEndian.PutUint32(buf[0:4], superMagic)
	binary.LittleEndian.PutUint32(buf[4:8], superVersion)
	binary.LittleEndian.PutUint32(buf[8:12], sb.slotCount)
	binary.LittleEndian.PutUint32(buf[12:16], sb.payloadCap)
	binary.LittleEndian.PutUint32(buf[16:20], uint32(sb.freeHead))
	crc := crc32.Checksum(buf[:28], crcTable)
	binary.LittleEndian.PutUint32(buf[28:32], crc)
	return buf
}

func decodeSuper(buf []byte) (superblock, bool) {
	if len(buf) < superSize {
		return superblock{}, false
	}
	if binary.LittleEndian.Uint32(buf[0:4]) != superMagic {
		return superblock{}, false
	}
	if binary.LittleEndian.Uint32(buf[4:8]) != superVersion {
		return superblock{}, false
	}
	stored := binary.LittleEndian.Uint32(buf[28:32])
	if crc32.Checksum(buf[:28], crcTable) != stored {
		return superblock{}, false
	}
	return superblock{
		slotCount:  binary.LittleEndian.Uint32(buf[8:12]),
		payloadCap: binary.LittleEndian.Uint32(buf[12:16]),
		freeHead:   int32(binary.LittleEndian.Uint32(buf[16:20])),
	}, true
}

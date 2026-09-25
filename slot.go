package slotstore

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// 槽布局（定长 slotSize 字节）：
//
//	[0:4]   magic "SLO1"
//	[4:8]   generation uint32（槽每承载一个新对象 +1）
//	[8]     state: 0=free, 1=used
//	[9:12]  保留，恒为 0
//	[12:16] payload 长度 uint32（used 槽有效）
//	[16:]   used 槽：payload；free 槽：前 4 字节为 nextFree 索引
//	尾部 4 字节：对前面全部字节的 CRC32 校验和（槽尾校验）
const (
	slotMagic   uint32 = 0x314F4C53 // "SLO1"
	slotHdrSize        = 16
	slotCRCSize        = 4
	nilLink     uint32 = 0xFFFFFFFF
)

type slotState byte

const (
	stateFree slotState = 0
	stateUsed slotState = 1
)

var errSlotCorrupt = errors.New("slotstore: slot tail checksum mismatch")

type slot struct {
	generation uint32
	state      slotState
	payload    []byte // used 槽
	nextFree   uint32 // free 槽
}

func encodeSlot(buf []byte, s slot) {
	for i := range buf {
		buf[i] = 0
	}
	binary.LittleEndian.PutUint32(buf[0:4], slotMagic)
	binary.LittleEndian.PutUint32(buf[4:8], s.generation)
	buf[8] = byte(s.state)
	body := buf[slotHdrSize : len(buf)-slotCRCSize]
	if s.state == stateUsed {
		binary.LittleEndian.PutUint32(buf[12:16], uint32(len(s.payload)))
		copy(body, s.payload)
	} else {
		binary.LittleEndian.PutUint32(body[0:4], s.nextFree)
	}
	crc := crc32.ChecksumIEEE(buf[:len(buf)-slotCRCSize])
	binary.LittleEndian.PutUint32(buf[len(buf)-slotCRCSize:], crc)
}

func decodeSlot(buf []byte) (slot, error) {
	var s slot
	if len(buf) < slotHdrSize+slotCRCSize {
		return s, errSlotCorrupt
	}
	if binary.LittleEndian.Uint32(buf[0:4]) != slotMagic {
		return s, errSlotCorrupt
	}
	want := binary.LittleEndian.Uint32(buf[len(buf)-slotCRCSize:])
	got := crc32.ChecksumIEEE(buf[:len(buf)-slotCRCSize])
	if want != got {
		return s, errSlotCorrupt
	}
	s.generation = binary.LittleEndian.Uint32(buf[4:8])
	s.state = slotState(buf[8])
	n := binary.LittleEndian.Uint32(buf[12:16])
	switch s.state {
	case stateFree:
		s.nextFree = binary.LittleEndian.Uint32(buf[16:20])
	case stateUsed:
		if uint64(n) > uint64(len(buf))-slotHdrSize-slotCRCSize {
			return s, errSlotCorrupt
		}
		s.payload = append([]byte(nil), buf[slotHdrSize:slotHdrSize+int(n)]...)
	default:
		return s, errSlotCorrupt
	}
	return s, nil
}

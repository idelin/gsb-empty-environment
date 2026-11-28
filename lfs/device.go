package lfs

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"hash/crc32"
)

// Device is the simulated raw block device. It holds B blocks per segment and
// two fixed checkpoint slots. Bytes stored here are the only "durable" state;
// everything else is rebuilt from them during recovery.
type Device struct {
	SegmentCount int
	BlocksPerSeg int
	// frames[seg][slot] is nil for an empty slot, otherwise a crc+gob frame.
	frames [][][]byte
	// cpSlots[0] and cpSlots[1] hold the raw alternating checkpoint bytes.
	cpSlots [2][]byte
}

// NewDevice creates a device with segCount segments of blocksPerSeg blocks.
func NewDevice(segCount, blocksPerSeg int) *Device {
	frames := make([][][]byte, segCount)
	for i := range frames {
		frames[i] = make([][]byte, blocksPerSeg)
	}
	return &Device{SegmentCount: segCount, BlocksPerSeg: blocksPerSeg, frames: frames}
}

func crc32c(b []byte) uint32 { return crc32.ChecksumIEEE(b) }

func encodeFrame(v any) ([]byte, error) {
	var payload bytes.Buffer
	if err := gob.NewEncoder(&payload).Encode(v); err != nil {
		return nil, err
	}
	frame := make([]byte, 4+payload.Len())
	binary.BigEndian.PutUint32(frame, crc32c(payload.Bytes()))
	copy(frame[4:], payload.Bytes())
	return frame, nil
}

// Node frame layout keeps the sequence number in a plaintext header so a torn
// or checksum-corrupt block can still be located precisely during recovery:
// [seq:8][crc32(header+payload):4][gob(Node payload)].
func encodeNodeFrame(n Node) ([]byte, error) {
	var payload bytes.Buffer
	if err := gob.NewEncoder(&payload).Encode(n); err != nil {
		return nil, err
	}
	frame := make([]byte, 12+payload.Len())
	binary.BigEndian.PutUint64(frame[0:8], n.Seq)
	binary.BigEndian.PutUint32(frame[8:12], crc32c(append(frame[0:8], payload.Bytes()...)))
	copy(frame[12:], payload.Bytes())
	return frame, nil
}

func decodeNodeFrame(frame []byte) (Node, error) {
	if len(frame) < 12 {
		return Node{}, ErrInvalidNode
	}
	header := frame[0:8]
	payload := frame[12:]
	want := binary.BigEndian.Uint32(frame[8:12])
	if crc32c(append(append([]byte(nil), header...), payload...)) != want {
		return Node{}, ErrInvalidNode
	}
	var n Node
	if err := gob.NewDecoder(bytes.NewReader(payload)).Decode(&n); err != nil {
		return Node{}, ErrInvalidNode
	}
	return n, nil
}

// nodeFrameSeq reads the plaintext sequence even from a corrupt frame.
func nodeFrameSeq(frame []byte) (uint64, bool) {
	if len(frame) < 12 {
		return 0, false
	}
	seq := binary.BigEndian.Uint64(frame[0:8])
	return seq, seq > 0
}

func decodeFrame(frame []byte, v any) error {
	if len(frame) < 4 {
		return ErrInvalidNode
	}
	want := binary.BigEndian.Uint32(frame[:4])
	payload := frame[4:]
	if crc32c(payload) != want {
		return ErrInvalidNode
	}
	if err := gob.NewDecoder(bytes.NewReader(payload)).Decode(v); err != nil {
		return ErrInvalidNode
	}
	return nil
}

// WriteNode durably logs one node block at the physical address.
func (d *Device) WriteNode(addr PhysAddr, n Node) error {
	if addr.Seg < 0 || addr.Seg >= d.SegmentCount || addr.Slot < 0 || addr.Slot >= d.BlocksPerSeg {
		return ErrInvalidArgument
	}
	frame, err := encodeNodeFrame(n)
	if err != nil {
		return err
	}
	d.frames[addr.Seg][addr.Slot] = frame
	return nil
}

// ReadNode decodes the node block at the address. A torn/corrupt frame yields
// ErrInvalidNode.
func (d *Device) ReadNode(addr PhysAddr) (Node, error) {
	if addr.Seg < 0 || addr.Seg >= d.SegNumber() || addr.Slot < 0 || addr.Slot >= d.BlocksPerSeg {
		return Node{}, ErrInvalidArgument
	}
	frame := d.frames[addr.Seg][addr.Slot]
	if frame == nil {
		return Node{}, ErrInvalidNode
	}
	return decodeNodeFrame(frame)
}

// SegNumber returns the number of segments on the device.
func (d *Device) SegNumber() int { return d.SegmentCount }

// BlockSize returns the fixed block count per segment.
func (d *Device) BlockSize() int { return d.BlocksPerSeg }

// BlockOccupied reports whether the slot currently holds a frame.
func (d *Device) BlockOccupied(addr PhysAddr) bool {
	return d.frames[addr.Seg][addr.Slot] != nil
}

// WipeBlock empties a physical slot, simulating a missing log record.
func (d *Device) WipeBlock(addr PhysAddr) {
	d.frames[addr.Seg][addr.Slot] = nil
}

// CorruptBlock flips bytes in the stored frame so its CRC no longer matches,
// simulating a half-written/torn node block.
func (d *Device) CorruptBlock(addr PhysAddr) {
	frame := d.frames[addr.Seg][addr.Slot]
	if frame == nil {
		return
	}
	corrupted := append([]byte(nil), frame...)
	corrupted[len(corrupted)-1] ^= 0xFF
	d.frames[addr.Seg][addr.Slot] = corrupted
}

// resetSegment empties every slot of a reclaimed segment.
func (d *Device) resetSegment(seg int) {
	for slot := range d.frames[seg] {
		d.frames[seg][slot] = nil
	}
}

// rawNode returns the underlying frame bytes (used by recovery to distinguish
// a missing slot from a corrupt one).
func (d *Device) rawNode(addr PhysAddr) []byte { return d.frames[addr.Seg][addr.Slot] }

// ---- checkpoint slots ----

type cpHead struct {
	Version uint64
	HeadCRC uint32
}

type cpTail struct {
	Version uint64
	TailCRC uint32
}

// cpSlotBytes is the on-device layout of one checkpoint slot:
// [len(headFrame)][headFrame][stateFrame][tailFrame].
type cpSlotBytes struct {
	HeadLen uint32
	Head    []byte
	State   []byte
	Tail    []byte
}

func writeCPSlot(version uint64, state any) ([]byte, error) {
	stateFrame, err := encodeFrame(state)
	if err != nil {
		return nil, err
	}
	stateCRC := crc32c(stateFrame)

	headFrame, err := encodeFrame(cpHead{Version: version, HeadCRC: stateCRC})
	if err != nil {
		return nil, err
	}
	tailFrame, err := encodeFrame(cpTail{Version: version, TailCRC: stateCRC})
	if err != nil {
		return nil, err
	}

	slot := cpSlotBytes{
		HeadLen: uint32(len(headFrame)),
		Head:    headFrame,
		State:   stateFrame,
		Tail:    tailFrame,
	}
	return encodeFrame(slot)
}

// readCPSlot validates a slot's head and tail CRCs plus the monotonic version
// and returns the encoded checkpoint state frame. Any tear/CRC fault yields
// ErrCorruptCheckpoint so the caller falls back to the other slot.
func readCPSlot(raw []byte) (version uint64, stateFrame []byte, err error) {
	if len(raw) == 0 {
		return 0, nil, ErrCorruptCheckpoint
	}
	var slot cpSlotBytes
	if decodeFrame(raw, &slot) != nil {
		return 0, nil, ErrCorruptCheckpoint
	}
	var head cpHead
	if decodeFrame(slot.Head, &head) != nil {
		return 0, nil, ErrCorruptCheckpoint
	}
	var tail cpTail
	if decodeFrame(slot.Tail, &tail) != nil {
		return 0, nil, ErrCorruptCheckpoint
	}
	if head.Version != tail.Version {
		return 0, nil, ErrCorruptCheckpoint
	}
	if head.HeadCRC != tail.TailCRC || head.HeadCRC != crc32c(slot.State) {
		return 0, nil, ErrCorruptCheckpoint
	}
	if int(slot.HeadLen) != len(slot.Head) {
		return 0, nil, ErrCorruptCheckpoint
	}
	return head.Version, slot.State, nil
}

func (d *Device) writeCPSlot(slot int, raw []byte) { d.cpSlots[slot] = raw }

func (d *Device) readCPRaw(slot int) []byte { return d.cpSlots[slot] }

// CorruptCPSlot damages a checkpoint slot to simulate a half-written CP.
func (d *Device) CorruptCPSlot(slot int) error {
	if slot < 0 || slot > 1 {
		return ErrInvalidArgument
	}
	raw := d.cpSlots[slot]
	if raw == nil {
		return ErrCorruptCheckpoint
	}
	bad := append([]byte(nil), raw...)
	bad[len(bad)/2] ^= 0xFF
	d.cpSlots[slot] = bad
	return nil
}

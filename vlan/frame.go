package vlan

import "errors"

// EtherType values relevant to 802.1Q VLAN tagging.
const (
	EtherTypeVLAN uint16 = 0x8100 // C-TAG (also used for priority tags)
	EtherTypeQinQ uint16 = 0x88A8 // S-TAG (service tag)
)

// Bounds on the label stack. The switch only ever handles zero, one (priority
// or VLAN) or two (service + customer) tags; a third tag means the frame does
// not belong to any tenant we manage and must be dropped, not guessed at.
const (
	MaxTags     = 2
	minFrameLen = 14 // dst(6) + src(6) + ethertype(2)
	priorityVID = 0  // VID 0 marks a priority-only tag
)

var (
	// ErrTruncated is returned when the frame does not hold the bytes its
	// headers claim, or is shorter than an Ethernet header.
	ErrTruncated = errors.New("vlan: truncated ethernet frame")
	// ErrTagStackTooDeep is returned when a frame already carries
	// MaxTags and another 802.1Q header is present.
	ErrTagStackTooDeep = errors.New("vlan: label stack exceeds two tags")
)

// StackClass describes how many VLAN tags a frame carries on the wire.
type StackClass int

const (
	StackUntagged StackClass = iota
	StackPriority            // single tag with VID 0
	StackSingle              // single tag with VID 1..4095
	StackDouble              // two tags (S-TAG/outer + C-TAG/inner)
)

func (c StackClass) String() string {
	switch c {
	case StackUntagged:
		return "untagged"
	case StackPriority:
		return "priority"
	case StackSingle:
		return "single"
	case StackDouble:
		return "double"
	default:
		return "unknown"
	}
}

// Tag is one 802.1Q TPI/TCI header.
type Tag struct {
	TPID uint16 // 0x8100 or 0x88A8
	PCP  uint8  // priority code point, 0..7
	DEI  bool   // drop eligible indicator
	VID  uint16 // 0..4095
}

// Frame is a parsed, bounded view of an Ethernet frame. Body still points at
// the backing array of the input frame; callers must not mutate it while the
// Frame is in use.
type Frame struct {
	Dst  [6]byte
	Src  [6]byte
	Tags []Tag // len 0..MaxTags
	Body []byte
}

// Class reports the wire shape of the tag stack.
func (f *Frame) Class() StackClass {
	switch len(f.Tags) {
	case 0:
		return StackUntagged
	case 1:
		if f.Tags[0].VID == priorityVID {
			return StackPriority
		}
		return StackSingle
	default:
		return StackDouble
	}
}

// ParseFrame performs a byte-level parse of an Ethernet frame, accepting at
// most MaxTags VLAN headers. It never allocates a copy of the payload.
func ParseFrame(b []byte) (*Frame, error) {
	if len(b) < minFrameLen {
		return nil, ErrTruncated
	}
	f := &Frame{}
	copy(f.Dst[:], b[0:6])
	copy(f.Src[:], b[6:12])

	off := 12
	for {
		if len(b)-off < 2 {
			return nil, ErrTruncated
		}
		tpid := beUint16(b[off : off+2])
		if tpid != EtherTypeVLAN && tpid != EtherTypeQinQ {
			break
		}
		if len(b) < off+4 {
			return nil, ErrTruncated
		}
		if len(f.Tags) == MaxTags {
			return nil, ErrTagStackTooDeep
		}
		tci := beUint16(b[off+2 : off+4])
		f.Tags = append(f.Tags, Tag{
			TPID: tpid,
			PCP:  uint8(tci >> 13),
			DEI:  tci&0x1000 != 0,
			VID:  tci & 0x0FFF,
		})
		off += 4
		// The next iteration needs two bytes to read a TPID/ethertype.
		if len(b)-off < 2 {
			return nil, ErrTruncated
		}
	}
	f.Body = b[off:]
	return f, nil
}

// Marshal rebuilds the frame from destination, source, tags and body. The
// output is a fresh allocation, so concurrent policy swaps never observe a
// half-rewritten frame. dst/src and PCP are preserved across rewrites.
func (f *Frame) Marshal() []byte {
	out := make([]byte, 12+4*len(f.Tags)+len(f.Body))
	copy(out[0:6], f.Dst[:])
	copy(out[6:12], f.Src[:])
	off := 12
	for _, t := range f.Tags {
		putBE16(out[off:off+2], t.TPID)
		tci := t.VID & 0x0FFF
		tci |= uint16(t.PCP&0x07) << 13
		if t.DEI {
			tci |= 0x1000
		}
		putBE16(out[off+2:off+4], tci)
		off += 4
	}
	copy(out[off:], f.Body)
	return out
}

// insertTag adds one outermost 802.1Q tag into a freshly allocated buffer.
func insertTag(b []byte, t Tag) []byte {
	if len(b) < minFrameLen {
		return b
	}
	out := make([]byte, len(b)+4)
	copy(out[:12], b[:12])
	putBE16(out[12:14], t.TPID)
	tci := t.VID & 0x0FFF
	tci |= uint16(t.PCP&0x07) << 13
	if t.DEI {
		tci |= 0x1000
	}
	putBE16(out[14:16], tci)
	copy(out[16:], b[12:])
	return out
}

func beUint16(b []byte) uint16 { return uint16(b[0])<<8 | uint16(b[1]) }

func putBE16(b []byte, v uint16) {
	b[0] = byte(v >> 8)
	b[1] = byte(v)
}

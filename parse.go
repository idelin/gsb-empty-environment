package fmp4mux

import (
	"encoding/binary"
	"errors"
)

// Box is one parsed ISO BMFF box.
type Box struct {
	Type       string // 4CC, e.g. "moof"
	Version    uint8  // full-box version; 0 for plain boxes
	Flags      uint32 // full-box flags; 0 for plain boxes
	Payload    []byte // full-box payload without version/flags, or plain payload
	Children   []*Box
	HeaderSize int // bytes consumed by box header (8 or 12 for full boxes)
	Size       int // total on-wire box size including all descendants
}

// fullBoxes is the set of box types whose payload starts with version/flags.
var fullBoxes = map[string]bool{
	"mvhd": true, "tkhd": true, "mdhd": true, "hdlr": true, "vmhd": true,
	"smhd": true, "nmhd": true, "sthd": true, "dref": true, "stsd": true,
	"stts": true, "ctts": true, "stsc": true, "stsz": true, "stz2": true,
	"stco": true, "co64": true, "stss": true, "stsh": true, "padb": true,
	"stdp": true, "sdtp": true, "sbgp": true, "sgpd": true, "subs": true,
	"trex": true, "trep": true, "mfhd": true, "tfhd": true, "tfdt": true,
	"trun": true, "tfra": true, "mfro": true, "sidx": true, "ssix": true,
	"elst": true, "url ": true, "urn ": true, "meta": true, "pdin": true,
	"mehd": true, "leva": true, "tfxd": true, "tfrf": true, "saiz": true,
	"saio": true, "prft": true, "chnl": true, "stvi": true, "mhap": true,
}

// containerBoxes only contain child boxes (no leaf payload).
var containerBoxes = map[string]bool{
	"moov": true, "trak": true, "mdia": true, "minf": true, "dinf": true,
	"stbl": true, "mvex": true, "moof": true, "traf": true, "mfra": true,
	"edts": true, "udta": true, "meco": true, "mere": true,
}

// ErrTruncatedBox is returned when a box header or payload is incomplete.
var ErrTruncatedBox = errors.New("fmp4mux: truncated box")

// ParseBoxes parses all top-level boxes in data, descending into known
// container boxes. Sample entries inside stsd are parsed so their child
// boxes (e.g. avcC) are reachable.
func ParseBoxes(data []byte) ([]*Box, error) {
	return parseBoxList(data, 0)
}

func parseBoxList(data []byte, depth int) ([]*Box, error) {
	var boxes []*Box
	off := 0
	for off < len(data) {
		box, n, err := parseOneBox(data[off:], depth)
		if err != nil {
			return nil, err
		}
		boxes = append(boxes, box)
		off += n
	}
	return boxes, nil
}

func parseOneBox(data []byte, depth int) (*Box, int, error) {
	if len(data) < 8 {
		return nil, 0, ErrTruncatedBox
	}
	size := int(binary.BigEndian.Uint32(data[:4]))
	typ := string(data[4:8])
	header := 8
	if size == 1 {
		if len(data) < 16 {
			return nil, 0, ErrTruncatedBox
		}
		large := binary.BigEndian.Uint64(data[8:16])
		size = int(large)
		header = 16
	} else if size == 0 {
		size = len(data)
	}
	if size < header || size > len(data) {
		return nil, 0, ErrTruncatedBox
	}

	box := &Box{Type: typ}
	payload := data[header:size]

	if fullBoxes[typ] {
		if len(payload) < 4 {
			return nil, 0, ErrTruncatedBox
		}
		box.Version = payload[0]
		// version occupies the high byte; flags are the low 24 bits.
		box.Flags = uint32(payload[1])<<16 | uint32(payload[2])<<8 | uint32(payload[3])
		box.HeaderSize = header + 4
		box.Payload = payload[4:]
	} else {
		box.HeaderSize = header
		box.Payload = payload
	}

	switch {
	case containerBoxes[typ]:
		children, err := parseBoxList(box.Payload, depth+1)
		if err != nil {
			return nil, 0, err
		}
		box.Children = children
		box.Payload = nil
	case typ == "stsd":
		children, err := parseStsd(box.Payload)
		if err != nil {
			return nil, 0, err
		}
		box.Children = children
		box.Payload = nil
	}

	box.Size = size
	return box, size, nil
}

// parseStsd parses the sample entry table. Each sample entry has fixed fields
// followed by child boxes (e.g. avcC).
func parseStsd(payload []byte) ([]*Box, error) {
	if len(payload) < 4 {
		return nil, ErrTruncatedBox
	}
	count := int(binary.BigEndian.Uint32(payload[:4]))
	rest := payload[4:]
	var entries []*Box
	for i := 0; i < count; i++ {
		if len(rest) < 8 {
			return nil, ErrTruncatedBox
		}
		size := int(binary.BigEndian.Uint32(rest[:4]))
		if size < 8 || size > len(rest) {
			return nil, ErrTruncatedBox
		}
		entry := &Box{
			Type:       string(rest[4:8]),
			HeaderSize: 8,
			Payload:    rest[8:size],
			Size:       size,
		}
		children, fixed, err := parseSampleEntryChildren(entry.Type, entry.Payload)
		if err != nil {
			return nil, err
		}
		entry.Children = children
		entry.Payload = entry.Payload[:fixed]
		entries = append(entries, entry)
		rest = rest[size:]
	}
	return entries, nil
}

// visualSampleEntries have 78 bytes of fixed fields after the box header
// (ISO/IEC 14496-12 8.5.2.2); their remaining payload holds child boxes.
var visualSampleEntries = map[string]bool{
	"avc1": true, "avc2": true, "avc3": true, "avc4": true,
	"hvc1": true, "hev1": true, "mp4v": true, "encv": true,
}

func parseSampleEntryChildren(entryType string, payload []byte) ([]*Box, int, error) {
	fixed := 0
	if visualSampleEntries[entryType] {
		fixed = 78
	}
	if len(payload) < fixed {
		return nil, 0, ErrTruncatedBox
	}
	if len(payload) == fixed {
		return nil, fixed, nil
	}
	children, err := parseBoxList(payload[fixed:], 0)
	return children, fixed, err
}

// FindBox returns the first box (depth-first, pre-order) matching typ.
func FindBox(boxes []*Box, typ string) *Box {
	for _, b := range boxes {
		if b.Type == typ {
			return b
		}
		if found := FindBox(b.Children, typ); found != nil {
			return found
		}
	}
	return nil
}

// FindAllBoxes returns every box (pre-order) matching typ.
func FindAllBoxes(boxes []*Box, typ string) []*Box {
	var out []*Box
	for _, b := range boxes {
		if b.Type == typ {
			out = append(out, b)
		}
		out = append(out, FindAllBoxes(b.Children, typ)...)
	}
	return out
}

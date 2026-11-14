package netsec

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Tag identifies a DICOM data element.
type Tag struct {
	Group   uint16
	Element uint16
}

// NewTag constructs a DICOM tag.
func NewTag(group, element uint16) Tag {
	return Tag{Group: group, Element: element}
}

func (t Tag) key() uint32 {
	return uint32(t.Group)<<16 | uint32(t.Element)
}

// String returns the conventional "(gggg,eeee)" representation.
func (t Tag) String() string {
	return fmt.Sprintf("(%04X,%04X)", t.Group, t.Element)
}

func (t Tag) isPrivate() bool {
	return t.Group&1 == 1
}

func (t Tag) isPrivateCreator() bool {
	return t.isPrivate() && t.Element >= 0x0010 && t.Element <= 0x00FF
}

func (t Tag) privateCreatorForData() Tag {
	return Tag{Group: t.Group, Element: uint16(t.Element >> 8)}
}

func (t Tag) isOverlayOrCurveGroup() bool {
	return t.Group&0xFF00 == 0x6000 || t.Group&0xFF00 == 0x5000
}

type vr string

const (
	vrAE vr = "AE"
	vrAS vr = "AS"
	vrAT vr = "AT"
	vrCS vr = "CS"
	vrDA vr = "DA"
	vrDS vr = "DS"
	vrDT vr = "DT"
	vrFD vr = "FD"
	vrFL vr = "FL"
	vrIS vr = "IS"
	vrLO vr = "LO"
	vrLT vr = "LT"
	vrOB vr = "OB"
	vrOD vr = "OD"
	vrOF vr = "OF"
	vrOL vr = "OL"
	vrOV vr = "OV"
	vrOW vr = "OW"
	vrPN vr = "PN"
	vrSH vr = "SH"
	vrSL vr = "SL"
	vrSQ vr = "SQ"
	vrSS vr = "SS"
	vrST vr = "ST"
	vrSV vr = "SV"
	vrTM vr = "TM"
	vrUC vr = "UC"
	vrUI vr = "UI"
	vrUL vr = "UL"
	vrUN vr = "UN"
	vrUR vr = "UR"
	vrUS vr = "US"
	vrUT vr = "UT"
	vrUV vr = "UV"
)

var longValueVRs = map[vr]bool{
	vrOB: true,
	vrOD: true,
	vrOF: true,
	vrOL: true,
	vrOV: true,
	vrOW: true,
	vrSQ: true,
	vrUC: true,
	vrUN: true,
	vrUR: true,
	vrUT: true,
}

var explicitVRs = map[string]vr{
	"AE": vrAE, "AS": vrAS, "AT": vrAT, "CS": vrCS, "DA": vrDA,
	"DS": vrDS, "DT": vrDT, "FD": vrFD, "FL": vrFL, "IS": vrIS,
	"LO": vrLO, "LT": vrLT, "OB": vrOB, "OD": vrOD, "OF": vrOF,
	"OL": vrOL, "OV": vrOV, "OW": vrOW, "PN": vrPN, "SH": vrSH,
	"SL": vrSL, "SQ": vrSQ, "SS": vrSS, "ST": vrST, "SV": vrSV,
	"TM": vrTM, "UC": vrUC, "UI": vrUI, "UL": vrUL, "UN": vrUN,
	"UR": vrUR, "US": vrUS, "UT": vrUT, "UV": vrUV,
}

type element struct {
	tag          Tag
	vr           vr
	value        []byte
	items        []*dataset
	encapsulated bool
}

func (e *element) isSequence() bool {
	return e.vr == vrSQ || e.items != nil
}

type dataset struct {
	elements []*element
}

func (d *dataset) find(tag Tag) *element {
	if d == nil {
		return nil
	}
	for _, e := range d.elements {
		if e.tag == tag {
			return e
		}
	}
	return nil
}

func (d *dataset) has(tag Tag) bool {
	return d.find(tag) != nil
}

type fileEncoding struct {
	explicit bool
	little   bool
	deflated bool
}

type dicomFile struct {
	part10       bool
	preamble     [128]byte
	zeroPreamble bool
	meta         *dataset
	data         *dataset
	enc          fileEncoding
}

var (
	tagItem             = Tag{0xFFFE, 0xE000}
	tagItemDelimitation = Tag{0xFFFE, 0xE00D}
	tagSequenceDelim    = Tag{0xFFFE, 0xE0DD}
	tagPixelData        = Tag{0x7FE0, 0x0010}
	tagTransferSyntax   = Tag{0x0002, 0x0010}
)

var errInvalidDICOM = errors.New("invalid DICOM data")

type reader struct {
	data     []byte
	pos      int
	explicit bool
	little   bool
	depth    int
}

func (r *reader) remaining() int {
	return len(r.data) - r.pos
}

func (r *reader) readUint16() (uint16, error) {
	if r.remaining() < 2 {
		return 0, errInvalidDICOM
	}
	var v uint16
	if r.little {
		v = binary.LittleEndian.Uint16(r.data[r.pos:])
	} else {
		v = binary.BigEndian.Uint16(r.data[r.pos:])
	}
	r.pos += 2
	return v, nil
}

func (r *reader) readUint32() (uint32, error) {
	if r.remaining() < 4 {
		return 0, errInvalidDICOM
	}
	var v uint32
	if r.little {
		v = binary.LittleEndian.Uint32(r.data[r.pos:])
	} else {
		v = binary.BigEndian.Uint32(r.data[r.pos:])
	}
	r.pos += 4
	return v, nil
}

func (r *reader) peekTag() (Tag, error) {
	if r.remaining() < 4 {
		return Tag{}, errInvalidDICOM
	}
	group, err := r.peekUint16()
	if err != nil {
		return Tag{}, err
	}
	element, err := r.peekUint16At(2)
	if err != nil {
		return Tag{}, err
	}
	return Tag{Group: group, Element: element}, nil
}

func (r *reader) peekUint16() (uint16, error) {
	return r.peekUint16At(0)
}

func (r *reader) peekUint16At(offset int) (uint16, error) {
	if r.remaining() < offset+2 {
		return 0, errInvalidDICOM
	}
	if r.little {
		return binary.LittleEndian.Uint16(r.data[r.pos+offset:]), nil
	}
	return binary.BigEndian.Uint16(r.data[r.pos+offset:]), nil
}

func (r *reader) readTag() (Tag, error) {
	group, err := r.readUint16()
	if err != nil {
		return Tag{}, err
	}
	element, err := r.readUint16()
	if err != nil {
		return Tag{}, err
	}
	return Tag{Group: group, Element: element}, nil
}

func (r *reader) readElement() (*element, error) {
	tag, err := r.readTag()
	if err != nil {
		return nil, err
	}
	if tag.Group == 0xFFFE {
		return nil, fmt.Errorf("%w: unexpected delimiter %s", errInvalidDICOM, tag)
	}

	var valueVR vr
	var valueLength uint32
	if r.explicit {
		if r.remaining() < 4 {
			return nil, errInvalidDICOM
		}
		vrBytes := string(r.data[r.pos : r.pos+2])
		r.pos += 2
		parsedVR, ok := explicitVRs[vrBytes]
		if !ok {
			return nil, fmt.Errorf("%w: unknown explicit VR %q at %s", errInvalidDICOM, vrBytes, tag)
		}
		valueVR = parsedVR
		if longValueVRs[valueVR] {
			if r.remaining() < 6 || r.data[r.pos] != 0 || r.data[r.pos+1] != 0 {
				return nil, fmt.Errorf("%w: bad reserved bytes for %s", errInvalidDICOM, tag)
			}
			r.pos += 2
			valueLength, err = r.readUint32()
			if err != nil {
				return nil, err
			}
		} else {
			var length16 uint16
			length16, err = r.readUint16()
			if err != nil {
				return nil, err
			}
			valueLength = uint32(length16)
		}
	} else {
		valueVR = vrForTag(tag)
		valueLength, err = r.readUint32()
		if err != nil {
			return nil, err
		}
	}

	if valueLength == 0xFFFFFFFF {
		if tag == tagPixelData {
			return r.readEncapsulatedPixelData(tag, valueVR)
		}
		if valueVR != vrSQ && valueVR != vrUN {
			nextTag, peekErr := r.peekTag()
			if peekErr != nil || nextTag != tagItem {
				return nil, fmt.Errorf("%w: undefined length for non-sequence %s", errInvalidDICOM, tag)
			}
		}
		items, err := r.readUndefinedSequence()
		if err != nil {
			return nil, err
		}
		return &element{tag: tag, vr: vrSQ, items: items}, nil
	}

	valueEnd := r.pos + int(valueLength)
	if valueEnd < r.pos || valueEnd > len(r.data) {
		return nil, fmt.Errorf("%w: value of %s exceeds input", errInvalidDICOM, tag)
	}

	if tag != tagPixelData && valueLength >= 8 && (valueVR == vrSQ || valueVR == vrUN) {
		nextTag, peekErr := r.peekTag()
		if peekErr == nil && nextTag == tagItem {
			items, err := r.readDefinedSequence(valueEnd)
			if err != nil {
				return nil, err
			}
			return &element{tag: tag, vr: vrSQ, items: items}, nil
		}
	}

	if valueVR == vrSQ {
		items, err := r.readDefinedSequence(valueEnd)
		if err != nil {
			return nil, err
		}
		return &element{tag: tag, vr: vrSQ, items: items}, nil
	}

	value := r.data[r.pos:valueEnd]
	r.pos = valueEnd
	return &element{tag: tag, vr: valueVR, value: value}, nil
}

func (r *reader) readUndefinedSequence() ([]*dataset, error) {
	if r.depth >= maxSequenceDepth {
		return nil, fmt.Errorf("%w: sequence nesting exceeds %d", errInvalidDICOM, maxSequenceDepth)
	}
	r.depth++
	defer func() { r.depth-- }()

	var items []*dataset
	for {
		tag, err := r.readTag()
		if err != nil {
			return nil, err
		}
		if tag == tagSequenceDelim {
			if _, err := r.readUint32(); err != nil {
				return nil, err
			}
			return items, nil
		}
		if tag != tagItem {
			return nil, fmt.Errorf("%w: expected sequence item, got %s", errInvalidDICOM, tag)
		}
		itemLength, err := r.readUint32()
		if err != nil {
			return nil, err
		}
		item, err := r.readSequenceItem(itemLength)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
}

func (r *reader) readDefinedSequence(end int) ([]*dataset, error) {
	if r.depth >= maxSequenceDepth {
		return nil, fmt.Errorf("%w: sequence nesting exceeds %d", errInvalidDICOM, maxSequenceDepth)
	}
	r.depth++
	defer func() { r.depth-- }()

	var items []*dataset
	for r.pos < end {
		tag, err := r.readTag()
		if err != nil {
			return nil, err
		}
		if tag != tagItem {
			return nil, fmt.Errorf("%w: expected sequence item at offset %d, got %s", errInvalidDICOM, r.pos-4, tag)
		}
		itemLength, err := r.readUint32()
		if err != nil {
			return nil, err
		}
		item, err := r.readSequenceItem(itemLength)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if r.pos != end {
		return nil, fmt.Errorf("%w: sequence length mismatch", errInvalidDICOM)
	}
	return items, nil
}

func (r *reader) readSequenceItem(length uint32) (*dataset, error) {
	if length == 0xFFFFFFFF {
		item, err := r.readDataset(len(r.data), true)
		if err != nil {
			return nil, err
		}
		tag, err := r.readTag()
		if err != nil {
			return nil, err
		}
		if tag != tagItemDelimitation {
			return nil, fmt.Errorf("%w: expected item delimiter, got %s", errInvalidDICOM, tag)
		}
		if _, err := r.readUint32(); err != nil {
			return nil, err
		}
		return item, nil
	}

	end := r.pos + int(length)
	if end < r.pos || end > len(r.data) {
		return nil, fmt.Errorf("%w: item length exceeds input", errInvalidDICOM)
	}
	item, err := r.readDataset(end, false)
	if err != nil {
		return nil, err
	}
	if r.pos != end {
		return nil, fmt.Errorf("%w: item length mismatch", errInvalidDICOM)
	}
	return item, nil
}

func (r *reader) readDataset(end int, stopAtDelimiter bool) (*dataset, error) {
	ds := &dataset{}
	for r.pos < end {
		if r.remaining() < 8 {
			if stopAtDelimiter {
				tag, err := r.peekTag()
				if err == nil && (tag == tagItemDelimitation || tag == tagSequenceDelim) {
					return ds, nil
				}
			}
			return nil, fmt.Errorf("%w: truncated element header", errInvalidDICOM)
		}
		nextTag, err := r.peekTag()
		if err != nil {
			return nil, err
		}
		if nextTag == tagItemDelimitation || nextTag == tagSequenceDelim {
			if stopAtDelimiter {
				return ds, nil
			}
			return nil, fmt.Errorf("%w: unexpected delimiter in dataset", errInvalidDICOM)
		}
		e, err := r.readElement()
		if err != nil {
			return nil, err
		}
		ds.elements = append(ds.elements, e)
	}
	return ds, nil
}

func (r *reader) readEncapsulatedPixelData(tag Tag, valueVR vr) (*element, error) {
	start := r.pos
	for {
		itemTag, err := r.readTag()
		if err != nil {
			return nil, err
		}
		if itemTag == tagSequenceDelim {
			if _, err := r.readUint32(); err != nil {
				return nil, err
			}
			break
		}
		if itemTag != tagItem {
			return nil, fmt.Errorf("%w: bad encapsulated pixel item", errInvalidDICOM)
		}
		itemLength, err := r.readUint32()
		if err != nil {
			return nil, err
		}
		if itemLength == 0xFFFFFFFF {
			return nil, fmt.Errorf("%w: undefined encapsulated pixel item", errInvalidDICOM)
		}
		if int(itemLength) > r.remaining() {
			return nil, fmt.Errorf("%w: encapsulated pixel item exceeds input", errInvalidDICOM)
		}
		r.pos += int(itemLength)
	}
	return &element{
		tag:          tag,
		vr:           valueVR,
		value:        r.data[start:r.pos],
		encapsulated: true,
	}, nil
}

func parseDICOM(data []byte) (*dicomFile, error) {
	if len(data) >= 132 && string(data[128:132]) == "DICM" {
		metaReader := &reader{data: data, pos: 132, explicit: true, little: true}
		meta := &dataset{}
		for metaReader.pos < len(data) {
			nextTag, err := metaReader.peekTag()
			if err != nil {
				return nil, err
			}
			if nextTag.Group != 0x0002 {
				break
			}
			e, err := metaReader.readElement()
			if err != nil {
				return nil, err
			}
			meta.elements = append(meta.elements, e)
		}

		enc := encodingFromMeta(meta)
		rest := data[metaReader.pos:]
		if enc.deflated {
			zr, err := zlib.NewReader(bytes.NewReader(rest))
			if err != nil {
				return nil, fmt.Errorf("%w: cannot inflate dataset: %v", errInvalidDICOM, err)
			}
			inflated, readErr := io.ReadAll(zr)
			closeErr := zr.Close()
			if readErr != nil {
				return nil, fmt.Errorf("%w: cannot inflate dataset: %v", errInvalidDICOM, readErr)
			}
			if closeErr != nil {
				return nil, fmt.Errorf("%w: cannot close inflater: %v", errInvalidDICOM, closeErr)
			}
			rest = inflated
		}

		dsReader := &reader{data: rest, explicit: enc.explicit, little: enc.little}
		ds, err := dsReader.readDataset(len(rest), false)
		if err != nil {
			return nil, err
		}
		if err := allowTrailingPadding(rest, dsReader.pos); err != nil {
			return nil, err
		}
		f := &dicomFile{part10: true, meta: meta, data: ds, enc: enc}
		copy(f.preamble[:], data[:128])
		return f, nil
	}

	enc, err := detectRawEncoding(data)
	if err != nil {
		return nil, err
	}
	dsReader := &reader{data: data, explicit: enc.explicit, little: enc.little}
	ds, err := dsReader.readDataset(len(data), false)
	if err != nil {
		return nil, err
	}
	if err := allowTrailingPadding(data, dsReader.pos); err != nil {
		return nil, err
	}
	return &dicomFile{data: ds, enc: enc}, nil
}

func allowTrailingPadding(data []byte, pos int) error {
	for _, b := range data[pos:] {
		if b != 0x00 && b != 0x20 {
			return fmt.Errorf("%w: unexpected trailing data", errInvalidDICOM)
		}
	}
	return nil
}

func detectRawEncoding(data []byte) (fileEncoding, error) {
	if len(data) < 8 {
		return fileEncoding{}, fmt.Errorf("%w: dataset too short", errInvalidDICOM)
	}
	candidate := string(data[4:6])
	if _, ok := explicitVRs[candidate]; ok {
		if longValueVRs[explicitVRs[candidate]] {
			if len(data) >= 12 && data[6] == 0 && data[7] == 0 {
				return fileEncoding{explicit: true, little: true}, nil
			}
		} else {
			return fileEncoding{explicit: true, little: true}, nil
		}
	}
	return fileEncoding{explicit: false, little: true}, nil
}

func encodingFromMeta(meta *dataset) fileEncoding {
	e := meta.find(tagTransferSyntax)
	if e == nil {
		return fileEncoding{explicit: true, little: true}
	}
	ts := strings.TrimRight(string(e.value), "\x00 ")
	switch ts {
	case "1.2.840.10008.1.2":
		return fileEncoding{explicit: false, little: true}
	case "1.2.840.10008.1.2.1":
		return fileEncoding{explicit: true, little: true}
	case "1.2.840.10008.1.2.1.99":
		return fileEncoding{explicit: true, little: true, deflated: true}
	case "1.2.840.10008.1.2.2":
		return fileEncoding{explicit: true, little: false}
	default:
		return fileEncoding{explicit: true, little: true}
	}
}

func encodeDICOM(f *dicomFile) ([]byte, error) {
	enc := f.enc
	dataBytes, err := encodeDataset(f.data, enc.explicit, enc.little)
	if err != nil {
		return nil, err
	}
	if enc.deflated {
		var compressed bytes.Buffer
		zw := zlib.NewWriter(&compressed)
		if _, err := zw.Write(dataBytes); err != nil {
			return nil, err
		}
		if err := zw.Close(); err != nil {
			return nil, err
		}
		dataBytes = compressed.Bytes()
	}

	if !f.part10 {
		return dataBytes, nil
	}

	metaBytes, err := encodeMeta(f.meta)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 132, 132+len(metaBytes)+len(dataBytes))
	if !f.zeroPreamble {
		copy(out[:128], f.preamble[:])
	}
	copy(out[128:], "DICM")
	out = append(out, metaBytes...)
	out = append(out, dataBytes...)
	return out, nil
}

func encodeMeta(meta *dataset) ([]byte, error) {
	var body []byte
	for _, e := range meta.elements {
		if e.tag == (Tag{0x0002, 0x0000}) {
			continue
		}
		encoded, err := encodeElement(e, true, true)
		if err != nil {
			return nil, err
		}
		body = append(body, encoded...)
	}

	groupLength := &element{
		tag:   Tag{0x0002, 0x0000},
		vr:    vrUL,
		value: make([]byte, 4),
	}
	if len(body) > int(^uint32(0)) {
		return nil, fmt.Errorf("file meta information too large")
	}
	binary.LittleEndian.PutUint32(groupLength.value, uint32(len(body)))
	head, err := encodeElement(groupLength, true, true)
	if err != nil {
		return nil, err
	}
	return append(head, body...), nil
}

func encodeDataset(ds *dataset, explicit, little bool) ([]byte, error) {
	var out []byte
	for _, e := range ds.elements {
		encoded, err := encodeElement(e, explicit, little)
		if err != nil {
			return nil, err
		}
		out = append(out, encoded...)
	}
	return out, nil
}

func encodeElement(e *element, explicit, little bool) ([]byte, error) {
	if e == nil {
		return nil, nil
	}
	valueVR := e.vr
	if valueVR == "" {
		valueVR = vrUN
	}

	var body []byte
	if e.encapsulated {
		body = e.value
	} else if e.isSequence() {
		var err error
		body, err = encodeSequence(e.items, explicit, little)
		if err != nil {
			return nil, err
		}
	} else {
		body = e.value
		body = padValue(body, valueVR)
	}

	out := make([]byte, 0, 12+len(body))
	out = appendUint16(out, e.tag.Group, little)
	out = appendUint16(out, e.tag.Element, little)

	length := uint32(len(body))
	if e.encapsulated {
		length = 0xFFFFFFFF
	} else if uint64(len(body)) > uint64(^uint32(0)) {
		return nil, fmt.Errorf("value for %s is too large", e.tag)
	}
	if explicit {
		out = append(out, byte(valueVR[0]), byte(valueVR[1]))
		if longValueVRs[valueVR] {
			out = append(out, 0, 0)
			out = appendUint32(out, length, little)
		} else {
			if length > 0xFFFF {
				return nil, fmt.Errorf("value for %s exceeds 16-bit VR length", e.tag)
			}
			out = appendUint16(out, uint16(length), little)
		}
	} else {
		out = appendUint32(out, length, little)
	}
	out = append(out, body...)
	return out, nil
}

func encodeSequence(items []*dataset, explicit, little bool) ([]byte, error) {
	var out []byte
	for _, item := range items {
		body, err := encodeDataset(item, explicit, little)
		if err != nil {
			return nil, err
		}
		if len(body) > int(^uint32(0)) {
			return nil, fmt.Errorf("sequence item too large")
		}
		out = appendUint16(out, tagItem.Group, little)
		out = appendUint16(out, tagItem.Element, little)
		out = appendUint32(out, uint32(len(body)), little)
		out = append(out, body...)
	}
	return out, nil
}

func padValue(value []byte, valueVR vr) []byte {
	if len(value)%2 == 0 {
		return value
	}
	pad := byte(0x20)
	switch valueVR {
	case vrUI, vrOB, vrOD, vrOF, vrOL, vrOV, vrOW, vrUN:
		pad = 0x00
	}
	out := make([]byte, len(value)+1)
	copy(out, value)
	out[len(value)] = pad
	return out
}

func appendUint16(dst []byte, v uint16, little bool) []byte {
	var buf [2]byte
	if little {
		binary.LittleEndian.PutUint16(buf[:], v)
	} else {
		binary.BigEndian.PutUint16(buf[:], v)
	}
	return append(dst, buf[:]...)
}

func appendUint32(dst []byte, v uint32, little bool) []byte {
	var buf [4]byte
	if little {
		binary.LittleEndian.PutUint32(buf[:], v)
	} else {
		binary.BigEndian.PutUint32(buf[:], v)
	}
	return append(dst, buf[:]...)
}

func trimText(value []byte) string {
	return strings.TrimRight(string(value), "\x00 ")
}

func trimUID(value []byte) string {
	return strings.Trim(string(value), "\x00 ")
}

func textValue(value string) []byte {
	return []byte(value)
}

const maxSequenceDepth = 64

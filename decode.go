package sintcol

import (
	"encoding/binary"
	"hash"
	"hash/crc32"
	"io"
	"math"
)

type blockHeader struct {
	ordinal int
	first   uint64
	count   uint64
	payload uint64
	base    int64
	init    int64
	enc     Encoding
	chained bool
}

type chainState struct {
	value     int64
	diff      int64
	hasValue  bool
	hasDiff   bool
	goodValue bool
	goodDiff  bool
}

type frameDecoder struct {
	cfg config

	buf    []byte
	offset int64

	phase int

	frameStart int64
	rawHeader  []byte
	header     blockHeader
	headerErr  error
	frameReady bool
	payloadLen uint64
	payloadGot uint64
	payload    []byte
	discard    bool
	hash       hash.Hash32

	ordinal       int
	expectedFirst uint64
	firstSeen     bool
	chain         chainState
	entries       []indexEntry
}

const (
	phaseMagic = iota
	phaseBlock
	phaseIndex
	phaseDone
)

func newFrameDecoder(cfg config) *frameDecoder {
	return &frameDecoder{cfg: cfg}
}

func (d *frameDecoder) Feed(data []byte) ([]Event, error) {
	d.buf = append(d.buf, data...)
	var events []Event
	for len(d.buf) > 0 {
		switch d.phase {
		case phaseMagic:
			need := len(magic) - int(d.offset)
			if len(d.buf) < need {
				if string(d.buf) != magic[d.offset:] {
					return events, ErrCorrupt
				}
				d.offset += int64(len(d.buf))
				d.buf = d.buf[:0]
				return events, nil
			}
			if string(d.buf[:need]) != magic[d.offset:] {
				return events, ErrCorrupt
			}
			d.buf = d.buf[need:]
			d.offset = 0
			d.phase = phaseBlock
		case phaseBlock:
			event, err := d.processBlock()
			if err == ErrTruncated {
				return events, nil
			}
			if err != nil {
				return events, err
			}
			if event != nil {
				events = append(events, *event)
			}
		case phaseIndex:
			done, err := d.consumeIndex()
			if err != nil {
				return events, err
			}
			if done || len(d.buf) == 0 {
				return events, nil
			}
		default:
			return events, ErrCorrupt
		}
	}
	return events, nil
}

func (d *frameDecoder) Finish() ([]Event, error) {
	switch d.phase {
	case phaseDone:
		return nil, io.EOF
	case phaseMagic, phaseBlock:
		return nil, ErrTruncated
	default:
		if len(d.buf) == 0 {
			return nil, ErrTruncated
		}
		return nil, ErrCorrupt
	}
}

func (d *frameDecoder) processBlock() (*Event, error) {
	if d.buf[0] == indexMarker {
		d.phase = phaseIndex
		return nil, nil
	}
	if !d.frameReady {
		if d.buf[0] != blockMarker {
			return nil, ErrCorrupt
		}
		if err := d.startFrame(); err != nil {
			return nil, err
		}
	}
	return d.consumePayload()
}

func (d *frameDecoder) startFrame() error {
	d.frameStart = d.offset
	d.frameReady = false
	d.headerErr = nil
	d.discard = false
	d.payloadGot = 0
	d.payload = nil
	d.rawHeader = nil

	if len(d.buf) < 2 {
		return ErrTruncated
	}
	headerLen, lenSize, err := strictUvarint(d.buf[2:])
	if err == ErrCorrupt {
		return ErrCorrupt
	}
	if err != nil {
		return ErrTruncated
	}
	minLen := uint64(7 + lenSize)
	if headerLen < minLen || headerLen > maxHeaderBytes {
		return ErrCorrupt
	}
	if uint64(len(d.buf)) < headerLen {
		return ErrTruncated
	}

	raw := make([]byte, headerLen)
	copy(raw, d.buf[:headerLen])
	d.rawHeader = raw

	header := blockHeader{}
	header.chained = raw[1]&flagChain != 0
	if raw[1]&^flagMask != 0 {
		return ErrCorrupt
	}
	header.enc = Encoding((raw[1] >> 1) & 0x03)
	if header.enc > EncDeltaDelta {
		d.headerErr = ErrCorrupt
	}

	pos := 2 + lenSize
	readField := func(dst *uint64, recoverable bool) bool {
		v, n, fieldErr := strictUvarint(raw[pos:])
		if fieldErr != nil {
			if recoverable {
				d.headerErr = ErrCorrupt
				return false
			}
			return false
		}
		pos += n
		*dst = v
		return true
	}
	var ordinal uint64
	if !readField(&ordinal, false) {
		return ErrCorrupt
	}
	header.ordinal = int(ordinal)
	if !readField(&header.first, false) {
		return ErrCorrupt
	}
	if !readField(&header.count, false) {
		return ErrCorrupt
	}
	if !readField(&header.payload, false) {
		return ErrCorrupt
	}

	if header.count == 0 || header.count > uint64(d.cfg.maxBlockElements) {
		d.headerErr = ErrCorrupt
	}
	if header.payload > uint64(d.cfg.maxBlockPayload) || header.payload > math.MaxInt {
		d.headerErr = ErrCorrupt
		d.discard = true
	}
	nextFirst, overflow := saturatingAdd(header.first, header.count)
	if overflow || nextFirst > uint64(d.cfg.maxTotalElements) {
		d.headerErr = ErrCorrupt
		d.discard = true
	}

	if base, n, baseErr := strictZigzag(raw[pos:]); baseErr != nil {
		d.headerErr = ErrCorrupt
	} else {
		pos += n
		header.base = base
	}
	if !header.chained && header.enc == EncDeltaDelta && header.count >= 2 {
		if init, n, initErr := strictZigzag(raw[pos:]); initErr != nil {
			d.headerErr = ErrCorrupt
		} else {
			pos += n
			header.init = init
		}
	}
	if pos != int(headerLen) {
		d.headerErr = ErrCorrupt
	}
	if header.ordinal != d.ordinal {
		d.headerErr = ErrCorrupt
	}
	if header.first != d.expectedFirst || (!d.firstSeen && header.first != 0) {
		d.headerErr = ErrCorrupt
	}

	d.header = header
	d.payloadLen = header.payload
	d.hash = crc32.NewIEEE()
	d.hash.Write(raw)
	if !d.discard && header.payload <= math.MaxInt {
		d.payload = make([]byte, 0, int(header.payload))
	}
	d.frameReady = true
	return nil
}

func (d *frameDecoder) consumePayload() (*Event, error) {
	if d.payloadGot < d.payloadLen {
		remaining := d.payloadLen - d.payloadGot
		if uint64(len(d.buf)) < remaining {
			d.hash.Write(d.buf)
			if !d.discard {
				d.payload = append(d.payload, d.buf...)
			}
			consumed := uint64(len(d.buf))
			d.payloadGot += consumed
			d.buf = d.buf[:0]
			d.offset += int64(consumed)
			return nil, ErrTruncated
		}
		piece := d.buf[:remaining]
		d.hash.Write(piece)
		if !d.discard {
			d.payload = append(d.payload, piece...)
		}
		d.payloadGot = d.payloadLen
	}

	if uint64(len(d.buf)) < d.payloadLen+4 {
		return nil, ErrTruncated
	}
	payloadEnd := d.payloadLen
	received := binary.LittleEndian.Uint32(d.buf[payloadEnd : payloadEnd+4])
	if received != d.hash.Sum32() {
		d.headerErr = ErrCorrupt
	}

	frameSize := uint64(len(d.rawHeader)) + payloadEnd + 4
	if uint64(len(d.buf)) < frameSize {
		return nil, ErrCorrupt
	}
	event := d.finalizeFrame()
	d.buf = d.buf[frameSize:]
	d.offset += int64(frameSize)
	return event, nil
}

func (d *frameDecoder) finalizeFrame() *Event {
	header := d.header
	ordinal := d.ordinal
	first := header.first
	count := int(header.count)
	d.ordinal++
	d.firstSeen = true
	d.expectedFirst, _ = saturatingAdd(header.first, header.count)

	makeError := func(err error) *Event {
		if !header.chained {
			d.chain = chainState{}
		}
		return &Event{Err: &BlockError{
			Offset:       d.frameStart,
			Ordinal:      ordinal,
			FirstElement: int64FromUint(first),
			ElementCount: count,
			Err:          err,
		}}
	}
	if d.headerErr != nil {
		return makeError(d.headerErr)
	}

	var values []int64
	var next chainState
	var err error
	if header.chained {
		values, next, err = decodeChained(header, d.payload, d.chain)
	} else {
		values, next, err = decodeIndependent(header, d.payload)
	}
	if err != nil {
		return makeError(err)
	}

	d.chain = next
	return &Event{Block: &Block{
		Ordinal:      ordinal,
		FirstElement: int64FromUint(first),
		ElementCount: count,
		Offset:       d.frameStart,
		Encoding:     header.enc,
		Chained:      header.chained,
		Values:       values,
	}}
}

func saturatingAdd(a, b uint64) (uint64, bool) {
	if a > math.MaxUint64-b {
		return math.MaxUint64, true
	}
	return a + b, false
}

func int64FromUint(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}

func strictZigzag(buf []byte) (int64, int, error) {
	v, n, err := strictUvarint(buf)
	if err != nil {
		return 0, 0, err
	}
	return zigzagDecode(v), n, nil
}

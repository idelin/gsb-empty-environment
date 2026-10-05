package arith

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// Format limits. Frames violating them are rejected with ErrLimit.
const (
	MaxBits     = 1 << 24 // max events per frame (16 Mi)
	MaxContexts = 1 << 16 // max distinct context labels
	MaxLabelLen = 64      // max label length in bytes
)

// maxPayloadBytes bounds the payload for n bits: each coded bit emits at
// most ~14 normalization bits (min probability 1/4096), well under 16
// bits, plus termination and padding.
func maxPayloadBytes(n uint64) uint64 { return 2*n + 64 }

var magic = []byte("ACX1")
var stateMagic = []byte("ACS1")

// Encoder incrementally codes context-labeled bits and assembles a frame.
type Encoder struct {
	rc     rangeEncoder
	labels []string
	index  map[string]uint32
	ctxs   []context
	seq    []uint32 // context index per event
	bits   []byte   // bit per event
	done   bool
	frame  []byte
}

func NewEncoder() *Encoder {
	return &Encoder{
		rc:    newRangeEncoder(),
		index: make(map[string]uint32),
	}
}

func validLabel(label string) bool { return len(label) >= 1 && len(label) <= MaxLabelLen }

// EncodeBit codes one bit under the given context label.
func (e *Encoder) EncodeBit(label string, bit byte) error {
	if e.done {
		return fmt.Errorf("%w: encoder already finished", ErrState)
	}
	if bit > 1 {
		return ErrBit
	}
	if !validLabel(label) {
		return fmt.Errorf("%w: label length %d not in [1,%d]", ErrContext, len(label), MaxLabelLen)
	}
	if uint64(len(e.bits)) >= MaxBits {
		return fmt.Errorf("%w: bit count exceeds %d", ErrLimit, MaxBits)
	}
	idx, ok := e.index[label]
	if !ok {
		if len(e.labels) >= MaxContexts {
			return fmt.Errorf("%w: more than %d contexts", ErrLimit, MaxContexts)
		}
		idx = uint32(len(e.labels))
		e.index[label] = idx
		e.labels = append(e.labels, label)
		e.ctxs = append(e.ctxs, context{1, 1})
	}
	ctx := &e.ctxs[idx]
	e.rc.encodeBit(ctx, bit)
	ctx.update(bit)
	e.seq = append(e.seq, idx)
	e.bits = append(e.bits, bit)
	return nil
}

// Encode codes a slice of bits (each 0 or 1) under one label.
func (e *Encoder) Encode(label string, bits []byte) error {
	for _, b := range bits {
		if err := e.EncodeBit(label, b); err != nil {
			return err
		}
	}
	return nil
}

// Finish terminates the stream and returns the complete frame.
func (e *Encoder) Finish() []byte {
	if e.done {
		return e.frame
	}
	e.done = true
	e.rc.finish()
	payload := e.rc.out

	var buf []byte
	buf = append(buf, magic...)
	buf = binary.AppendUvarint(buf, uint64(len(e.bits)))
	buf = binary.AppendUvarint(buf, uint64(len(e.labels)))
	for _, l := range e.labels {
		buf = binary.AppendUvarint(buf, uint64(len(l)))
		buf = append(buf, l...)
	}
	// Label sequence rule: with a single context every index is 0, so the
	// sequence is implicit and occupies zero bytes; otherwise each event
	// stores its context index as one uvarint.
	if len(e.labels) > 1 {
		for _, s := range e.seq {
			buf = binary.AppendUvarint(buf, uint64(s))
		}
	}
	buf = binary.AppendUvarint(buf, uint64(len(payload)))
	buf = append(buf, payload...)
	var crc [4]byte
	binary.BigEndian.PutUint32(crc[:], crc32.ChecksumIEEE(packBits(e.bits)))
	buf = append(buf, crc[:]...)
	e.frame = buf
	return buf
}

// EncodeFrame is the one-shot equivalent of NewEncoder/Encode/Finish.
func EncodeFrame(labels []string, bits []byte) ([]byte, error) {
	if len(labels) != len(bits) {
		return nil, fmt.Errorf("%w: %d labels for %d bits", ErrState, len(labels), len(bits))
	}
	e := NewEncoder()
	for i, b := range bits {
		if err := e.EncodeBit(labels[i], b); err != nil {
			return nil, err
		}
	}
	return e.Finish(), nil
}

// packBits packs 0/1 bytes MSB-first, zero-padding the last byte.
func packBits(bits []byte) []byte {
	out := make([]byte, (len(bits)+7)/8)
	for i, b := range bits {
		if b != 0 {
			out[i>>3] |= 1 << (7 - uint(i)&7)
		}
	}
	return out
}

type cursor struct {
	data []byte
	pos  int
}

func (c *cursor) uvarint() (uint64, error) {
	v, n := binary.Uvarint(c.data[c.pos:])
	if n == 0 {
		return 0, ErrTruncated
	}
	if n < 0 {
		return 0, fmt.Errorf("%w: varint overflow", ErrLimit)
	}
	c.pos += n
	return v, nil
}

func (c *cursor) bytes(n uint64) ([]byte, error) {
	if n > uint64(len(c.data)-c.pos) {
		return nil, ErrTruncated
	}
	b := c.data[c.pos : c.pos+int(n)]
	c.pos += int(n)
	return b, nil
}

// DecodeFrame parses and decodes a frame, returning the context label per
// event and the decoded bits. It rejects bad magic, illegal contexts,
// over-limit lengths, truncation, trailing garbage and checksum mismatches.
func DecodeFrame(frame []byte) (labels []string, bits []byte, err error) {
	c := &cursor{data: frame}
	m, err := c.bytes(4)
	if err != nil {
		return nil, nil, err
	}
	if string(m) != string(magic) {
		return nil, nil, ErrBadMagic
	}
	n, err := c.uvarint()
	if err != nil {
		return nil, nil, err
	}
	if n > MaxBits {
		return nil, nil, fmt.Errorf("%w: bit count %d exceeds %d", ErrLimit, n, MaxBits)
	}
	k, err := c.uvarint()
	if err != nil {
		return nil, nil, err
	}
	if k > MaxContexts {
		return nil, nil, fmt.Errorf("%w: context count %d exceeds %d", ErrLimit, k, MaxContexts)
	}
	if (n == 0) != (k == 0) {
		return nil, nil, fmt.Errorf("%w: %d bits but %d contexts", ErrContext, n, k)
	}
	ctxLabels := make([]string, 0, k)
	seen := make(map[string]struct{}, k)
	for i := uint64(0); i < k; i++ {
		l, err := c.uvarint()
		if err != nil {
			return nil, nil, err
		}
		if l == 0 || l > MaxLabelLen {
			return nil, nil, fmt.Errorf("%w: label length %d", ErrContext, l)
		}
		b, err := c.bytes(l)
		if err != nil {
			return nil, nil, err
		}
		s := string(b)
		if _, dup := seen[s]; dup {
			return nil, nil, fmt.Errorf("%w: duplicate label %q", ErrContext, s)
		}
		seen[s] = struct{}{}
		ctxLabels = append(ctxLabels, s)
	}
	seq := make([]uint32, n)
	if k == 1 {
		// Implicit all-zero label sequence; nothing stored.
	} else {
		for i := range seq {
			v, err := c.uvarint()
			if err != nil {
				return nil, nil, err
			}
			if v >= k {
				return nil, nil, fmt.Errorf("%w: context index %d >= %d", ErrContext, v, k)
			}
			seq[i] = uint32(v)
		}
	}
	p, err := c.uvarint()
	if err != nil {
		return nil, nil, err
	}
	if p > maxPayloadBytes(n) {
		return nil, nil, fmt.Errorf("%w: payload %d bytes for %d bits", ErrLimit, p, n)
	}
	payload, err := c.bytes(p)
	if err != nil {
		return nil, nil, err
	}
	crcBytes, err := c.bytes(4)
	if err != nil {
		return nil, nil, err
	}
	if c.pos != len(c.data) {
		return nil, nil, fmt.Errorf("%w: %d extra bytes", ErrTrailing, len(c.data)-c.pos)
	}

	ctxs := make([]context, k)
	for i := range ctxs {
		ctxs[i] = context{1, 1}
	}
	d := newRangeDecoder(payload)
	bits = make([]byte, n)
	labels = make([]string, n)
	for i := range bits {
		ctx := &ctxs[seq[i]]
		b := d.decodeBit(ctx)
		ctx.update(b)
		bits[i] = b
		labels[i] = ctxLabels[seq[i]]
	}
	if crc32.ChecksumIEEE(packBits(bits)) != binary.BigEndian.Uint32(crcBytes) {
		return nil, nil, ErrChecksum
	}
	return labels, bits, nil
}

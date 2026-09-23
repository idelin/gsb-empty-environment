package secded

import (
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

// readChunkSize is the batch size used when streaming input.
const readChunkSize = 64 * 1024

// ErrDataCorrupt is returned (together with the best-effort payload and a
// full report) when the payload CRC fails or blocks remain uncorrected.
var ErrDataCorrupt = errors.New("secded: payload failed verification")

// ErrOutputTooLarge is returned when the declared payload length exceeds
// the caller's decode bound; nothing is decoded in that case.
var ErrOutputTooLarge = errors.New("secded: payload length exceeds decode bound")

// Encoder streams payload through SECDED blocks into the interleaved format.
type Encoder struct {
	Params Params // code parameters (k, r, n)
	Depth  int    // interleave depth D
}

// NewEncoder builds an encoder for k data bits per block and depth D.
func NewEncoder(k, depth int) (Encoder, error) {
	p, err := NewParams(k)
	if err != nil {
		return Encoder{}, err
	}
	if depth < 1 || depth > 255 {
		return Encoder{}, fmt.Errorf("secded: depth %d out of range [1,255]", depth)
	}
	return Encoder{Params: p, Depth: depth}, nil
}

// EncodeReport summarizes an Encode run.
type EncodeReport struct {
	Header    Header
	SourceMap SourceMap
}

// Encode reads r in 64 KiB batches, writes the framed stream to w, and
// returns the header and the bit-source mapping (also persistable via
// json.Marshal). Payload bits are consumed MSB-first; the final block is
// zero-padded, with the true length recorded in the header.
func (e Encoder) Encode(w io.Writer, r io.Reader) (EncodeReport, error) {
	var rep EncodeReport
	crc := crc32.NewIEEE()
	var payload []byte
	buf := make([]byte, readChunkSize)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			payload = append(payload, buf[:n]...)
			crc.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return rep, err
		}
	}
	k := uint64(e.Params.DataBits)
	blockCount := uint32((uint64(len(payload))*8 + k - 1) / k)
	h := Header{
		Version:    FormatVersion,
		DataBits:   uint8(e.Params.DataBits),
		Depth:      uint8(e.Depth),
		BlockCount: blockCount,
		PayloadLen: uint64(len(payload)),
		PayloadCRC: crc.Sum32(),
	}
	if _, err := w.Write(h.marshal()); err != nil {
		return rep, err
	}
	bodyBits, err := h.BodyBits()
	if err != nil {
		return rep, err
	}
	body := make([]byte, (bodyBits+7)/8)
	n := e.Params.TotalBits
	d := e.Depth
	// Batch per frame: encode D blocks, interleave their bits.
	for blk := uint32(0); blk < blockCount; blk++ {
		data := extractBits(payload, uint64(blk)*k, k)
		cw := e.Params.Encode(data)
		frame := int(blk) / d
		slot := int(blk) % d
		base := frame * n * d
		for pos := 0; pos < n; pos++ {
			if (cw>>uint(pos))&1 == 1 {
				setBodyBit(body, uint64(base+pos*d+slot))
			}
		}
	}
	if _, err := w.Write(body); err != nil {
		return rep, err
	}
	sm, err := h.SourceMap()
	if err != nil {
		return rep, err
	}
	rep.Header = h
	rep.SourceMap = sm
	return rep, nil
}

// BlockReport is the decode outcome of one block, kept for audit.
type BlockReport struct {
	Index   uint32 `json:"index"`
	Frame   uint64 `json:"frame"`
	Status  string `json:"status"`
	Flipped []int  `json:"flipped,omitempty"` // codeword positions flipped during correction
}

// DecodeReport is the full decode audit, including uncorrected blocks.
type DecodeReport struct {
	Header        Header        `json:"header"`
	PayloadCRCOK  bool          `json:"payloadCRCOK"`
	Blocks        []BlockReport `json:"blocks"`
	Uncorrectable []uint32      `json:"uncorrectable"`
}

// Decode reads one framed stream from r. Erasure markers (body-relative
// bit indices, may be nil) flag unreliable bits. Output is bounded:
// if the declared payload length exceeds maxBytes, ErrOutputTooLarge is
// returned before any decoding. On success the payload, its provenance
// map and the per-block report are returned; if verification fails the
// best-effort payload is still returned together with ErrDataCorrupt —
// the caller must treat the data as untrusted.
func Decode(r io.Reader, erasures *BitSet, maxBytes uint64) ([]byte, SourceMap, DecodeReport, error) {
	var rep DecodeReport
	hbuf := make([]byte, HeaderSize)
	if _, err := io.ReadFull(r, hbuf); err != nil {
		return nil, SourceMap{}, rep, fmt.Errorf("secded: reading header: %w", err)
	}
	h, err := UnmarshalHeader(hbuf)
	if err != nil {
		return nil, SourceMap{}, rep, err
	}
	rep.Header = h
	if h.PayloadLen > maxBytes {
		return nil, SourceMap{}, rep, fmt.Errorf("%w: %d > %d", ErrOutputTooLarge, h.PayloadLen, maxBytes)
	}
	p, _ := h.Params()
	bodyBits, err := h.BodyBits()
	if err != nil {
		return nil, SourceMap{}, rep, err
	}
	body := make([]byte, (bodyBits+7)/8)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, SourceMap{}, rep, fmt.Errorf("secded: truncated body: %w", err)
	}
	n := p.TotalBits
	d := int(h.Depth)
	erased := make([]bool, n)
	var out []byte
	var acc uint64
	var accBits uint
	k := uint(p.DataBits)
	flush := func(bit uint) {
		acc = (acc << 1) | uint64(bit)
		accBits++
		if accBits == 8 {
			out = append(out, byte(acc))
			acc, accBits = 0, 0
		}
	}
	for blk := uint32(0); blk < h.BlockCount; blk++ {
		frame := uint64(blk) / uint64(d)
		slot := int(blk) % d
		base := frame * uint64(n*d)
		var cw uint64
		hasErasure := false
		for pos := 0; pos < n; pos++ {
			off := base + uint64(pos*d+slot)
			if getBodyBit(body, off) == 1 {
				cw |= 1 << uint(pos)
			}
			erased[pos] = erasures != nil && erasures.Has(off)
			hasErasure = hasErasure || erased[pos]
		}
		var data uint64
		var st Status
		var fixed uint64
		if hasErasure {
			data, st, fixed = p.DecodeWithErasures(cw, erased)
		} else {
			data, st, fixed = p.Decode(cw)
		}
		br := BlockReport{Index: blk, Frame: frame, Status: st.String()}
		diff := cw ^ fixed
		for pos := 0; pos < n; pos++ {
			if (diff>>uint(pos))&1 == 1 {
				br.Flipped = append(br.Flipped, pos)
			}
		}
		if st == StatusDoubleError || st == StatusUncorrectable {
			rep.Uncorrectable = append(rep.Uncorrectable, blk)
		}
		rep.Blocks = append(rep.Blocks, br)
		for i := k; i > 0; i-- {
			flush(uint((data >> (i - 1)) & 1))
		}
	}
	if uint64(len(out)) > h.PayloadLen {
		out = out[:h.PayloadLen]
	}
	rep.PayloadCRCOK = crc32.ChecksumIEEE(out) == h.PayloadCRC
	sm, err := h.SourceMap()
	if err != nil {
		return nil, SourceMap{}, rep, err
	}
	if !rep.PayloadCRCOK || len(rep.Uncorrectable) > 0 {
		return out, sm, rep, ErrDataCorrupt
	}
	return out, sm, rep, nil
}

// extractBits reads n bits from buf starting at bit offset off (MSB-first),
// zero-padding past the end.
func extractBits(buf []byte, off, n uint64) uint64 {
	var v uint64
	for i := uint64(0); i < n; i++ {
		bit := off + i
		b := uint64(0)
		if bit/8 < uint64(len(buf)) {
			b = uint64((buf[bit/8] >> (7 - bit%8)) & 1)
		}
		v = (v << 1) | b
	}
	return v
}

func setBodyBit(buf []byte, i uint64) { buf[i/8] |= 1 << (7 - i%8) }

func getBodyBit(buf []byte, i uint64) int { return int((buf[i/8] >> (7 - i%8)) & 1) }

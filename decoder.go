package streamcol

import (
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
)

// DecoderConfig sets decoding limits. All limits are enforced before any
// allocation, so a hostile or corrupt header cannot trigger a huge
// allocation. Zero values pick the defaults.
type DecoderConfig struct {
	// MaxBlockElements caps elements per block (default 1<<24).
	MaxBlockElements uint64
	// MaxPayloadBytes caps the payload size of one block (default 1<<31).
	MaxPayloadBytes uint64
	// MaxTotalElements caps the decoded element total (default 1<<40).
	MaxTotalElements uint64
}

func (c *DecoderConfig) withDefaults() DecoderConfig {
	out := *c
	if out.MaxBlockElements == 0 {
		out.MaxBlockElements = 1 << 24
	}
	if out.MaxPayloadBytes == 0 {
		out.MaxPayloadBytes = 1 << 31
	}
	if out.MaxTotalElements == 0 {
		out.MaxTotalElements = 1 << 40
	}
	return out
}

// Checkpoint captures the decoder state at a block boundary so decoding can
// resume later, possibly in another process, by feeding the stream again
// starting at Offset.
type Checkpoint struct {
	Offset     int64  // absolute stream offset of the next unparsed byte
	LastValue  int64  // last decoded value (chain anchor)
	ChainValid bool   // whether LastValue is trustworthy
	NextIndex  uint64 // stream-wide index of the next element
}

// Decoder incrementally parses a stream from arbitrarily chunked input.
// Feed bytes with Feed, then pull decoded blocks with NextBlock until it
// returns ErrNeedMoreData.
type Decoder struct {
	cfg        DecoderConfig
	buf        []byte
	consumed   int
	streamOff  int64 // absolute offset of buf[0]
	lastValue  int64
	chainValid bool
	total      uint64
	index      Index
}

// NewDecoder returns a decoder with the given limits.
func NewDecoder(cfg *DecoderConfig) *Decoder {
	c := DecoderConfig{}
	if cfg != nil {
		c = *cfg
	}
	return &Decoder{cfg: c.withDefaults()}
}

// ResumeDecoder returns a decoder positioned at a previous checkpoint.
// The caller must feed the stream again starting at cp.Offset.
func ResumeDecoder(cp Checkpoint, cfg *DecoderConfig) *Decoder {
	d := NewDecoder(cfg)
	d.streamOff = cp.Offset
	d.lastValue = cp.LastValue
	d.chainValid = cp.ChainValid
	d.total = cp.NextIndex
	return d
}

// Feed appends the next chunk of input. Chunks may split the stream at any
// byte boundary.
func (d *Decoder) Feed(p []byte) { d.buf = append(d.buf, p...) }

// Buffered reports how many fed bytes are still unparsed.
func (d *Decoder) Buffered() int { return len(d.buf) - d.consumed }

// Offset returns the absolute stream offset of the next unparsed byte.
func (d *Decoder) Offset() int64 { return d.streamOff + int64(d.consumed) }

// Index returns the random-access index built from the blocks decoded so far.
func (d *Decoder) Index() Index { return append(Index(nil), d.index...) }

// Checkpoint snapshots the decoder state for later resumption.
func (d *Decoder) Checkpoint() Checkpoint {
	return Checkpoint{
		Offset:     d.Offset(),
		LastValue:  d.lastValue,
		ChainValid: d.chainValid,
		NextIndex:  d.total,
	}
}

// NextBlock decodes the next complete block and returns its values.
//
// Return contract:
//   - (values, nil): one block decoded.
//   - (nil, ErrNeedMoreData): the buffered bytes are a prefix of a valid
//     stream; feed more data. Never returned for known-bad bytes.
//   - (nil, *BlockError): a block was rejected and skipped; the decoder is
//     ready for the next call. Dependent blocks report ErrBrokenChain until
//     an independent block re-anchors the chain.
//   - (nil, ctx.Err()): the context was cancelled.
func (d *Decoder) NextBlock(ctx context.Context) ([]int64, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		avail := d.buf[d.consumed:]
		if len(avail) == 0 {
			d.compact()
			return nil, ErrNeedMoreData
		}
		h, err := parseHeader(avail)
		if err != nil {
			if errors.Is(err, ErrNeedMoreData) {
				return nil, ErrNeedMoreData
			}
			off := d.Offset()
			d.resync()
			d.chainValid = false
			return nil, &BlockError{Offset: off, Err: err}
		}
		// Enforce every length quota before allocating or waiting for the
		// payload: a corrupt header must not cause a huge allocation or an
		// endless wait for bytes that will never arrive.
		if lerr := d.checkLimits(h); lerr != nil {
			off := d.Offset()
			d.resync()
			d.chainValid = false
			return nil, &BlockError{Offset: off, Err: lerr}
		}
		total := uint64(h.headerLen) + h.payloadLen + crcLen
		if uint64(len(avail)) < total {
			return nil, ErrNeedMoreData
		}
		block := avail[:total]
		payload := block[h.headerLen : len(block)-crcLen]
		want := binary.LittleEndian.Uint32(block[len(block)-crcLen:])
		if crc32.Checksum(block[2:len(block)-crcLen], castagnoli) != want {
			return nil, d.failBlock(h, total, ErrChecksum)
		}
		var baseline int64
		if h.independent {
			baseline = h.baseline
		} else {
			if !d.chainValid {
				return nil, d.failBlock(h, total, ErrBrokenChain)
			}
			b, aerr := checkedAdd(d.lastValue, h.baseline)
			if aerr != nil {
				return nil, d.failBlock(h, total, aerr)
			}
			baseline = b
		}
		vals, derr := decodePayload(h.enc, baseline, h.count, payload)
		if derr != nil {
			return nil, d.failBlock(h, total, derr)
		}
		last := baseline
		if len(vals) > 0 {
			last = vals[len(vals)-1]
		}
		d.index = append(d.index, IndexEntry{
			Offset:      d.Offset(),
			FirstIndex:  d.total,
			Count:       h.count,
			Baseline:    baseline,
			LastValue:   last,
			Independent: h.independent,
			Enc:         h.enc,
		})
		d.lastValue = last
		d.chainValid = true
		d.total += h.count
		d.advance(total)
		return vals, nil
	}
}

// checkLimits rejects oversized or inconsistent declared lengths before any
// allocation happens.
func (d *Decoder) checkLimits(h blockHeader) error {
	switch {
	case h.count > d.cfg.MaxBlockElements:
		return ErrLengthLimit
	case h.payloadLen > d.cfg.MaxPayloadBytes:
		return ErrLengthLimit
	case h.count > h.payloadLen:
		// Each element needs at least one payload byte.
		return ErrCorruptHeader
	case h.count > d.cfg.MaxTotalElements-d.total:
		return ErrLengthLimit
	}
	return nil
}

// failBlock skips a structurally complete but rejected block and breaks the
// value chain. The element count is still credited so later index entries
// keep their true stream positions.
func (d *Decoder) failBlock(h blockHeader, total uint64, err error) error {
	off := d.Offset()
	d.advance(total)
	d.chainValid = false
	d.total += h.count
	return &BlockError{Offset: off, Err: err}
}

// resync recovers from an unparseable header by scanning for the next block
// magic. Bytes that cannot start a block are dropped; a trailing lone magic
// byte is kept because it may begin a split magic pair.
func (d *Decoder) resync() {
	rest := d.buf[d.consumed+1:]
	for i := 0; i+1 < len(rest); i++ {
		if rest[i] == magic0 && rest[i+1] == magic1 {
			d.consumed += 1 + i
			d.compact()
			return
		}
	}
	keep := 0
	if len(rest) > 0 && rest[len(rest)-1] == magic0 {
		keep = 1
	}
	d.consumed = len(d.buf) - keep
	d.compact()
}

func (d *Decoder) advance(n uint64) {
	d.consumed += int(n)
	d.compact()
}

func (d *Decoder) compact() {
	if d.consumed == 0 {
		return
	}
	switch {
	case d.consumed == len(d.buf):
		d.buf = d.buf[:0]
	case d.consumed >= 4096:
		d.buf = append(d.buf[:0], d.buf[d.consumed:]...)
	default:
		return
	}
	d.streamOff += int64(d.consumed)
	d.consumed = 0
}

// DecodeAll feeds the whole buffer and drains the decoder, returning the
// decoded values, the random-access index, and every error encountered
// (corrupt blocks do not stop the decode). A truncated tail is reported as
// io.ErrUnexpectedEOF, distinct from corruption.
func DecodeAll(ctx context.Context, data []byte, cfg *DecoderConfig) ([]int64, Index, []error) {
	d := NewDecoder(cfg)
	d.Feed(data)
	return Drain(ctx, d)
}

// Drain pulls blocks until the decoder runs out of input.
func Drain(ctx context.Context, d *Decoder) ([]int64, Index, []error) {
	var vals []int64
	var errs []error
	for {
		v, err := d.NextBlock(ctx)
		if err == nil {
			vals = append(vals, v...)
			continue
		}
		var be *BlockError
		if errors.As(err, &be) {
			errs = append(errs, be)
			continue
		}
		if errors.Is(err, ErrNeedMoreData) {
			if d.Buffered() > 0 {
				errs = append(errs, io.ErrUnexpectedEOF)
			}
			break
		}
		errs = append(errs, err)
		break
	}
	return vals, d.Index(), errs
}

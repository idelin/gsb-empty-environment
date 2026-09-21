package streamcol

import (
	"fmt"
	"io"
)

// EncoderConfig tunes the encoder. Zero values pick the defaults.
type EncoderConfig struct {
	// BlockSize is the number of elements per block (default 128).
	BlockSize int
	// Encoding forces one encoding, or EncAuto (default) to pick the
	// smallest payload per block.
	Encoding Encoding
	// IndependentEvery makes every Nth block carry an absolute baseline so
	// decoding can resume after corruption (default 8; <=0 means only the
	// first block is independent).
	IndependentEvery int
}

func (c *EncoderConfig) withDefaults() EncoderConfig {
	out := *c
	if out.BlockSize <= 0 {
		out.BlockSize = 128
	}
	if out.IndependentEvery == 0 {
		out.IndependentEvery = 8
	}
	return out
}

// Encoder streams int64 values into blocks on an io.Writer.
type Encoder struct {
	w       io.Writer
	cfg     EncoderConfig
	pending []int64
	last    int64
	hasLast bool
	blocks  uint64
	total   uint64
	off     int64
	index   Index
	err     error
}

// NewEncoder returns an encoder writing to w.
func NewEncoder(w io.Writer, cfg *EncoderConfig) *Encoder {
	c := EncoderConfig{Encoding: EncAuto}
	if cfg != nil {
		c = *cfg
	}
	return &Encoder{w: w, cfg: c.withDefaults()}
}

// Add appends one value, flushing a block whenever the pending buffer fills.
func (e *Encoder) Add(v int64) error {
	if e.err != nil {
		return e.err
	}
	e.pending = append(e.pending, v)
	if len(e.pending) >= e.cfg.BlockSize {
		return e.flush()
	}
	return nil
}

// Close flushes any pending values as a final block.
func (e *Encoder) Close() error {
	if e.err != nil {
		return e.err
	}
	return e.flush()
}

// Index returns the random-access index of the blocks written so far.
func (e *Encoder) Index() Index { return append(Index(nil), e.index...) }

// Offset returns the number of bytes written so far.
func (e *Encoder) Offset() int64 { return e.off }

func (e *Encoder) flush() error {
	if len(e.pending) == 0 {
		return nil
	}
	anchor := int64(0)
	independent := true
	if e.hasLast {
		anchor = e.last
		independent = e.cfg.IndependentEvery > 0 && e.blocks%uint64(e.cfg.IndependentEvery) == 0
	}
	baselineStored := anchor // absolute when independent
	if !independent {
		// The anchor is the previous block's last value, so the stored
		// delta is zero; the decoder recomputes baseline = prevLast + 0.
		baselineStored = 0
	}

	candidates := []Encoding{e.cfg.Encoding}
	if e.cfg.Encoding == EncAuto {
		candidates = []Encoding{EncRaw, EncDelta, EncDeltaOfDelta}
	}
	bestEnc := EncRaw
	var best []byte
	for _, enc := range candidates {
		p, err := encodePayload(enc, anchor, e.pending)
		if err != nil {
			continue // delta not representable as int64; try another encoding
		}
		if best == nil || len(p) < len(best) {
			best, bestEnc = p, enc
		}
	}
	if best == nil {
		e.err = fmt.Errorf("%w: use EncAuto or EncRaw for full-range data", ErrIncompatibleEncoder)
		return e.err
	}

	block := appendBlock(nil, bestEnc, independent, baselineStored, uint64(len(e.pending)), best)
	if _, err := e.w.Write(block); err != nil {
		e.err = err
		return err
	}
	last := e.pending[len(e.pending)-1]
	e.index = append(e.index, IndexEntry{
		Offset:      e.off,
		FirstIndex:  e.total,
		Count:       uint64(len(e.pending)),
		Baseline:    anchor,
		LastValue:   last,
		Independent: independent,
		Enc:         bestEnc,
	})
	e.off += int64(len(block))
	e.blocks++
	e.total += uint64(len(e.pending))
	e.last, e.hasLast = last, true
	e.pending = e.pending[:0]
	return nil
}

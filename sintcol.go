package sintcol

import (
	"errors"
	"io"
)

const (
	magic       = "SICOL1"
	indexMagic  = "SICIDX"
	blockMarker = 0xBD
	indexMarker = 0xFF

	flagChain = 0x01
	flagMask  = 0x07

	maxHeaderBytes = 64
)

type Encoding uint8

const (
	EncZigZag Encoding = iota
	EncDelta
	EncDeltaDelta
)

var (
	ErrCorrupt        = errors.New("sintcol: corrupt data")
	ErrTruncated      = io.ErrUnexpectedEOF
	ErrTooLarge       = errors.New("sintcol: declared length exceeds limit")
	ErrBrokenChain    = errors.New("sintcol: block depends on an unavailable preceding block")
	ErrIndexOutOfRange = errors.New("sintcol: item index out of range")
	ErrClosed         = errors.New("sintcol: writer is closed")
)

type config struct {
	maxBlockElements int
	maxBlockPayload  int
	maxTotalElements int64
	maxIndexEntries  int
	maxFooterBytes   int
}

func defaultConfig() config {
	return config{
		maxBlockElements: 1_000_000,
		maxBlockPayload:  16 << 20,
		maxTotalElements: 1 << 40,
		maxIndexEntries:  1_000_000,
		maxFooterBytes:   16 << 20,
	}
}

type Option func(*config)

func WithMaxBlockElements(n int) Option {
	return func(c *config) { c.maxBlockElements = n }
}

func WithMaxBlockPayload(n int) Option {
	return func(c *config) { c.maxBlockPayload = n }
}

func WithMaxTotalElements(n int64) Option {
	return func(c *config) { c.maxTotalElements = n }
}

func WithMaxIndexEntries(n int) Option {
	return func(c *config) { c.maxIndexEntries = n }
}

func WithMaxFooterBytes(n int) Option {
	return func(c *config) { c.maxFooterBytes = n }
}

func normalizeConfig(c config) config {
	d := defaultConfig()
	if c.maxBlockElements > 0 {
		d.maxBlockElements = c.maxBlockElements
	}
	if c.maxBlockPayload > 0 {
		d.maxBlockPayload = c.maxBlockPayload
	}
	if c.maxTotalElements > 0 {
		d.maxTotalElements = c.maxTotalElements
	}
	if c.maxIndexEntries > 0 {
		d.maxIndexEntries = c.maxIndexEntries
	}
	if c.maxFooterBytes > 0 {
		d.maxFooterBytes = c.maxFooterBytes
	}
	return d
}

type Block struct {
	Ordinal      int
	FirstElement int64
	ElementCount int
	Offset       int64
	Encoding     Encoding
	Chained      bool
	Values       []int64
}

type BlockError struct {
	Offset       int64
	Ordinal      int
	FirstElement int64
	ElementCount int
	Err          error
}

func (e *BlockError) Error() string {
	if e == nil || e.Err == nil {
		return "sintcol: block error"
	}
	return e.Err.Error()
}

func (e *BlockError) Unwrap() error { return e.Err }

type Event struct {
	Block *Block
	Err   *BlockError
}

type indexEntry struct {
	firstIndex uint64
	offset     int64
	count      uint64
	enc        Encoding
	chained    bool
}

func entryFlags(e indexEntry) uint8 {
	flags := uint8(e.enc) << 1
	if e.chained {
		flags |= flagChain
	}
	return flags
}

func parseEntryFlags(flags uint8) (Encoding, bool, error) {
	if flags&^flagMask != 0 {
		return 0, false, ErrCorrupt
	}
	enc := Encoding((flags >> 1) & 0x03)
	if enc > EncDeltaDelta {
		return 0, false, ErrCorrupt
	}
	return enc, flags&flagChain != 0, nil
}

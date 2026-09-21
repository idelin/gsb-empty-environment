package endianconv

import "encoding/binary"

// Decoder parses a stream of records in a fixed byte order. Input may be
// fed in arbitrarily sized chunks. Next distinguishes "need more data"
// (ErrNeedMoreData, keep feeding) from "bad data" (ErrRecordTooLarge,
// stream is corrupt) and checks the length quota before allocating
// anything based on a declared length.
type Decoder struct {
	schema Schema
	order  binary.ByteOrder
	max    int
	buf    []byte
}

// NewDecoder returns a Decoder for records laid out per schema and encoded
// in order. maxRecord is the per-record length quota (<= 0 means no limit).
func NewDecoder(s Schema, order binary.ByteOrder, maxRecord int) *Decoder {
	return &Decoder{schema: s, order: order, max: maxRecord}
}

// Feed appends a chunk of input. Chunks may be of any size, down to a
// single byte.
func (d *Decoder) Feed(p []byte) {
	d.buf = append(d.buf, p...)
}

// Next returns the next complete record, consuming it from the buffer.
//
// If the buffered bytes are a valid but incomplete prefix, it returns
// ErrNeedMoreData. If a length header violates the quota, it returns
// ErrRecordTooLarge immediately — without waiting for the payload the
// (corrupt) header promises, and without allocating for it.
func (d *Decoder) Next() ([]byte, error) {
	n, err := d.schema.recordLen(d.buf, d.order, d.max)
	if err != nil {
		return nil, err
	}
	rec := make([]byte, n) // allocated only after the full length passed quota checks
	copy(rec, d.buf[:n])
	d.buf = d.buf[n:]
	if len(d.buf) == 0 {
		d.buf = nil // release the backing array
	}
	return rec, nil
}

// Buffered reports how many unprocessed bytes are currently held.
func (d *Decoder) Buffered() int { return len(d.buf) }

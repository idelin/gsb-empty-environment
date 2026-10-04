package extsort

import (
	"bufio"
	"encoding/binary"
	"io"
)

// Record is one sortable item. Seq is the global input sequence number
// (uint64, monotonically assigned in original input order) and is the
// tie-breaker that makes the sort stable: equal keys are ordered by Seq.
type Record struct {
	Key   string
	Seq   uint64
	Value []byte
}

// Less orders by (Key, Seq); Seq has full uint64 precision so stability
// holds for arbitrarily large inputs.
func (r *Record) Less(o *Record) bool {
	if r.Key != o.Key {
		return r.Key < o.Key
	}
	return r.Seq < o.Seq
}

// encodeRecord serializes a record as:
// uvarint keyLen | key | uvarint seq | uvarint valueLen | value
func encodeRecord(r *Record) []byte {
	buf := make([]byte, 0, len(r.Key)+len(r.Value)+3*binary.MaxVarintLen64)
	buf = binary.AppendUvarint(buf, uint64(len(r.Key)))
	buf = append(buf, r.Key...)
	buf = binary.AppendUvarint(buf, r.Seq)
	buf = binary.AppendUvarint(buf, uint64(len(r.Value)))
	buf = append(buf, r.Value...)
	return buf
}

// countingReader counts bytes consumed by the decoder (not read-ahead),
// which yields the exact file offset of the next undecoded record.
type countingReader struct {
	br *bufio.Reader
	n  int64
}

func (c *countingReader) ReadByte() (byte, error) {
	b, err := c.br.ReadByte()
	if err == nil {
		c.n++
	}
	return b, err
}

func (c *countingReader) Read(p []byte) (int, error) {
	m, err := c.br.Read(p)
	c.n += int64(m)
	return m, err
}

// recordDecoder decodes records from a run file positioned anywhere.
type recordDecoder struct {
	cr      *countingReader
	offset  int64 // absolute file offset of the next record
	records int64 // records decoded so far
}

func newRecordDecoder(r io.Reader, startOffset int64) *recordDecoder {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReaderSize(r, 1<<16)
	}
	return &recordDecoder{cr: &countingReader{br: br}, offset: startOffset}
}

func (d *recordDecoder) next() (*Record, error) {
	keyLen, err := binary.ReadUvarint(d.cr)
	if err != nil {
		return nil, err
	}
	key := make([]byte, keyLen)
	if _, err := io.ReadFull(d.cr, key); err != nil {
		return nil, err
	}
	seq, err := binary.ReadUvarint(d.cr)
	if err != nil {
		return nil, err
	}
	valLen, err := binary.ReadUvarint(d.cr)
	if err != nil {
		return nil, err
	}
	val := make([]byte, valLen)
	if _, err := io.ReadFull(d.cr, val); err != nil {
		return nil, err
	}
	d.records++
	return &Record{Key: string(key), Seq: seq, Value: val}, nil
}

// position returns the absolute file offset just past the last decoded
// record; seeking an input to this offset resumes decoding exactly.
func (d *recordDecoder) position() int64 { return d.offset + d.cr.n }

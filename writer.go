package walrestore

import (
	"encoding/binary"
	"errors"
	"io"
)

// Writer appends committed batches to a WAL, splitting each logical record into
// fragments at block boundaries and zero-padding the remainder of every block.
// Files written by Writer are readable by Open, Recover and Scan.
type Writer struct {
	w         io.Writer
	blockSize int

	block       []byte
	blockOffset int
	nextSeq     uint64
	closed      bool
}

// NewWriter creates a WAL writer over w using the default block size.
func NewWriter(w io.Writer, opts ...Option) (*Writer, error) {
	cfg, err := buildConfig(opts)
	if err != nil {
		return nil, err
	}
	return &Writer{
		w:         w,
		blockSize: cfg.blockSize,
		block:     make([]byte, cfg.blockSize),
	}, nil
}

// NextSeq returns the sequence number the next WriteBatch will use.
func (w *Writer) NextSeq() uint64 { return w.nextSeq + 1 }

// WriteBatch atomically appends one batch and returns its assigned sequence
// number. Sequence numbers start at one and increase strictly. Entries are
// copied before encoding, so later caller mutations never reach the log.
func (w *Writer) WriteBatch(entries []Entry) (uint64, error) {
	if w.closed {
		return 0, errors.New("walrestore: writer closed")
	}
	if len(entries) == 0 {
		return 0, errors.New("walrestore: refusing to write empty batch")
	}

	w.nextSeq++
	seq := w.nextSeq
	record := encodeBatch(seq, entries)
	if err := w.writeRecord(record); err != nil {
		return 0, err
	}
	return seq, nil
}

// writeRecord splits record into block-bounded fragments.
func (w *Writer) writeRecord(record []byte) error {
	consumed := 0
	first := true

	for consumed < len(record) {
		available := w.blockSize - w.blockOffset
		if available <= fragmentHeaderSize {
			if err := w.flushBlock(); err != nil {
				return err
			}
			available = w.blockSize - w.blockOffset
		}

		capacity := available - fragmentHeaderSize
		chunk := len(record) - consumed
		last := chunk <= capacity
		if chunk > capacity {
			chunk = capacity
		}

		var fragType uint8
		switch {
		case first && last:
			fragType = fragFull
		case first:
			fragType = fragFirst
		case last:
			fragType = fragLast
		default:
			fragType = fragMiddle
		}

		payload := record[consumed : consumed+chunk]
		header := makeFragmentHeader(fragType, payload)
		w.blockOffset += copy(w.block[w.blockOffset:], header)
		w.blockOffset += copy(w.block[w.blockOffset:], payload)

		consumed += chunk
		first = false

		if w.blockOffset == w.blockSize {
			if err := w.flushBlock(); err != nil {
				return err
			}
		}
	}
	return nil
}

// flushBlock writes the current block (zero padding fills unused tail bytes)
// and prepares a fresh block.
func (w *Writer) flushBlock() error {
	for i := w.blockOffset; i < w.blockSize; i++ {
		w.block[i] = 0
	}
	if _, err := w.w.Write(w.block); err != nil {
		return err
	}
	w.block = make([]byte, w.blockSize)
	w.blockOffset = 0
	return nil
}

// Sync flushes any buffered partial block. Without Sync, an un-flushed tail is
// not part of the durable log; a torn tail on reopen is handled by recovery.
func (w *Writer) Sync() error {
	if w.blockOffset == 0 {
		return nil
	}
	return w.flushBlock()
}

// Close flushes buffered data and marks the writer unusable.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	return w.Sync()
}

// makeFragmentHeader builds the 7-byte little-endian fragment header.
func makeFragmentHeader(fragType uint8, payload []byte) []byte {
	header := make([]byte, fragmentHeaderSize)
	binary.LittleEndian.PutUint32(header, checksum(fragType, len(payload), payload))
	binary.LittleEndian.PutUint16(header[4:], uint16(len(payload)))
	header[6] = fragType
	return header
}

// Record encoding (all integers little-endian):
//
//	seq     uint64
//	count   uint32
//	entries[count]:
//	  op      uint8
//	  keyLen  uint32
//	  key     [keyLen]
//	  valLen  uint32
//	  value   [valLen]   (empty for OpDelete)

func encodeBatch(seq uint64, entries []Entry) []byte {
	size := 12
	for _, entry := range entries {
		size += 5 + len(entry.Key) + 4 + len(entry.Value)
	}

	buf := make([]byte, 0, size)
	var scratch [8]byte

	binary.LittleEndian.PutUint64(scratch[:], seq)
	buf = append(buf, scratch[:]...)
	binary.LittleEndian.PutUint32(scratch[:4], uint32(len(entries)))
	buf = append(buf, scratch[:4]...)

	for _, entry := range entries {
		buf = append(buf, byte(entry.Op))
		binary.LittleEndian.PutUint32(scratch[:4], uint32(len(entry.Key)))
		buf = append(buf, scratch[:4]...)
		buf = append(buf, entry.Key...)
		binary.LittleEndian.PutUint32(scratch[:4], uint32(len(entry.Value)))
		buf = append(buf, scratch[:4]...)
		buf = append(buf, entry.Value...)
	}
	return buf
}

func decodeBatch(record []byte) (*Batch, error) {
	if len(record) < 12 {
		return nil, errors.New("batch record shorter than header")
	}

	seq := binary.LittleEndian.Uint64(record[:8])
	count := int(binary.LittleEndian.Uint32(record[8:12]))
	if count == 0 {
		return nil, errors.New("batch contains no entries")
	}

	pos := 12
	entries := make([]Entry, 0, count)
	for i := 0; i < count; i++ {
		if pos+5 > len(record) {
			return nil, errors.New("truncated entry header")
		}
		op := Op(record[pos])
		if op != OpPut && op != OpDelete {
			return nil, errors.New("unknown entry operation")
		}
		keyLen := int(binary.LittleEndian.Uint32(record[pos+1:]))
		pos += 5
		if pos+keyLen+4 > len(record) {
			return nil, errors.New("truncated entry key")
		}
		key := append([]byte(nil), record[pos:pos+keyLen]...)
		pos += keyLen
		valLen := int(binary.LittleEndian.Uint32(record[pos:]))
		pos += 4
		if pos+valLen > len(record) {
			return nil, errors.New("truncated entry value")
		}
		value := append([]byte(nil), record[pos:pos+valLen]...)
		pos += valLen

		if op == OpDelete {
			value = nil
		}
		entries = append(entries, Entry{Op: op, Key: key, Value: value})
	}

	if pos != len(record) {
		return nil, errors.New("trailing bytes after batch entries")
	}
	return &Batch{Seq: seq, Entries: entries}, nil
}

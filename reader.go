package walrestore

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"sort"
)

// Physical fragment layout, little-endian:
//
//	crc32  uint32  (4 bytes) checksum over type byte, length field and payload
//	length uint16  (2 bytes) payload length, at most blockSize-headerSize
//	type   uint8   (1 byte)  one of the fragment type constants below
//	payload [length] bytes
//
// A fragment never crosses a block boundary; logical records cross blocks by
// being split into multiple fragments. Unused block bytes are zero padding.
const fragmentHeaderSize = 7

const (
	fragFull   uint8 = 1 // whole record fits in this fragment
	fragFirst  uint8 = 2 // first fragment of a multi-fragment record
	fragMiddle uint8 = 3 // interior fragment
	fragLast   uint8 = 4 // final fragment
)

var crcTable = crc32.MakeTable(crc32.Castagnoli)

// Iterator streams committed batches out of a WAL without materializing the
// whole log in memory. A batch is handed out only after its entire record has
// been reassembled and verified, so partial batches are never observable.
type Iterator struct {
	r         io.ReaderAt
	size      int64
	blockSize int

	block       []byte
	blockIndex  int
	blockCount  int
	blockOffset int // read cursor inside the current block

	assembling bool
	record     bytes.Buffer

	lastSeq uint64

	truncated   bool
	truncatedAt int64
	truncErr    error
	done        bool
}

// Scan opens a streaming iterator over a WAL of size bytes. Call Close when
// done; the iterator does not close r.
func Scan(r io.ReaderAt, size int64, opts ...Option) (*Iterator, error) {
	if size < 0 {
		return nil, errors.New("walrestore: negative log size")
	}
	cfg, err := buildConfig(opts)
	if err != nil {
		return nil, err
	}
	return newIterator(r, size, cfg), nil
}

func newIterator(r io.ReaderAt, size int64, cfg config) *Iterator {
	return &Iterator{
		r:          r,
		size:       size,
		blockSize:  cfg.blockSize,
		blockCount: int((size + int64(cfg.blockSize) - 1) / int64(cfg.blockSize)),
	}
}

// Next returns the next committed batch. It returns io.EOF at a clean end,
// ErrTruncated once after cutting a torn tail, or ErrCorruption for damage in
// a non-final block. Context cancellation is checked between batches via Recover.
func (it *Iterator) Next() (*Batch, error) {
	if it.done {
		return nil, io.EOF
	}

	for {
		if it.block == nil {
			if err := it.loadNextBlock(); err != nil {
				return nil, err
			}
			if it.block == nil {
				return nil, it.endOfLog()
			}
		}

		blockLen := len(it.block)

		// Remaining bytes in this block are zero padding.
		if it.blockOffset >= blockLen {
			if err := it.verifyPadding(); err != nil {
				return nil, err
			}
			it.block = nil
			continue
		}

		if it.block[it.blockOffset] == 0 {
			if err := it.verifyPadding(); err != nil {
				return nil, err
			}
			it.block = nil
			continue
		}

		if blockLen-it.blockOffset < fragmentHeaderSize {
			return nil, it.structural("fragment header overruns block")
		}

		headerStart := it.blockOffset
		crc := binary.LittleEndian.Uint32(it.block[headerStart:])
		length := int(binary.LittleEndian.Uint16(it.block[headerStart+4:]))
		fragType := it.block[headerStart+6]

		if length == 0 {
			return nil, it.structural("zero-length fragment")
		}
		if length > it.blockSize-fragmentHeaderSize {
			return nil, it.structural("declared fragment length exceeds block capacity")
		}
		if it.blockOffset+fragmentHeaderSize+length > blockLen {
			return nil, it.structural("fragment payload overruns block")
		}

		payload := it.block[headerStart+fragmentHeaderSize : headerStart+fragmentHeaderSize+length]
		if checksum(fragType, length, payload) != crc {
			return nil, it.structural("crc mismatch")
		}
		it.blockOffset = headerStart + fragmentHeaderSize + length

		switch fragType {
		case fragFull:
			if it.assembling {
				return nil, it.structural("full fragment while record in progress")
			}
			return it.publish(payload)

		case fragFirst:
			if it.assembling {
				return nil, it.structural("first fragment while record in progress")
			}
			it.assembling = true
			it.record.Reset()
			it.record.Write(payload)

		case fragMiddle:
			if !it.assembling {
				return nil, it.structural("middle fragment without open record")
			}
			it.record.Write(payload)

		case fragLast:
			if !it.assembling {
				return nil, it.structural("last fragment without open record")
			}
			it.record.Write(payload)
			it.assembling = false
			record := it.record.Bytes()
			it.record.Reset()
			return it.publish(record)

		default:
			return nil, it.structural("unknown fragment type")
		}
	}
}

// publish decodes one complete record into a batch or reports a structural
// failure (torn tail when it sits in the final block).
func (it *Iterator) publish(record []byte) (*Batch, error) {
	batch, err := decodeBatch(record)
	if err != nil {
		return nil, it.structural(err.Error())
	}
	if batch.Seq != it.lastSeq+1 {
		return nil, it.structural("batch sequence is not strictly increasing by one")
	}
	it.lastSeq = batch.Seq
	return batch, nil
}

// loadNextBlock fills the block buffer from the underlying reader.
func (it *Iterator) loadNextBlock() error {
	if it.blockIndex >= it.blockCount {
		return nil
	}

	offset := int64(it.blockIndex) * int64(it.blockSize)
	buf := make([]byte, it.blockSize)
	n, err := it.r.ReadAt(buf, offset)

	isTail := it.blockIndex == it.blockCount-1
	if err != nil && !(err == io.EOF && isTail) {
		if isTail {
			return it.truncate(err.Error())
		}
		return err
	}

	// Only the final physical block may be shorter than blockSize.
	if n < it.blockSize && !isTail {
		return it.structural("short read in non-final block")
	}
	it.block = buf[:n]
	it.blockOffset = 0
	it.blockIndex++
	return nil
}

// verifyPadding ensures every byte from the cursor to the block end is zero.
func (it *Iterator) verifyPadding() error {
	for _, b := range it.block[it.blockOffset:] {
		if b != 0 {
			return it.structural("non-zero padding byte")
		}
	}
	return nil
}

// endOfLog finalizes a clean log, rejecting a record that never reassembled.
func (it *Iterator) endOfLog() error {
	if it.assembling {
		return it.truncate("log ended inside a multi-fragment record")
	}
	it.done = true
	return io.EOF
}

// isTailBlock reports whether the block currently being parsed is the final
// physical block of the log. blockIndex was already advanced by loadNextBlock.
func (it *Iterator) isTailBlock() bool {
	return it.blockIndex > 0 && it.blockIndex == it.blockCount
}

// structural maps a structural failure onto torn-tail truncation for the final
// block and onto ErrCorruption everywhere else.
func (it *Iterator) structural(msg string) error {
	if it.isTailBlock() {
		return it.truncate(msg)
	}
	return errors.Join(ErrCorruption, errors.New(msg))
}

// truncate cuts the log at the start of the block containing the damage. The
// caller has already emitted every complete batch preceding it.
func (it *Iterator) truncate(msg string) error {
	if it.truncated {
		it.done = true
		return io.EOF
	}
	it.truncated = true
	it.truncatedAt = int64(it.blockIndex-1) * int64(it.blockSize)
	if it.truncatedAt < 0 {
		it.truncatedAt = 0
	}
	it.truncErr = errors.Join(ErrTruncated, errors.New(msg))
	it.done = true
	return it.truncErr
}

// Truncated reports whether the iterator discarded a torn tail.
func (it *Iterator) Truncated() bool { return it.truncated }

// TruncatedOffset is the byte offset at which a torn tail was cut, or -1.
func (it *Iterator) TruncatedOffset() int64 {
	if !it.truncated {
		return -1
	}
	return it.truncatedAt
}

// TruncationError returns the wrapped torn-tail error, or nil.
func (it *Iterator) TruncationError() error { return it.truncErr }

// Close releases the iterator. After Close, Next returns io.EOF.
func (it *Iterator) Close() error {
	it.done = true
	it.assembling = false
	return nil
}

// checksum computes the fragment CRC over type, length and payload, matching
// makeFragmentHeader.
func checksum(fragType uint8, length int, payload []byte) uint32 {
	h := crc32.New(crcTable)
	var scratch [3]byte
	scratch[0] = byte(length)
	scratch[1] = byte(length >> 8)
	scratch[2] = fragType
	h.Write(scratch[:])
	h.Write(payload)
	return h.Sum32()
}

// sortIndices permutes order so that keys[order[i]] is ascending.
func sortIndices(order []int, keys [][]byte) {
	sort.Slice(order, func(i, j int) bool {
		return bytes.Compare(keys[order[i]], keys[order[j]]) < 0
	})
}

// lookupKey binary-searches the sorted key index.
func lookupKey(keys [][]byte, key []byte) (int, bool) {
	pos := sort.Search(len(keys), func(i int) bool {
		return bytes.Compare(keys[i], key) >= 0
	})
	if pos < len(keys) && bytes.Equal(keys[pos], key) {
		return pos, true
	}
	return 0, false
}

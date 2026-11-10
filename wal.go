package main

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
)

const (
	blockSize  = 32768
	headerSize = 7
)

const (
	recZero   = 0
	recFull   = 1
	recFirst  = 2
	recMiddle = 3
	recLast   = 4
)

const crcMaskDelta = 0xa282ead8

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

func maskCRC(crc uint32) uint32 {
	return ((crc >> 15) | (crc << 17)) + crcMaskDelta
}

func unmaskCRC(masked uint32) uint32 {
	rot := masked - crcMaskDelta
	return (rot >> 17) | (rot << 15)
}

type Corruption struct {
	Offset int64
	Reason string
}

func (c Corruption) String() string {
	return fmt.Sprintf("offset %d: %s", c.Offset, c.Reason)
}

// Record is one fully reassembled logical record (a WriteBatch payload).
type Record struct {
	Offset int64
	Data   []byte
}

// LogReader reads physical fragments from a LevelDB log file and
// reassembles logical records. Corrupted or incomplete fragments are
// dropped and reported; a partial record never yields a commit.
type LogReader struct {
	r   io.Reader
	buf []byte
	n   int
	pos int
	off int64 // absolute offset of buf[0]
	eof bool

	pending    []byte
	pendingOff int64
	inPending  bool

	Corruptions []Corruption
}

func NewLogReader(r io.Reader) *LogReader {
	return &LogReader{r: r, buf: make([]byte, blockSize)}
}

func (lr *LogReader) report(offset int64, format string, args ...any) {
	lr.Corruptions = append(lr.Corruptions, Corruption{
		Offset: offset,
		Reason: fmt.Sprintf(format, args...),
	})
}

func (lr *LogReader) dropPending(reason string) {
	if lr.inPending {
		lr.report(lr.pendingOff, "dropping partial record: %s", reason)
	}
	lr.pending = lr.pending[:0]
	lr.inPending = false
}

// fillBlock loads the next 32KB block. Returns false at EOF.
func (lr *LogReader) fillBlock() bool {
	if lr.eof {
		return false
	}
	lr.off += int64(lr.n) // advance past previous block
	n, err := io.ReadFull(lr.r, lr.buf)
	if err == io.EOF && n == 0 {
		lr.eof = true
		return false
	}
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		lr.report(lr.off, "read error: %v", err)
		lr.eof = true
		return false
	}
	if err == io.ErrUnexpectedEOF {
		// A short final block is normal: the writer stops mid-block and
		// the remaining bytes simply do not exist. Parse what is there.
		lr.eof = true
	}
	lr.n = n
	lr.pos = 0
	return n > 0
}

// Next returns the next complete logical record, or nil at end of log.
func (lr *LogReader) Next() *Record {
	for {
		if lr.pos+headerSize > lr.n {
			// Block trailer: must be zero padding.
			for i := lr.pos; i < lr.n; i++ {
				if lr.buf[i] != 0 {
					lr.report(lr.off+int64(lr.pos), "non-zero block trailer padding (%d bytes)", lr.n-lr.pos)
					break
				}
			}
			if !lr.fillBlock() {
				lr.dropPending("missing fragment at end of log")
				return nil
			}
			continue
		}

		headOff := lr.off + int64(lr.pos)
		storedCRC := binary.LittleEndian.Uint32(lr.buf[lr.pos:])
		length := int(binary.LittleEndian.Uint16(lr.buf[lr.pos+4:]))
		recType := lr.buf[lr.pos+6]

		if length+headerSize > lr.n-lr.pos {
			lr.report(headOff, "fragment length %d overruns block remainder %d", length, lr.n-lr.pos-headerSize)
			lr.dropPending("fragment length overrun")
			lr.pos = lr.n
			continue
		}

		payload := lr.buf[lr.pos+headerSize : lr.pos+headerSize+length]
		want := maskCRC(crc32.Checksum(lr.buf[lr.pos+6:lr.pos+headerSize+length], castagnoli))
		if storedCRC != want {
			lr.report(headOff, "CRC32C mismatch: stored %#08x want %#08x", storedCRC, want)
			lr.dropPending("CRC mismatch in fragment chain")
			lr.pos = lr.n // skip rest of block
			continue
		}
		lr.pos += headerSize + length

		switch recType {
		case recFull:
			if lr.inPending {
				lr.dropPending("FULL record interrupts fragmented record")
			}
			data := make([]byte, len(payload))
			copy(data, payload)
			return &Record{Offset: headOff, Data: data}
		case recFirst:
			if lr.inPending {
				lr.dropPending("FIRST record interrupts fragmented record")
			}
			lr.pending = append(lr.pending[:0], payload...)
			lr.pendingOff = headOff
			lr.inPending = true
		case recMiddle:
			if !lr.inPending {
				lr.report(headOff, "MIDDLE record without FIRST")
				continue
			}
			lr.pending = append(lr.pending, payload...)
		case recLast:
			if !lr.inPending {
				lr.report(headOff, "LAST record without FIRST")
				continue
			}
			lr.pending = append(lr.pending, payload...)
			data := make([]byte, len(lr.pending))
			copy(data, lr.pending)
			off := lr.pendingOff
			lr.pending = lr.pending[:0]
			lr.inPending = false
			return &Record{Offset: off, Data: data}
		default:
			lr.report(headOff, "unknown record type %d", recType)
			lr.dropPending("unknown record type")
			lr.pos = lr.n
		}
	}
}

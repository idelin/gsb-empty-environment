package llex

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// Wire format (see FORMAT.md for the full specification).
//
// Code table header, self-delimiting:
//   magic    4B  "LLXT"
//   version  1B  = 1
//   L        1B  maximum code length, 1..64
//   n        4B  LE, original symbol count
//   n entries, symbols in canonical (ascending ID) order:
//     idLen  2B  LE
//     id     idLen bytes
//     len    1B  code length in [1, L]
//   crc32    4B  LE, IEEE, over every preceding header byte
//
// Block:
//   header   as above
//   dataBits 4B  LE, number of valid data bits
//   data     ceil(dataBits/8) bytes, MSB-first, padding bits must be zero
//
// File (chunked encoding):
//   magic    4B  "LLXF"
//   version  1B  = 1
//   flags    1B  = 0
//   blocks   4B  LE
//   blocks index entries, each 16B:
//     offset 8B  LE, absolute file offset of the block
//     byteLen 4B LE, total block bytes (header + count + data)
//     symbols 4B LE, number of decoded symbols in the block
//   crc32    4B  LE, IEEE, over every preceding file byte
//   blocks concatenated at their offsets, contiguously

const (
	tableMagic  = "LLXT"
	fileMagic   = "LLXF"
	wireVersion = 1

	tableFixedLen = 4 + 1 + 1 + 4 + 4 // magic, version, L, n, crc32
	fileFixedLen  = 4 + 1 + 1 + 4 + 4 // magic, version, flags, count, crc32
	indexEntryLen = 8 + 4 + 4
	minBlockLen   = tableFixedLen + 4 // empty table + dataBits field
)

var errShort = errors.New("llex: truncated input")

// MarshalTable serializes a code table. The table must be well formed
// (sorted IDs, lengths in [1, L]); structural validity (Kraft <= 1) is
// verified by the decoder via BuildCode.
func MarshalTable(t *Table) ([]byte, error) {
	if t.L < 1 || t.L > MaxCodecL {
		return nil, fmt.Errorf("llex: cannot marshal table with L=%d (codec range is [1,%d])", t.L, MaxCodecL)
	}
	if len(t.IDs) != len(t.Lengths) {
		return nil, fmt.Errorf("llex: %d IDs but %d lengths", len(t.IDs), len(t.Lengths))
	}
	size := tableFixedLen
	for i, id := range t.IDs {
		if i > 0 && t.IDs[i-1] >= id {
			return nil, fmt.Errorf("llex: symbol IDs not in strictly ascending order at %q", id)
		}
		if len(id) > 0xffff {
			return nil, fmt.Errorf("llex: symbol ID %q too long (%d bytes)", id, len(id))
		}
		if t.Lengths[i] < 1 || t.Lengths[i] > t.L {
			return nil, fmt.Errorf("llex: symbol %q has length %d outside [1,%d]", id, t.Lengths[i], t.L)
		}
		size += 3 + len(id)
	}
	out := make([]byte, 0, size)
	out = append(out, tableMagic...)
	out = append(out, wireVersion, byte(t.L))
	out = binary.LittleEndian.AppendUint32(out, uint32(len(t.IDs)))
	for i, id := range t.IDs {
		out = binary.LittleEndian.AppendUint16(out, uint16(len(id)))
		out = append(out, id...)
		out = append(out, byte(t.Lengths[i]))
	}
	out = binary.LittleEndian.AppendUint32(out, crc32.ChecksumIEEE(out))
	return out, nil
}

// ParseTable parses and fully validates a code table header, returning the
// canonical code and the header length in bytes. It rejects bad magic or
// version, L out of range, counts inconsistent with the available bytes,
// non-increasing IDs, lengths outside [1, L], checksum mismatches, and
// oversubscribed tables (via BuildCode).
func ParseTable(data []byte) (*Code, int, error) {
	if len(data) < tableFixedLen {
		return nil, 0, errShort
	}
	if string(data[:4]) != tableMagic {
		return nil, 0, errors.New("llex: bad table magic")
	}
	if data[4] != wireVersion {
		return nil, 0, fmt.Errorf("llex: unsupported table version %d", data[4])
	}
	L := int(data[5])
	if L < 1 || L > MaxCodecL {
		return nil, 0, fmt.Errorf("llex: table L=%d outside [1,%d]", L, MaxCodecL)
	}
	n := int(binary.LittleEndian.Uint32(data[6:10]))
	// Bound the count by the bytes actually present before allocating:
	// every entry needs at least 3 bytes (idLen + length).
	if n > (len(data)-tableFixedLen)/3 {
		return nil, 0, fmt.Errorf("llex: declared symbol count %d exceeds available %d header bytes", n, len(data))
	}
	t := &Table{L: L, IDs: make([]string, 0, n), Lengths: make([]int, 0, n)}
	pos := 10
	for i := 0; i < n; i++ {
		if pos+3 > len(data) {
			return nil, 0, errShort
		}
		idLen := int(binary.LittleEndian.Uint16(data[pos : pos+2]))
		if pos+3+idLen > len(data) {
			return nil, 0, errShort
		}
		id := string(data[pos+2 : pos+2+idLen])
		l := int(data[pos+2+idLen])
		if l < 1 || l > L {
			return nil, 0, fmt.Errorf("llex: symbol %q has length %d outside [1,%d]", id, l, L)
		}
		if i > 0 && id <= t.IDs[i-1] {
			return nil, 0, fmt.Errorf("llex: symbol IDs not strictly ascending at %q", id)
		}
		t.IDs = append(t.IDs, id)
		t.Lengths = append(t.Lengths, l)
		pos += 3 + idLen
	}
	if pos+4 > len(data) {
		return nil, 0, errShort
	}
	if crc32.ChecksumIEEE(data[:pos]) != binary.LittleEndian.Uint32(data[pos:pos+4]) {
		return nil, 0, errors.New("llex: table checksum mismatch")
	}
	code, err := BuildCode(t)
	if err != nil {
		return nil, 0, err
	}
	return code, pos + 4, nil
}

// MarshalBlock serializes a code table plus the encoded token stream.
// tokens are indices into t.IDs. The layout is header || dataBits || data.
func MarshalBlock(t *Table, tokens []uint32) ([]byte, error) {
	code, err := BuildCode(t)
	if err != nil {
		return nil, err
	}
	hdr, err := MarshalTable(t)
	if err != nil {
		return nil, err
	}
	w := new(bitWriter)
	for _, tok := range tokens {
		if int(tok) >= len(t.IDs) {
			return nil, fmt.Errorf("llex: token %d outside table of %d symbols", tok, len(t.IDs))
		}
		cw := code.enc[t.IDs[tok]]
		w.Write(cw.bits, cw.n)
	}
	out := make([]byte, 0, len(hdr)+4+len(w.Bytes()))
	out = append(out, hdr...)
	out = binary.LittleEndian.AppendUint32(out, uint32(w.bitLen))
	out = append(out, w.Bytes()...)
	return out, nil
}

// DecodeBlockData decodes one block. It returns the symbol IDs and the
// number of block bytes consumed. maxOutput >= 0 caps the number of
// decoded symbols (negative means unlimited); exceeding the cap is an
// error, never an over-allocation.
//
// Rejected: non-canonical (non-zero) padding bits, codewords not defined
// by an incomplete table, codewords longer than L, and truncated final
// codewords.
func DecodeBlockData(block []byte, maxOutput int) ([]string, int, error) {
	code, hdrLen, err := ParseTable(block)
	if err != nil {
		return nil, 0, err
	}
	if len(block) < hdrLen+4 {
		return nil, 0, errShort
	}
	dataBits := int(binary.LittleEndian.Uint32(block[hdrLen : hdrLen+4]))
	dataBytes := (dataBits + 7) / 8
	if hdrLen+4+dataBytes > len(block) {
		return nil, 0, errShort
	}
	payload := block[hdrLen+4 : hdrLen+4+dataBytes]
	if rem := dataBits % 8; rem != 0 {
		mask := byte(1<<uint(8-rem)) - 1
		if payload[dataBytes-1]&mask != 0 {
			return nil, 0, errors.New("llex: non-canonical padding: bits beyond dataBits must be zero")
		}
	}
	r := &bitReader{data: payload, limit: dataBits}
	out := make([]string, 0, 16)
	for r.pos < r.limit {
		var v uint64
		l := 0
		for {
			b, ok := r.bit()
			if !ok {
				return nil, 0, errors.New("llex: data ends mid-codeword")
			}
			v = v<<1 | b
			l++
			if l > code.L {
				return nil, 0, fmt.Errorf("llex: illegal codeword: no defined codeword within L=%d bits", code.L)
			}
			if id, ok := code.lookup(l, v); ok {
				if maxOutput >= 0 && len(out) >= maxOutput {
					return nil, 0, fmt.Errorf("llex: output limit of %d symbols exceeded", maxOutput)
				}
				out = append(out, id)
				break
			}
		}
	}
	return out, hdrLen + 4 + dataBytes, nil
}

// BlockEntry is one file index entry.
type BlockEntry struct {
	Offset  uint64 // absolute file offset of the block
	ByteLen uint32 // total block bytes
	Symbols uint32 // decoded symbol count
}

// marshalFile assembles blocks into a file with an index.
func marshalFile(blocks [][]byte, counts []uint32) []byte {
	hdrLen := fileFixedLen + indexEntryLen*len(blocks)
	out := make([]byte, 0, hdrLen)
	out = append(out, fileMagic...)
	out = append(out, wireVersion, 0)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(blocks)))
	off := uint64(hdrLen)
	for i, b := range blocks {
		out = binary.LittleEndian.AppendUint64(out, off)
		out = binary.LittleEndian.AppendUint32(out, uint32(len(b)))
		out = binary.LittleEndian.AppendUint32(out, counts[i])
		off += uint64(len(b))
	}
	out = binary.LittleEndian.AppendUint32(out, crc32.ChecksumIEEE(out))
	for _, b := range blocks {
		out = append(out, b...)
	}
	return out
}

// parseIndex parses and validates the file header and index. It bounds
// every count by the bytes actually present before allocating, verifies
// the checksum, and checks per-entry sanity (minimum block size, symbol
// count consistent with the bit budget). Offset ordering and range checks
// against the full file are done by ParseFile; the streaming decoder
// repeats them incrementally.
func parseIndex(data []byte, maxBlocks int) ([]BlockEntry, int, error) {
	if len(data) < fileFixedLen {
		return nil, 0, errShort
	}
	if string(data[:4]) != fileMagic {
		return nil, 0, errors.New("llex: bad file magic")
	}
	if data[4] != wireVersion {
		return nil, 0, fmt.Errorf("llex: unsupported file version %d", data[4])
	}
	if data[5] != 0 {
		return nil, 0, fmt.Errorf("llex: unsupported file flags %d", data[5])
	}
	nb := int(binary.LittleEndian.Uint32(data[6:10]))
	if maxBlocks >= 0 && nb > maxBlocks {
		return nil, 0, fmt.Errorf("llex: block count %d exceeds limit %d", nb, maxBlocks)
	}
	// Every index entry occupies 16 bytes; reject counts that cannot
	// possibly fit before allocating anything.
	if nb > (len(data)-fileFixedLen)/indexEntryLen {
		return nil, 0, fmt.Errorf("llex: declared block count %d exceeds available %d index bytes", nb, len(data))
	}
	hdrLen := fileFixedLen + indexEntryLen*nb
	entries := make([]BlockEntry, 0, nb)
	pos := 10
	for i := 0; i < nb; i++ {
		e := BlockEntry{
			Offset:  binary.LittleEndian.Uint64(data[pos : pos+8]),
			ByteLen: binary.LittleEndian.Uint32(data[pos+8 : pos+12]),
			Symbols: binary.LittleEndian.Uint32(data[pos+12 : pos+16]),
		}
		if e.Offset >= 1<<48 {
			return nil, 0, fmt.Errorf("llex: block %d offset out of range", i)
		}
		if e.ByteLen < minBlockLen {
			return nil, 0, fmt.Errorf("llex: block %d length %d below minimum %d", i, e.ByteLen, minBlockLen)
		}
		// Every symbol needs at least one bit.
		if uint64(e.Symbols) > 8*uint64(e.ByteLen) {
			return nil, 0, fmt.Errorf("llex: block %d declares %d symbols in only %d bytes", i, e.Symbols, e.ByteLen)
		}
		entries = append(entries, e)
		pos += indexEntryLen
	}
	if crc32.ChecksumIEEE(data[:pos]) != binary.LittleEndian.Uint32(data[pos:pos+4]) {
		return nil, 0, errors.New("llex: file index checksum mismatch")
	}
	return entries, hdrLen, nil
}

// ParseFile parses the file header and index, validating that the blocks
// are contiguous, in order, and inside the file.
func ParseFile(data []byte, maxBlocks int) ([]BlockEntry, error) {
	entries, hdrLen, err := parseIndex(data, maxBlocks)
	if err != nil {
		return nil, err
	}
	expect := uint64(hdrLen)
	for i, e := range entries {
		if e.Offset != expect {
			return nil, fmt.Errorf("llex: block %d starts at %d, expected contiguous offset %d", i, e.Offset, expect)
		}
		expect += uint64(e.ByteLen)
	}
	if expect != uint64(len(data)) {
		return nil, fmt.Errorf("llex: file has %d bytes, blocks account for %d", len(data), expect)
	}
	return entries, nil
}

// DecodeFile decodes a whole chunked file.
func DecodeFile(data []byte, maxBlocks, maxOutput int) ([]string, error) {
	entries, err := ParseFile(data, maxBlocks)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, 64)
	for i := range entries {
		syms, err := DecodeFileBlock(data, entries, i, maxOutput-len(out))
		if err != nil {
			return nil, err
		}
		out = append(out, syms...)
	}
	return out, nil
}

// DecodeFileBlock decodes a single block by index, supporting random
// access. The index entry is cross-checked against the decoded block.
func DecodeFileBlock(data []byte, entries []BlockEntry, i int, maxOutput int) ([]string, error) {
	if i < 0 || i >= len(entries) {
		return nil, fmt.Errorf("llex: block index %d out of range (%d blocks)", i, len(entries))
	}
	e := entries[i]
	end := e.Offset + uint64(e.ByteLen)
	if end > uint64(len(data)) || end < e.Offset {
		return nil, fmt.Errorf("llex: block %d spans [%d,%d) outside %d file bytes", i, e.Offset, end, len(data))
	}
	syms, consumed, err := DecodeBlockData(data[e.Offset:end], maxOutput)
	if err != nil {
		return nil, fmt.Errorf("llex: block %d: %w", i, err)
	}
	if consumed != int(e.ByteLen) {
		return nil, fmt.Errorf("llex: block %d consumed %d bytes, index declares %d", i, consumed, e.ByteLen)
	}
	if len(syms) != int(e.Symbols) {
		return nil, fmt.Errorf("llex: block %d decoded %d symbols, index declares %d", i, len(syms), e.Symbols)
	}
	return syms, nil
}

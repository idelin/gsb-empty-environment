package checkpoint

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
)

// File layout of a checkpoint (all integers little-endian):
//
//	header:  magic(8) | id u64 | version u64 | prevChain [32]byte | numChunks u32
//	chunks:  [ dataLen u32 | chunkHash [32]byte | data ]...
//	manifest: numEntries u32 | [ pageID u32 | version u64 | chunkIdx u32 ]...
//	trailer: finalChainHash [32]byte
//
// chunkHash_i = SHA-256(chunkData_i)
// chain_0     = prevChain (final chain hash of the previous checkpoint)
// chain_i     = SHA-256(chain_{i-1} || chunkHash_i)
const (
	magic      = "LSCKPT01"
	headerSize = 8 + 8 + 8 + 32 + 4
	// chunkPages is the number of pages packed into one chunk.
	chunkPages = 16
)

type manifestEntry struct {
	pageID   uint32
	version  uint64
	chunkIdx uint32
}

type chunkBlob struct {
	hash [32]byte
	data []byte
}

type parsedCheckpoint struct {
	id         uint64
	version    uint64
	prevChain  [32]byte
	chunks     []chunkBlob
	manifest   []manifestEntry
	finalChain [32]byte
}

// encodeChunk serializes pages (by ascending key for determinism).
func encodeChunk(pages []dirtyPage) []byte {
	buf := &bytes.Buffer{}
	for _, dp := range pages {
		writeU32(buf, uint32(dp.id))
		writeU64(buf, dp.page.version)
		keys := make([]string, 0, len(dp.page.entries))
		for k := range dp.page.entries {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		writeU32(buf, uint32(len(keys)))
		for _, k := range keys {
			v := dp.page.entries[k]
			writeU16(buf, uint16(len(k)))
			buf.WriteString(k)
			writeU32(buf, uint32(len(v)))
			buf.WriteString(v)
		}
	}
	return buf.Bytes()
}

type decodedPage struct {
	id      uint32
	version uint64
	entries map[string]string
}

func decodeChunk(data []byte) ([]decodedPage, error) {
	r := &byteReader{buf: data}
	var pages []decodedPage
	for r.off < len(r.buf) {
		id, err := r.u32()
		if err != nil {
			return nil, err
		}
		version, err := r.u64()
		if err != nil {
			return nil, err
		}
		n, err := r.u32()
		if err != nil {
			return nil, err
		}
		dp := decodedPage{id: id, version: version, entries: make(map[string]string, n)}
		for i := uint32(0); i < n; i++ {
			klen, err := r.u16()
			if err != nil {
				return nil, err
			}
			k, err := r.bytes(int(klen))
			if err != nil {
				return nil, err
			}
			vlen, err := r.u32()
			if err != nil {
				return nil, err
			}
			v, err := r.bytes(int(vlen))
			if err != nil {
				return nil, err
			}
			dp.entries[string(k)] = string(v)
		}
		pages = append(pages, dp)
	}
	return pages, nil
}

// parseCheckpoint structurally parses a checkpoint file.
func parseCheckpoint(raw []byte) (*parsedCheckpoint, error) {
	r := &byteReader{buf: raw}
	m, err := r.bytes(8)
	if err != nil || string(m) != magic {
		return nil, fmt.Errorf("bad magic")
	}
	p := &parsedCheckpoint{}
	if p.id, err = r.u64(); err != nil {
		return nil, err
	}
	if p.version, err = r.u64(); err != nil {
		return nil, err
	}
	prev, err := r.bytes(32)
	if err != nil {
		return nil, err
	}
	copy(p.prevChain[:], prev)
	numChunks, err := r.u32()
	if err != nil {
		return nil, err
	}
	for i := uint32(0); i < numChunks; i++ {
		dataLen, err := r.u32()
		if err != nil {
			return nil, fmt.Errorf("chunk %d: %w", i, err)
		}
		h, err := r.bytes(32)
		if err != nil {
			return nil, fmt.Errorf("chunk %d: %w", i, err)
		}
		data, err := r.bytes(int(dataLen))
		if err != nil {
			return nil, fmt.Errorf("chunk %d: %w", i, err)
		}
		var cb chunkBlob
		copy(cb.hash[:], h)
		cb.data = data
		p.chunks = append(p.chunks, cb)
	}
	numEntries, err := r.u32()
	if err != nil {
		return nil, err
	}
	for i := uint32(0); i < numEntries; i++ {
		var e manifestEntry
		if e.pageID, err = r.u32(); err != nil {
			return nil, err
		}
		if e.version, err = r.u64(); err != nil {
			return nil, err
		}
		if e.chunkIdx, err = r.u32(); err != nil {
			return nil, err
		}
		p.manifest = append(p.manifest, e)
	}
	tail, err := r.bytes(32)
	if err != nil {
		return nil, fmt.Errorf("missing final chain hash")
	}
	copy(p.finalChain[:], tail)
	if r.off != len(r.buf) {
		return nil, fmt.Errorf("trailing bytes after trailer")
	}
	return p, nil
}

// LocateChunk returns the byte offset and length of the data section of the
// chunkIndex-th chunk (0-based) in a raw checkpoint file. It is an inspection
// utility used by tools and tests.
func LocateChunk(raw []byte, chunkIndex int) (offset, length int, err error) {
	if len(raw) < headerSize || string(raw[:8]) != magic {
		return 0, 0, fmt.Errorf("bad magic")
	}
	numChunks := int(binary.LittleEndian.Uint32(raw[56:60]))
	if chunkIndex < 0 || chunkIndex >= numChunks {
		return 0, 0, fmt.Errorf("chunk index %d out of range [0,%d)", chunkIndex, numChunks)
	}
	off := headerSize
	for i := 0; i < numChunks; i++ {
		if off+4 > len(raw) {
			return 0, 0, fmt.Errorf("truncated at chunk %d", i)
		}
		dataLen := int(binary.LittleEndian.Uint32(raw[off : off+4]))
		dataOff := off + 4 + 32
		if i == chunkIndex {
			if dataOff+dataLen > len(raw) {
				return 0, 0, fmt.Errorf("truncated chunk %d data", i)
			}
			return dataOff, dataLen, nil
		}
		off = dataOff + dataLen
	}
	return 0, 0, fmt.Errorf("unreachable")
}

func writeU16(buf *bytes.Buffer, v uint16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	buf.Write(b[:])
}

func writeU32(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

func writeU64(buf *bytes.Buffer, v uint64) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	buf.Write(b[:])
}

type byteReader struct {
	buf []byte
	off int
}

func (r *byteReader) bytes(n int) ([]byte, error) {
	if n < 0 || r.off+n > len(r.buf) {
		return nil, fmt.Errorf("unexpected end of data at offset %d", r.off)
	}
	b := r.buf[r.off : r.off+n]
	r.off += n
	return b, nil
}

func (r *byteReader) u16() (uint16, error) {
	b, err := r.bytes(2)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(b), nil
}

func (r *byteReader) u32() (uint32, error) {
	b, err := r.bytes(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

func (r *byteReader) u64() (uint64, error) {
	b, err := r.bytes(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b), nil
}

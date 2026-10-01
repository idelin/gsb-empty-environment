// Package blkdict implements an immutable, block-compressed ordered string
// dictionary with random access, prefix range scans, corruption detection,
// batch updates via segment merge, and snapshot isolation.
//
// On-disk / in-memory layout (all integers big-endian):
//
//	Segment := Block{1..N}                // concatenated blocks
//	Block   := magic(4B "BLK1")
//	           entryCount(2B, 1..BlockSize)
//	           firstKeyLen(4B)
//	           firstKey[firstKeyLen]      // restart key, stored in full
//	           entry{2..entryCount}       // prefix-compressed vs previous key
//	           crc32(4B, IEEE, over all preceding block bytes)
//	entry   := prefixLen(uvarint) suffixLen(uvarint) suffix[suffixLen]
//
// A segment also keeps an in-memory block offset table (8B per block on
// 64-bit) so any block can be located and decoded independently: the first
// key of every block is a full restart key, so prefix reconstruction never
// references a previous block.
package blkdict

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// BlockSize is the maximum number of keys per block. A random read decodes
// at most BlockSize entries.
const BlockSize = 16

const (
	blockMagic  = 0x424c4b31 // "BLK1"
	blockHdrLen = 4 + 2 + 4  // magic + entryCount + firstKeyLen
	blockCRCLen = 4
	// maxKeyLen bounds any single key; length fields beyond this are corrupt.
	maxKeyLen = 1 << 20
)

// CorruptError reports a block that failed validation. Decoders return it
// instead of silently misinterpreting damaged data.
type CorruptError struct{ Reason string }

func (e *CorruptError) Error() string { return "blkdict: corrupt block: " + e.Reason }

func corruptf(format string, args ...interface{}) *CorruptError {
	return &CorruptError{Reason: fmt.Sprintf(format, args...)}
}

// EncodeBlock encodes 1..BlockSize sorted, unique keys into one block.
// It panics on empty input; callers (buildSegment) guarantee sortedness.
func EncodeBlock(keys []string) []byte {
	if len(keys) == 0 || len(keys) > BlockSize {
		panic(fmt.Sprintf("blkdict: EncodeBlock with %d keys", len(keys)))
	}
	buf := make([]byte, 0, 64)
	hdr := make([]byte, blockHdrLen)
	binary.BigEndian.PutUint32(hdr[0:4], blockMagic)
	binary.BigEndian.PutUint16(hdr[4:6], uint16(len(keys)))
	binary.BigEndian.PutUint32(hdr[6:10], uint32(len(keys[0])))
	buf = append(buf, hdr...)
	buf = append(buf, keys[0]...)
	prev := keys[0]
	for _, k := range keys[1:] {
		p := commonPrefixLen(prev, k)
		var tmp [2 * binary.MaxVarintLen64]byte
		n := binary.PutUvarint(tmp[:], uint64(p))
		m := binary.PutUvarint(tmp[n:], uint64(len(k)-p))
		buf = append(buf, tmp[:n+m]...)
		buf = append(buf, k[p:]...)
		prev = k
	}
	var crc [blockCRCLen]byte
	binary.BigEndian.PutUint32(crc[:], crc32.ChecksumIEEE(buf))
	return append(buf, crc[:]...)
}

// DecodeBlock fully decodes and validates a block.
func DecodeBlock(block []byte) ([]string, error) {
	return decodeBlock(block, BlockSize)
}

// decodeBlock verifies the block CRC first, then decodes at most limit
// entries. Every length field is bounds-checked BEFORE any allocation, and
// prefixLen may never exceed the previous key of the SAME block, so damaged
// data is rejected up front and can never reference a previous block.
func decodeBlock(block []byte, limit int) ([]string, error) {
	if len(block) < blockHdrLen+blockCRCLen {
		return nil, corruptf("block too small: %d bytes", len(block))
	}
	body := block[:len(block)-blockCRCLen]
	want := binary.BigEndian.Uint32(block[len(block)-blockCRCLen:])
	if got := crc32.ChecksumIEEE(body); got != want {
		return nil, corruptf("crc32 mismatch: got %#08x want %#08x", got, want)
	}
	if magic := binary.BigEndian.Uint32(body[0:4]); magic != blockMagic {
		return nil, corruptf("bad magic %#08x", magic)
	}
	count := int(binary.BigEndian.Uint16(body[4:6]))
	if count == 0 || count > BlockSize {
		return nil, corruptf("entry count %d out of range", count)
	}
	rest := body[blockHdrLen:]
	firstLen := int(binary.BigEndian.Uint32(body[6:10]))
	if firstLen > maxKeyLen || firstLen > len(rest) {
		return nil, corruptf("first key length %d exceeds block remainder %d", firstLen, len(rest))
	}
	keys := make([]string, 0, count)
	keys = append(keys, string(rest[:firstLen]))
	rest = rest[firstLen:]
	prev := keys[0]
	for len(keys) < count {
		if len(keys) >= limit { // random access: decoded far enough
			return keys, nil
		}
		p, n := binary.Uvarint(rest)
		// Reject BEFORE allocating: the shared prefix cannot be longer
		// than the previous key of this block. prev is always an
		// in-block key, so no cross-block reference is possible.
		if n <= 0 || p > uint64(len(prev)) {
			return nil, corruptf("prefix length %d invalid (previous key is %d bytes)", p, len(prev))
		}
		rest = rest[n:]
		s, m := binary.Uvarint(rest)
		if m <= 0 || s > maxKeyLen || s > uint64(len(rest)-m) {
			return nil, corruptf("suffix length %d exceeds block remainder", s)
		}
		rest = rest[m:]
		// Both lengths validated; p+s <= 2*maxKeyLen, safe to allocate.
		key := make([]byte, int(p)+int(s))
		copy(key, prev[:p])
		copy(key[p:], rest[:s])
		rest = rest[s:]
		keys = append(keys, string(key))
		prev = keys[len(keys)-1]
	}
	if len(rest) != 0 {
		return nil, corruptf("%d trailing bytes after %d entries", len(rest), count)
	}
	return keys, nil
}

// blockFirstKey decodes only the restart key of a block (still CRC-checked).
func blockFirstKey(block []byte) (string, error) {
	keys, err := decodeBlock(block, 1)
	if err != nil {
		return "", err
	}
	return keys[0], nil
}

func commonPrefixLen(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

// prefixSuccessor returns the smallest byte string strictly greater than
// every string having prefix p, and false if no such bound exists (p empty
// or all 0xff). Prefixes are byte-oriented; a prefix may split a UTF-8
// sequence, but decoded output is always a complete stored string.
func prefixSuccessor(p string) (string, bool) {
	b := []byte(p)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] != 0xff {
			b[i]++
			return string(b[:i+1]), true
		}
	}
	return "", false
}

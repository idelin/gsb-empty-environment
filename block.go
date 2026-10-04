// Package blkidx implements an authenticated block index: sorted entries are
// packed into prefix-compressed leaf blocks, internal summary nodes bind child
// order, key ranges and counts, and a single root hash commits to the whole
// structure. Local proofs let an independent verifier check membership,
// rank/select aggregates and range-query completeness without loading the
// full index.
package blkidx

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// Leaf block binary layout (all integers little-endian):
//
//	header, fixed LeafHeaderSize = 40 bytes:
//	  [0:2]   magic u16 = LeafMagic
//	  [2:6]   version u32
//	  [6:10]  blockID u32
//	  [10:14] count u32
//	  [14:22] baseOff u64   (global ordinal of the first entry)
//	  [22:24] restartInterval u16
//	  [24:26] numRestarts u16
//	  [26:30] entriesLen u32 (bytes of the entries region)
//	  [30:34] crc32 u32 (IEEE, over header bytes [0:30])
//	  [34:36] flags u16
//	  [36:40] reserved u32
//	entries region, entriesLen bytes, per entry:
//	  sharedLen   uvarint (shared key prefix with previous entry, 0 at restart)
//	  unsharedLen uvarint
//	  keyDelta    [unsharedLen]byte
//	  valueLen    uvarint
//	  value       [valueLen]byte
//	restart array, numRestarts * 4 bytes: u32 offsets into the entries region.
const (
	LeafMagic       uint16 = 0x4B42
	LeafHeaderSize         = 40
	RestartInterval        = 8
)

// Entry is a single key/value pair. Keys are unique and sorted.
type Entry struct {
	Key   string
	Value []byte
}

// LeafHeader is the decoded fixed-size block header.
type LeafHeader struct {
	Version uint32
	BlockID uint32
	Count   uint32
	BaseOff uint64
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

func appendUvarint(dst []byte, v uint64) []byte {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	return append(dst, tmp[:n]...)
}

// encodeLeaf serializes one leaf block using the layout documented above.
func encodeLeaf(version, blockID uint32, baseOff uint64, entries []Entry) []byte {
	var region []byte
	var restarts []uint32
	prev := ""
	for i, e := range entries {
		shared := 0
		if i%RestartInterval == 0 {
			restarts = append(restarts, uint32(len(region)))
		} else {
			shared = commonPrefixLen(prev, e.Key)
		}
		region = appendUvarint(region, uint64(shared))
		region = appendUvarint(region, uint64(len(e.Key)-shared))
		region = append(region, e.Key[shared:]...)
		region = appendUvarint(region, uint64(len(e.Value)))
		region = append(region, e.Value...)
		prev = e.Key
	}

	hdr := make([]byte, LeafHeaderSize)
	binary.LittleEndian.PutUint16(hdr[0:2], LeafMagic)
	binary.LittleEndian.PutUint32(hdr[2:6], version)
	binary.LittleEndian.PutUint32(hdr[6:10], blockID)
	binary.LittleEndian.PutUint32(hdr[10:14], uint32(len(entries)))
	binary.LittleEndian.PutUint64(hdr[14:22], baseOff)
	binary.LittleEndian.PutUint16(hdr[22:24], RestartInterval)
	binary.LittleEndian.PutUint16(hdr[24:26], uint16(len(restarts)))
	binary.LittleEndian.PutUint32(hdr[26:30], uint32(len(region)))
	binary.LittleEndian.PutUint32(hdr[30:34], crc32.ChecksumIEEE(hdr[0:30]))
	// [34:36] flags, [36:40] reserved: zero.

	out := make([]byte, 0, LeafHeaderSize+len(region)+4*len(restarts))
	out = append(out, hdr...)
	out = append(out, region...)
	var tmp [4]byte
	for _, off := range restarts {
		binary.LittleEndian.PutUint32(tmp[:], off)
		out = append(out, tmp[:]...)
	}
	return out
}

// decodeLeaf parses and fully validates an encoded leaf block.
func decodeLeaf(block []byte) (LeafHeader, []Entry, error) {
	var hdr LeafHeader
	if len(block) < LeafHeaderSize {
		return hdr, nil, fmt.Errorf("block too short: %d bytes", len(block))
	}
	if binary.LittleEndian.Uint16(block[0:2]) != LeafMagic {
		return hdr, nil, fmt.Errorf("bad magic 0x%04x", binary.LittleEndian.Uint16(block[0:2]))
	}
	wantCRC := binary.LittleEndian.Uint32(block[30:34])
	if got := crc32.ChecksumIEEE(block[0:30]); got != wantCRC {
		return hdr, nil, fmt.Errorf("header crc mismatch: got 0x%08x want 0x%08x", got, wantCRC)
	}
	hdr.Version = binary.LittleEndian.Uint32(block[2:6])
	hdr.BlockID = binary.LittleEndian.Uint32(block[6:10])
	hdr.Count = binary.LittleEndian.Uint32(block[10:14])
	hdr.BaseOff = binary.LittleEndian.Uint64(block[14:22])
	restartInterval := int(binary.LittleEndian.Uint16(block[22:24]))
	numRestarts := int(binary.LittleEndian.Uint16(block[24:26]))
	entriesLen := int(binary.LittleEndian.Uint32(block[26:30]))
	if restartInterval != RestartInterval {
		return hdr, nil, fmt.Errorf("unsupported restart interval %d", restartInterval)
	}
	if len(block) != LeafHeaderSize+entriesLen+4*numRestarts {
		return hdr, nil, fmt.Errorf("block length %d does not match header (%d + %d + %d)",
			len(block), LeafHeaderSize, entriesLen, 4*numRestarts)
	}
	if hdr.Count == 0 {
		return hdr, nil, fmt.Errorf("empty block")
	}
	wantRestarts := (int(hdr.Count) + RestartInterval - 1) / RestartInterval
	if numRestarts != wantRestarts {
		return hdr, nil, fmt.Errorf("restart count %d, want %d for %d entries", numRestarts, wantRestarts, hdr.Count)
	}

	region := block[LeafHeaderSize : LeafHeaderSize+entriesLen]
	restartBytes := block[LeafHeaderSize+entriesLen:]
	restarts := make([]uint32, numRestarts)
	for i := range restarts {
		restarts[i] = binary.LittleEndian.Uint32(restartBytes[4*i : 4*i+4])
		if i > 0 && restarts[i] <= restarts[i-1] {
			return hdr, nil, fmt.Errorf("restart offsets not increasing")
		}
		if int(restarts[i]) >= entriesLen {
			return hdr, nil, fmt.Errorf("restart offset %d out of region", restarts[i])
		}
	}
	if numRestarts > 0 && restarts[0] != 0 {
		return hdr, nil, fmt.Errorf("first restart offset must be 0")
	}

	entries := make([]Entry, 0, hdr.Count)
	prev := ""
	pos := 0
	for i := 0; i < int(hdr.Count); i++ {
		if i%RestartInterval == 0 {
			if int(restarts[i/RestartInterval]) != pos {
				return hdr, nil, fmt.Errorf("restart %d points to %d, entry %d starts at %d",
					i/RestartInterval, restarts[i/RestartInterval], i, pos)
			}
		}
		shared, n := binary.Uvarint(region[pos:])
		if n <= 0 {
			return hdr, nil, fmt.Errorf("entry %d: bad shared varint", i)
		}
		pos += n
		unshared, n := binary.Uvarint(region[pos:])
		if n <= 0 {
			return hdr, nil, fmt.Errorf("entry %d: bad unshared varint", i)
		}
		pos += n
		if int(shared) > len(prev) {
			return hdr, nil, fmt.Errorf("entry %d: shared prefix %d exceeds previous key", i, shared)
		}
		if pos+int(unshared) > len(region) {
			return hdr, nil, fmt.Errorf("entry %d: key delta overruns region", i)
		}
		key := prev[:shared] + string(region[pos:pos+int(unshared)])
		pos += int(unshared)
		vlen, n := binary.Uvarint(region[pos:])
		if n <= 0 {
			return hdr, nil, fmt.Errorf("entry %d: bad value-length varint", i)
		}
		pos += n
		if pos+int(vlen) > len(region) {
			return hdr, nil, fmt.Errorf("entry %d: value overruns region", i)
		}
		val := make([]byte, vlen)
		copy(val, region[pos:pos+int(vlen)])
		pos += int(vlen)
		if i > 0 && key <= prev {
			return hdr, nil, fmt.Errorf("entry %d: keys not strictly increasing (%q after %q)", i, key, prev)
		}
		if i%RestartInterval == 0 && shared != 0 {
			return hdr, nil, fmt.Errorf("entry %d: restart entry must have zero shared prefix", i)
		}
		entries = append(entries, Entry{Key: key, Value: val})
		prev = key
	}
	if pos != len(region) {
		return hdr, nil, fmt.Errorf("entries region has %d trailing bytes", len(region)-pos)
	}
	return hdr, entries, nil
}

// leafHash commits to the full encoded block, header included.
func leafHash(block []byte) [32]byte {
	return sha256.Sum256(append([]byte("BLK1"), block...))
}

// lowerBound returns the smallest i with entries[i].Key >= key.
func lowerBound(entries []Entry, key string) int {
	lo, hi := 0, len(entries)
	for lo < hi {
		mid := (lo + hi) / 2
		if entries[mid].Key < key {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func equalBytes(a, b []byte) bool { return bytes.Equal(a, b) }

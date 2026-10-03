package sparse

import "encoding/binary"

// Packed stream format (all integers varint-encoded):
//
//	uvarint(count)
//	repeated count times:
//	  uvarint(keyDelta)   // key[i] - key[i-1], first delta is key[0]
//	  varint(value)       // zig-zag encoded signed value
//
// Keys are stored sorted, so deltas are non-negative and typically tiny
// for clustered sparse data, giving a very compact footprint.

func appendUvarint(dst []byte, x uint64) []byte {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], x)
	return append(dst, tmp[:n]...)
}

func appendVarint(dst []byte, x int64) []byte {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutVarint(tmp[:], x)
	return append(dst, tmp[:n]...)
}

// compress encodes sorted keys and their values into a packed stream.
func compress(keys []uint64, vals []int64) []byte {
	out := make([]byte, 0, len(keys)*4+8)
	out = appendUvarint(out, uint64(len(keys)))
	prev := uint64(0)
	for i := range keys {
		out = appendUvarint(out, keys[i]-prev)
		prev = keys[i]
		out = appendVarint(out, vals[i])
	}
	return out
}

// decompress restores the sorted key/value arrays from a packed stream.
func decompress(packed []byte) (keys []uint64, vals []int64) {
	n, m := binary.Uvarint(packed)
	if m <= 0 {
		return nil, nil
	}
	packed = packed[m:]
	keys = make([]uint64, 0, n)
	vals = make([]int64, 0, n)
	prev := uint64(0)
	for i := uint64(0); i < n; i++ {
		delta, m := binary.Uvarint(packed)
		if m <= 0 {
			break
		}
		packed = packed[m:]
		key := prev + delta
		prev = key
		val, m := binary.Varint(packed)
		if m <= 0 {
			break
		}
		packed = packed[m:]
		keys = append(keys, key)
		vals = append(vals, val)
	}
	return keys, vals
}

// packedCount reads the entry count without decoding the stream.
func packedCount(packed []byte) (int, int) {
	n, m := binary.Uvarint(packed)
	if m <= 0 {
		return 0, 0
	}
	return int(n), m
}

// packedGet scans the packed stream for key i (stops early once past it).
func packedGet(packed []byte, i uint64) (int64, bool) {
	n, m := packedCount(packed)
	if m == 0 {
		return 0, false
	}
	packed = packed[m:]
	prev := uint64(0)
	for k := 0; k < n; k++ {
		delta, s := binary.Uvarint(packed)
		if s <= 0 {
			return 0, false
		}
		packed = packed[s:]
		key := prev + delta
		prev = key
		val, s := binary.Varint(packed)
		if s <= 0 {
			return 0, false
		}
		packed = packed[s:]
		if key == i {
			return val, true
		}
		if key > i {
			return 0, false
		}
	}
	return 0, false
}

// packedSpan returns the first and last key in the stream.
func packedSpan(packed []byte) (lo, hi uint64, ok bool) {
	n, m := packedCount(packed)
	if m == 0 || n == 0 {
		return 0, 0, false
	}
	packed = packed[m:]
	prev := uint64(0)
	for k := 0; k < n; k++ {
		delta, s := binary.Uvarint(packed)
		if s <= 0 {
			break
		}
		packed = packed[s:]
		prev += delta
		if k == 0 {
			lo = prev
		}
		if _, s := binary.Varint(packed); s > 0 {
			packed = packed[s:]
		}
	}
	return lo, prev, true
}

// packedForEach walks the stream in ascending key order.
func packedForEach(packed []byte, fn func(k uint64, v int64) bool) {
	n, m := packedCount(packed)
	if m == 0 {
		return
	}
	packed = packed[m:]
	prev := uint64(0)
	for k := 0; k < n; k++ {
		delta, s := binary.Uvarint(packed)
		if s <= 0 {
			return
		}
		packed = packed[s:]
		prev += delta
		val, s := binary.Varint(packed)
		if s <= 0 {
			return
		}
		packed = packed[s:]
		if !fn(prev, val) {
			return
		}
	}
}

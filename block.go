package blockindex

import "errors"

// Entry is a single key/value pair. Keys are uint64 and unique within the
// index; values are opaque byte strings.
type Entry struct {
	Key   uint64
	Value []byte
}

// EncodeBlock serializes entries into the block wire format:
//
//	"BLK1" | version | count | firstKey | count * { keyDelta, valueLen, value }
//
// Keys must be strictly increasing. The first entry's keyDelta is 0 and its
// key equals firstKey; every later key is previous + keyDelta (keyDelta > 0).
func EncodeBlock(version uint64, entries []Entry) []byte {
	buf := append([]byte{}, blockMagic...)
	buf = appendUvarint(buf, version)
	buf = appendUvarint(buf, uint64(len(entries)))
	var first uint64
	if len(entries) > 0 {
		first = entries[0].Key
	}
	buf = appendUvarint(buf, first)
	prev := first
	for i, e := range entries {
		var delta uint64
		if i > 0 {
			delta = e.Key - prev
		}
		buf = appendUvarint(buf, delta)
		buf = appendUvarint(buf, uint64(len(e.Value)))
		buf = append(buf, e.Value...)
		prev = e.Key
	}
	return buf
}

// DecodeBlock parses a serialized block, validating magic, key ordering,
// count and exact length. It returns the format version and the entries.
func DecodeBlock(raw []byte) (uint64, []Entry, error) {
	d := &decoder{buf: raw}
	d.magic(blockMagic)
	version := d.uvarint()
	count := d.uvarint()
	first := d.uvarint()
	if d.err != nil {
		return 0, nil, d.err
	}
	// Each entry needs at least 2 bytes (delta + length), so count cannot
	// exceed what the remaining buffer could possibly hold.
	if count > uint64(len(d.buf))/2+1 {
		return 0, nil, errors.New("blockindex: entry count exceeds buffer")
	}
	entries := make([]Entry, 0, count)
	prev := first
	for i := uint64(0); i < count; i++ {
		delta := d.uvarint()
		vlen := d.uvarint()
		if d.err != nil {
			return 0, nil, d.err
		}
		if vlen > uint64(len(d.buf)) {
			return 0, nil, errors.New("blockindex: value length exceeds buffer")
		}
		val := append([]byte(nil), d.fixed(int(vlen))...)
		if d.err != nil {
			return 0, nil, d.err
		}
		key := first
		if i == 0 {
			if delta != 0 {
				return 0, nil, errors.New("blockindex: first key delta must be 0")
			}
		} else {
			if delta == 0 {
				return 0, nil, errors.New("blockindex: keys not strictly increasing")
			}
			if prev+delta < prev {
				return 0, nil, errors.New("blockindex: key overflow")
			}
			key = prev + delta
		}
		entries = append(entries, Entry{Key: key, Value: val})
		prev = key
	}
	if err := d.done(); err != nil {
		return 0, nil, err
	}
	return version, entries, nil
}

// BlockDigest commits to the exact serialized block bytes.
func BlockDigest(raw []byte) [DigestLen]byte { return digestOf(raw) }

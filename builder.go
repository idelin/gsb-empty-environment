package compactidx

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// KV is one key/value pair to encode.
type KV struct {
	Key   string
	Value string
}

// Build serializes kvs into the compact index byte image. Keys are sorted
// and must be unique and non-empty. Values longer than chunkSize bytes are
// split across continuation records linked through the offset graph;
// chunkSize <= 0 disables chunking.
func Build(kvs []KV, chunkSize int) ([]byte, error) {
	sorted := make([]KV, len(kvs))
	copy(sorted, kvs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })
	for i, kv := range sorted {
		if kv.Key == "" {
			return nil, errors.New("compactidx: empty keys cannot be encoded")
		}
		if i > 0 && sorted[i-1].Key == kv.Key {
			return nil, fmt.Errorf("compactidx: duplicate key %q", kv.Key)
		}
	}

	type chunk struct {
		key, val string
		linked   bool
	}
	var chunks []chunk
	headIndex := make([]int, 0, len(sorted))
	for _, kv := range sorted {
		headIndex = append(headIndex, len(chunks))
		val := kv.Value
		if chunkSize <= 0 || len(val) <= chunkSize {
			chunks = append(chunks, chunk{key: kv.Key, val: val})
			continue
		}
		first := true
		for len(val) > 0 {
			n := chunkSize
			if n > len(val) {
				n = len(val)
			}
			c := chunk{val: val[:n], linked: n < len(val)}
			if first {
				c.key = kv.Key
				first = false
			}
			chunks = append(chunks, c)
			val = val[n:]
		}
	}

	count := uint64(len(sorted))
	tableBytes, err := mul64(count, tableEntrySize)
	if err != nil {
		return nil, err
	}
	tableOff := uint64(HeaderLen)
	dataOff, err := add64(tableOff, tableBytes)
	if err != nil {
		return nil, err
	}

	// Record sizes depend only on key/value lengths and the link tag, so all
	// offsets can be computed before encoding.
	sizes := make([]uint64, len(chunks))
	for i, c := range chunks {
		sz := uint64(uvarintLen(uint64(len(c.key))) + len(c.key) +
			uvarintLen(uint64(len(c.val))) + len(c.val) + 1)
		if c.linked {
			sz += 8
		}
		sizes[i] = sz
	}
	offsets := make([]uint64, len(chunks))
	pos := dataOff
	for i := range chunks {
		offsets[i] = pos
		pos, err = add64(pos, sizes[i])
		if err != nil {
			return nil, err
		}
	}
	dataLen := pos - dataOff

	total, err := add64(dataOff, dataLen)
	if err != nil {
		return nil, err
	}
	out := make([]byte, total)
	hdr := header{
		entryCount: uint32(count),
		tableOff:   tableOff,
		dataOff:    dataOff,
		dataLen:    dataLen,
	}
	hdr.encode(out)
	for i, hi := range headIndex {
		binary.LittleEndian.PutUint64(out[tableOff+uint64(i)*tableEntrySize:], offsets[hi])
	}
	for i, c := range chunks {
		var link uint64
		if c.linked {
			link = offsets[i+1]
		}
		appendRecord(out, offsets[i], c.key, c.val, link)
	}
	return out, nil
}

func uvarintLen(v uint64) int {
	return len(binary.AppendUvarint(nil, v))
}

// appendRecord writes one record at offset off; buf must be large enough.
func appendRecord(buf []byte, off uint64, key, val string, link uint64) {
	tmp := binary.AppendUvarint(nil, uint64(len(key)))
	n := copy(buf[off:], tmp)
	off += uint64(n)
	n = copy(buf[off:], key)
	off += uint64(n)
	tmp = binary.AppendUvarint(nil, uint64(len(val)))
	n = copy(buf[off:], tmp)
	off += uint64(n)
	n = copy(buf[off:], val)
	off += uint64(n)
	if link == 0 {
		buf[off] = linkNone
		return
	}
	buf[off] = linkMore
	binary.LittleEndian.PutUint64(buf[off+1:], link)
}

package main

import (
	"encoding/hex"
	"encoding/json"
)

type memoEntry struct {
	ok     bool
	labels [][]byte
	// nextField is the offset of the byte immediately following the encoded
	// name as it appears at this offset. For a pointer-encoded name it is the
	// offset after the two-byte pointer, never an offset inside the target.
	nextField int
}

type pathItem struct {
	offset  int // packet offset where this encoded segment starts
	labels  [][]byte
	wireLen int // sum of (1 + len(label)) for this segment's own labels
	// endOffset is the byte just past this encoded segment: pointer+2 for a
	// pointer hop; set when a plain-label segment reaches its terminator.
	endOffset int
}

type decoder struct {
	packet []byte
	memo   map[int]*memoEntry
}

// decodeName resolves the compressed (or plain) DNS name at offset start.
// Compression pointers may jump forward and take multiple hops. Every packet
// offset is resolved at most once: shared suffixes reuse the memoized result,
// so a long pointer chain is walked once rather than once per starting offset.
func (d *decoder) decodeName(start int) *memoEntry {
	if start < 0 || start >= len(d.packet) {
		return &memoEntry{ok: false}
	}
	if e, ok := d.memo[start]; ok {
		return e
	}

	path := make([]pathItem, 0, 8)
	visiting := map[int]bool{}
	openSegment := func(off int) {
		visiting[off] = true
		path = append(path, pathItem{offset: off})
	}
	openSegment(start)

	offset := start
	for {
		if e, ok := d.memo[offset]; ok {
			if !e.ok {
				d.failPath(path)
				return d.memo[start]
			}
			// Already-resolved shared suffix: merge into open segments.
			return d.mergeSuffix(path, e.labels, start)
		}

		if offset >= len(d.packet) {
			d.failPath(path)
			return d.memo[start]
		}

		b := d.packet[offset]
		switch {
		case b == 0:
			// Root label terminates the final plain segment.
			path[len(path)-1].endOffset = offset + 1
			return d.mergeSuffix(path, [][]byte{}, start)

		case b&0xc0 == 0xc0:
			// Compression pointer (11 prefix).
			if offset+1 >= len(d.packet) {
				d.failPath(path)
				return d.memo[start]
			}
			target := int(b&0x3f)<<8 | int(d.packet[offset+1])
			// The segment starting at this path entry ends after the two pointer
			// bytes; jumping to the target does not move that boundary.
			path[len(path)-1].endOffset = offset + 2
			if target >= len(d.packet) {
				d.failPath(path)
				return d.memo[start]
			}
			if visiting[target] {
				d.failPath(path)
				return d.memo[start]
			}
			if e, seen := d.memo[target]; seen {
				if !e.ok {
					d.failPath(path)
					return d.memo[start]
				}
				return d.mergeSuffix(path, e.labels, start)
			}
			openSegment(target)
			offset = target

		case b&0xc0 != 0:
			// 10 (RFC 1035 reserved) and 01 prefixes are not legal label types.
			d.failPath(path)
			return d.memo[start]

		default:
			// Ordinary length-prefixed label.
			labelLen := int(b)
			// The label body spans offset+1 .. offset+labelLen; that last index
			// must be strictly inside the packet.
			if offset+labelLen >= len(d.packet) {
				d.failPath(path)
				return d.memo[start]
			}
			cur := &path[len(path)-1]
			cur.labels = append(cur.labels, copyBytes(d.packet[offset+1:offset+1+labelLen]))
			cur.wireLen += 1 + labelLen
			offset += 1 + labelLen
		}
	}
}

// finishSegment closes one encoded segment by prepending its labels to the
// already-resolved suffix. It records the merged result in the memo and
// returns the full labels and wire-form length for callers further up.
func (d *decoder) finishSegment(seg pathItem, suffix [][]byte, suffixWireLen int) ([][]byte, int) {
	labels := append(copySegments(seg.labels), suffix...)
	wireLen := seg.wireLen + suffixWireLen
	entry, ok := d.memo[seg.offset]
	if !ok {
		entry = &memoEntry{}
		d.memo[seg.offset] = entry
	}
	entry.nextField = seg.endOffset
	entry.ok = wireLen <= 255
	if entry.ok {
		entry.labels = labels
	}
	return labels, wireLen
}

func (d *decoder) failPath(path []pathItem) {
	for _, p := range path {
		d.memo[p.offset] = &memoEntry{ok: false}
	}
}

// mergeSuffix closes every open segment back to front, prepending each
// segment's labels to the already-resolved suffix. A nil suffix represents the
// root label (one wire octet); otherwise the suffix wire length includes its
// own root octet already.
func (d *decoder) mergeSuffix(path []pathItem, suffix [][]byte, start int) *memoEntry {
	labels := suffix
	wireLen := encodedNameLen(suffix)
	if suffix == nil {
		labels = [][]byte{}
		wireLen = 1
	}
	for i := len(path) - 1; i >= 0; i-- {
		labels, wireLen = d.finishSegment(path[i], labels, wireLen)
	}
	return d.memo[start]
}

func copySegments(labels [][]byte) [][]byte {
	out := make([][]byte, len(labels))
	for i, label := range labels {
		cp := make([]byte, len(label))
		copy(cp, label)
		out[i] = cp
	}
	return out
}

func copyBytes(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// encodedNameLen returns the full wire-form length of the name: one length
// octet per label, the label bytes, and the trailing root label octet.
func encodedNameLen(labels [][]byte) int {
	n := 1
	for _, label := range labels {
		n += 1 + len(label)
	}
	return n
}

// tuple is one output element: [label hex array, next-field offset].
type tuple struct {
	labels    []string
	nextField int
}

func (t tuple) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{t.labels, t.nextField})
}

// decodePacket decodes one captured packet. Each element of at produces one
// element in the returned slice. A valid element is the tuple
// [label hex array, next-field offset]; invalid offsets yield null.
func decodePacket(packet []byte, at []int) []any {
	d := &decoder{packet: packet, memo: make(map[int]*memoEntry)}
	results := make([]any, len(at))
	for i, offset := range at {
		e := d.decodeName(offset)
		if !e.ok {
			continue
		}
		hexLabels := make([]string, len(e.labels))
		for j, label := range e.labels {
			hexLabels[j] = hex.EncodeToString(label)
		}
		results[i] = tuple{labels: hexLabels, nextField: e.nextField}
	}
	return results
}

package blockindex

import "errors"

// ChildInfo is the per-child summary stored inside a summary node. It binds
// the child's key range, its aggregate entry count, its aggregate leaf-block
// count and the child content digest. Because the node digest covers the
// serialized child list, child order is committed as well.
type ChildInfo struct {
	MinKey  uint64
	MaxKey  uint64
	Entries uint64 // total entries in the child subtree
	Blocks  uint64 // total leaf blocks in the child subtree
	Digest  [DigestLen]byte
}

// EncodeNode serializes a summary node:
//
//	"SUM1" | version | level | nChild |
//	nChild * { minKeyDelta, maxKeyDelta, entries, blocks, digest[32] }
//
// minKeyDelta is absolute for child 0 and a positive delta from the previous
// child's MinKey afterwards; maxKeyDelta is MaxKey - MinKey of the same
// child. Children must have strictly increasing, non-overlapping key ranges.
func EncodeNode(version, level uint64, children []ChildInfo) []byte {
	buf := append([]byte{}, nodeMagic...)
	buf = appendUvarint(buf, version)
	buf = appendUvarint(buf, level)
	buf = appendUvarint(buf, uint64(len(children)))
	var prevMin uint64
	for i, c := range children {
		minDelta := c.MinKey
		if i > 0 {
			minDelta = c.MinKey - prevMin
		}
		buf = appendUvarint(buf, minDelta)
		buf = appendUvarint(buf, c.MaxKey-c.MinKey)
		buf = appendUvarint(buf, c.Entries)
		buf = appendUvarint(buf, c.Blocks)
		buf = append(buf, c.Digest[:]...)
		prevMin = c.MinKey
	}
	return buf
}

// DecodeNode parses a summary node and enforces structural invariants:
// at least one child, strictly increasing non-overlapping ranges, and
// non-zero aggregate counts.
func DecodeNode(raw []byte) (version, level uint64, children []ChildInfo, err error) {
	d := &decoder{buf: raw}
	d.magic(nodeMagic)
	version = d.uvarint()
	level = d.uvarint()
	n := d.uvarint()
	if d.err != nil {
		return 0, 0, nil, d.err
	}
	if n == 0 {
		return 0, 0, nil, errors.New("blockindex: node with no children")
	}
	// Each child occupies at least 4 varint bytes + a digest.
	if n > uint64(len(d.buf))/(4+DigestLen)+1 {
		return 0, 0, nil, errors.New("blockindex: child count exceeds buffer")
	}
	children = make([]ChildInfo, 0, n)
	var prevMin, prevMax uint64
	for i := uint64(0); i < n; i++ {
		minDelta := d.uvarint()
		maxDelta := d.uvarint()
		entries := d.uvarint()
		blocks := d.uvarint()
		dg := d.fixed(DigestLen)
		if d.err != nil {
			return 0, 0, nil, d.err
		}
		var c ChildInfo
		if i == 0 {
			c.MinKey = minDelta
		} else {
			if minDelta == 0 {
				return 0, 0, nil, errors.New("blockindex: child min keys not increasing")
			}
			if prevMin+minDelta < prevMin {
				return 0, 0, nil, errors.New("blockindex: key overflow")
			}
			c.MinKey = prevMin + minDelta
		}
		if c.MinKey+maxDelta < c.MinKey {
			return 0, 0, nil, errors.New("blockindex: key overflow")
		}
		c.MaxKey = c.MinKey + maxDelta
		if i > 0 && c.MinKey <= prevMax {
			return 0, 0, nil, errors.New("blockindex: child ranges overlap")
		}
		if entries == 0 || blocks == 0 {
			return 0, 0, nil, errors.New("blockindex: child with zero count")
		}
		c.Entries = entries
		c.Blocks = blocks
		copy(c.Digest[:], dg)
		children = append(children, c)
		prevMin, prevMax = c.MinKey, c.MaxKey
	}
	if err := d.done(); err != nil {
		return 0, 0, nil, err
	}
	return version, level, children, nil
}

// NodeDigest commits to the exact serialized node bytes.
func NodeDigest(raw []byte) [DigestLen]byte { return digestOf(raw) }

// checkChildrenOrder re-validates the invariants DecodeNode enforces, for
// child lists that arrive already decoded inside a proof.
func checkChildrenOrder(children []ChildInfo) error {
	if len(children) == 0 {
		return errors.New("blockindex: node with no children")
	}
	for i, c := range children {
		if c.MinKey > c.MaxKey {
			return errors.New("blockindex: child min key above max key")
		}
		if c.Entries == 0 || c.Blocks == 0 {
			return errors.New("blockindex: child with zero count")
		}
		if i > 0 && c.MinKey <= children[i-1].MaxKey {
			return errors.New("blockindex: child ranges overlap or unordered")
		}
	}
	return nil
}

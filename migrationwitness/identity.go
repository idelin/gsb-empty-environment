package migrationwitness

import (
	"crypto/sha256"
	"encoding/binary"
)

// NewAlgo is the collision-resistant algorithm used for migrated identities.
const NewAlgo = "sha256"

// NewID is a complete migrated identity. It binds node kind, order, full
// length and the new digests of the children (the blob case binds the payload
// directly). Equal structural content always yields an equal NewID, even when
// the content was reached through different node names or tree shapes.
type NewID struct {
	Algo string
	Sum  []byte
}

// Equal reports whether two new identities are the same.
func (n NewID) Equal(other NewID) bool {
	return n.Algo == other.Algo && bytesEqual(n.Sum, other.Sum)
}

// String renders the identity as algorithm:hex.
func (n NewID) String() string {
	return n.Algo + ":" + hexEncode(n.Sum)
}

type canonic struct{ buf []byte }

func (c *canonic) tag(b byte)       { c.buf = append(c.buf, b) }
func (c *canonic) uvarint(v uint64) { c.buf = binary.AppendUvarint(c.buf, v) }
func (c *canonic) int64(v int64)    { c.uvarint(uint64(v)) }
func (c *canonic) str(s string)     { c.uvarint(uint64(len(s))); c.buf = append(c.buf, s...) }
func (c *canonic) raw(b []byte)     { c.uvarint(uint64(len(b))); c.buf = append(c.buf, b...) }

func (c *canonic) ref(r Ref) {
	c.str(string(r.Digest))
	c.int64(r.Length)
}

func (c *canonic) newID(id NewID) {
	c.str(id.Algo)
	c.raw(id.Sum)
}

// canonicalize produces the deterministic, domain-separated byte encoding that
// is signed and hashed. Every signed body type has a unique leading tag.
func canonicalize(body any) []byte {
	c := &canonic{}
	switch v := body.(type) {
	case BlobStatement:
		c.tag('B')
		c.str(string(v.Node))
		c.ref(v.Ref)
		c.raw(v.Payload)
	case ConcatStatement:
		c.tag('C')
		c.str(string(v.Node))
		c.ref(v.Ref)
		c.uvarint(uint64(len(v.Edges)))
		for _, e := range v.Edges {
			c.ref(e)
		}
	case AliasStatement:
		c.tag('A')
		c.ref(v.From)
		c.ref(v.To)
	case DisambiguationReceipt:
		c.tag('R')
		c.str(string(v.ID))
		c.str(string(v.Manifest))
		c.uvarint(uint64(v.Edge))
		c.str(string(v.Candidate))
	case ClosureReceipt:
		c.tag('K')
		c.str(v.Scope)
	case WithdrawalStatement:
		c.tag('W')
		c.str(string(v.Receipt))
	default:
		panic("migrationwitness: unknown signed body type")
	}
	return c.buf
}

// ComputeBlobID derives the new identity of a raw block. It binds the blob
// kind, the full length and the payload.
func ComputeBlobID(payload []byte) NewID {
	c := &canonic{}
	c.tag('b')
	c.int64(int64(len(payload)))
	c.raw(payload)
	sum := sha256.Sum256(c.buf)
	return NewID{Algo: NewAlgo, Sum: sum[:]}
}

// ComputeConcatID derives the new identity of an ordered concatenation. It
// binds the concat kind, the full length, the child count/order and each
// child's new digest.
func ComputeConcatID(fullLength int64, children []NewID) NewID {
	c := &canonic{}
	c.tag('c')
	c.int64(fullLength)
	c.uvarint(uint64(len(children)))
	for _, child := range children {
		c.newID(child)
	}
	sum := sha256.Sum256(c.buf)
	return NewID{Algo: NewAlgo, Sum: sum[:]}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

const hexDigits = "0123456789abcdef"

func hexEncode(b []byte) string {
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexDigits[v>>4]
		out[i*2+1] = hexDigits[v&0x0f]
	}
	return string(out)
}

package capacitycred

import (
	"context"
	"encoding/binary"
)

// canonicalBuilder produces an unambiguous, length-prefixed encoding. Length
// prefixes make every field boundary explicit so concatenations cannot collide.
type canonicalBuilder struct {
	buf []byte
}

func (b *canonicalBuilder) tag(t byte) *canonicalBuilder {
	b.buf = append(b.buf, t)
	return b
}

func (b *canonicalBuilder) u64(v uint64) *canonicalBuilder {
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], v)
	b.buf = append(b.buf, tmp[:]...)
	return b
}

func (b *canonicalBuilder) u32(v uint32) *canonicalBuilder {
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], v)
	b.buf = append(b.buf, tmp[:]...)
	return b
}

func (b *canonicalBuilder) bytes(v []byte) *canonicalBuilder {
	b.u64(uint64(len(v)))
	b.buf = append(b.buf, v...)
	return b
}

func (b *canonicalBuilder) str(v string) *canonicalBuilder {
	b.bytes([]byte(v))
	return b
}

func (b *canonicalBuilder) id(v string) *canonicalBuilder {
	b.str(v)
	return b
}

// eventBody is the exact byte stream covered by the signature and by the
// event id digest. EventID and Signature are deliberately excluded.
func eventBody(e Event) []byte {
	b := new(canonicalBuilder)
	b.tag(1).str(string(e.Kind))
	b.tag(2).id(string(e.Tenant))
	b.tag(3).id(string(e.CredID))
	b.tag(4).id(string(e.ParentID))
	b.tag(5).id(string(e.From))
	b.tag(6).id(string(e.To))
	b.tag(7).u64(e.Range.Low).u64(e.Range.High)
	b.tag(8).u64(e.Total)
	b.tag(9).bytes(e.ParamsCommitment)
	b.tag(10).u64(uint64(len(e.Children)))
	for _, child := range e.Children {
		b.id(string(child.ID)).u64(child.Range.Low).u64(child.Range.High)
	}
	b.tag(11).id(string(e.Prev))
	b.tag(12).u64(e.Nonce)
	return b.buf
}

// EventDigest computes the host-bound digest of an event body. It is the
// canonical value that EventID must equal.
func EventDigest(cb Callbacks, e Event) (EventID, error) {
	if err := cb.valid(); err != nil {
		return "", err
	}
	sum, err := cb.Hash(context.Background(), eventBody(e))
	if err != nil {
		return "", err
	}
	return EventID(sum), nil
}

// EventSigningBody returns the canonical byte stream covered by an event
// signature. Host-side signers use it to create the Signature that the
// injected Verify callback later checks.
func EventSigningBody(e Event) []byte {
	return eventBody(e)
}

// ParamsCommitment binds KDF inputs to a credential chain.
func ParamsCommitment(cb Callbacks, p Params) ([]byte, error) {
	if err := cb.valid(); err != nil {
		return nil, err
	}
	b := new(canonicalBuilder)
	b.tag(1).u64(p.SCost)
	b.tag(2).u64(p.Iterations)
	b.tag(3).u32(p.Parallel)
	b.tag(4).bytes(p.Salt)
	b.tag(5).bytes(p.Password)
	b.tag(6).str(p.Label)
	return cb.Hash(context.Background(), b.buf)
}

func checkedAdd(a, b uint64) (uint64, error) {
	if a > ^uint64(0)-b {
		return 0, ErrOverflow
	}
	return a + b, nil
}

func checkedSub(a, b uint64) (uint64, error) {
	if b > a {
		return 0, ErrOverflow
	}
	return a - b, nil
}

package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"math/big"
)

// domainTag is the protocol domain separation label absorbed first into
// every transcript.
const domainTag = "netsec/innerprod/v1"

// challengeLabel separates the challenge squeeze input from absorbed data.
const challengeLabel = "challenge"

// transcript is the Fiat-Shamir transcript. It accumulates the exact byte
// string that is hashed for each challenge: the length-prefixed domain tag,
// host context, n, all generators, the commitment P, and the ordered,
// length-prefixed L/R encodings of every round so far.
type transcript struct {
	g   Group
	buf []byte
}

func appendUint64(buf []byte, v uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	return append(buf, b[:]...)
}

func appendLenPrefixed(buf, data []byte) []byte {
	buf = appendUint64(buf, uint64(len(data)))
	return append(buf, data...)
}

func newTranscript(g Group, params *Params, P Point) *transcript {
	t := &transcript{g: g}
	t.buf = appendLenPrefixed(t.buf, []byte(domainTag))
	t.buf = appendLenPrefixed(t.buf, params.Context)
	t.buf = appendUint64(t.buf, uint64(len(params.G)))
	for _, pt := range params.G {
		t.absorbPoint(pt)
	}
	for _, pt := range params.H {
		t.absorbPoint(pt)
	}
	t.absorbPoint(params.U)
	t.absorbPoint(P)
	return t
}

func (t *transcript) absorbPoint(p Point) {
	t.buf = appendLenPrefixed(t.buf, p.Bytes())
}

// challenge squeezes the round challenge x = SHA256(transcript) mod q.
// A zero result is rejected by re-hashing with an incremented counter, so
// the returned challenge is always non-zero and invertible.
func (t *transcript) challenge() Scalar {
	q := t.g.Order()
	for counter := uint64(0); ; counter++ {
		h := sha256.New()
		h.Write(t.buf)
		h.Write([]byte(challengeLabel))
		var cb [8]byte
		binary.BigEndian.PutUint64(cb[:], counter)
		h.Write(cb[:])
		x := new(big.Int).SetBytes(h.Sum(nil))
		x.Mod(x, q)
		if x.Sign() != 0 {
			return t.g.ScalarFromBig(x)
		}
	}
}

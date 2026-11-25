package ipp

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"math/big"
)

const DefaultDomainLabel = "pairwise-gsb/custom-prime-order-ipp/v1"

type Transcript struct {
	q   *big.Int
	buf bytes.Buffer
}

func writeLengthPrefixed(w io.Writer, b []byte) error {
	if uint64(len(b)) > math.MaxUint32 {
		return fmt.Errorf("ipp: transcript field too long: %d", len(b))
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(b)))
	if _, err := w.Write(length[:]); err != nil {
		return err
	}
	_, err := w.Write(b)
	return err
}

func appendLengthPrefixed(buf *bytes.Buffer, b []byte) error {
	return writeLengthPrefixed(buf, b)
}

func NewTranscript(ad Adapter, domainLabel, context []byte, gens Generators, commitment Point) (*Transcript, error) {
	pp, err := prepareParams(ad, context, gens, commitment, true)
	if err != nil {
		return nil, err
	}
	return newTranscript(domainLabel, context, pp)
}

func newTranscript(domainLabel, context []byte, pp *preparedParams) (*Transcript, error) {
	if pp == nil {
		return nil, fmt.Errorf("%w: prepared parameters are nil", ErrTranscript)
	}
	if len(domainLabel) == 0 {
		domainLabel = []byte(DefaultDomainLabel)
	}
	t := &Transcript{q: new(big.Int).Set(pp.q)}
	if err := appendLengthPrefixed(&t.buf, domainLabel); err != nil {
		return nil, err
	}
	if err := appendLengthPrefixed(&t.buf, context); err != nil {
		return nil, err
	}
	var nBytes [4]byte
	binary.BigEndian.PutUint32(nBytes[:], uint32(pp.n))
	if err := appendLengthPrefixed(&t.buf, nBytes[:]); err != nil {
		return nil, err
	}
	for _, g := range pp.g {
		if err := appendLengthPrefixed(&t.buf, g); err != nil {
			return nil, err
		}
	}
	for _, h := range pp.h {
		if err := appendLengthPrefixed(&t.buf, h); err != nil {
			return nil, err
		}
	}
	if err := appendLengthPrefixed(&t.buf, pp.u); err != nil {
		return nil, err
	}
	if err := appendLengthPrefixed(&t.buf, pp.p); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *Transcript) appendPair(l, r Point) error {
	if err := appendLengthPrefixed(&t.buf, l); err != nil {
		return err
	}
	return appendLengthPrefixed(&t.buf, r)
}

func (t *Transcript) initialTag() []byte {
	digest := sha256.Sum256(t.buf.Bytes())
	return digest[:]
}

func (t *Transcript) Challenge(l, r Point) (*big.Int, error) {
	if t == nil || t.q == nil {
		return nil, fmt.Errorf("%w: nil transcript", ErrTranscript)
	}
	if err := t.appendPair(l, r); err != nil {
		return nil, err
	}
	prefix := t.buf.Bytes()
	for counter := uint64(0); counter < 10000; counter++ {
		h := sha256.New()
		_, _ = h.Write(prefix)
		if counter > 0 {
			var counterBytes [8]byte
			binary.BigEndian.PutUint64(counterBytes[:], counter)
			if err := writeLengthPrefixed(h, counterBytes[:]); err != nil {
				return nil, err
			}
		}
		digest := h.Sum(nil)
		x := new(big.Int).SetBytes(digest)
		x.Mod(x, t.q)
		if x.Sign() != 0 {
			return x, nil
		}
	}
	return nil, fmt.Errorf("%w: repeated zero hashes", ErrChallengeZero)
}

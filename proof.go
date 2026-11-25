package ipp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
)

const proofMagic = "IPP1"

type Proof struct {
	N int
	L []Point
	R []Point
	A *big.Int
	B *big.Int

	contextTag []byte
}

func (p *Proof) Encode(ad Adapter) ([]byte, error) {
	return EncodeProof(ad, p)
}

func EncodeProof(ad Adapter, proof *Proof) ([]byte, error) {
	if proof == nil {
		return nil, fmt.Errorf("%w: nil proof", ErrProofFormat)
	}
	if ad == nil {
		return nil, fmt.Errorf("%w: nil adapter", ErrAdapter)
	}
	q := ad.Order()
	if err := checkOrder(q); err != nil {
		return nil, err
	}
	rounds, err := roundsForN(proof.N)
	if err != nil {
		return nil, err
	}
	if len(proof.L) != rounds || len(proof.R) != rounds {
		return nil, fmt.Errorf("%w: n=%d wants %d rounds, got L=%d R=%d",
			ErrWrongRoundCount, proof.N, rounds, len(proof.L), len(proof.R))
	}
	if err := checkScalar(proof.A, q); err != nil {
		return nil, fmt.Errorf("final a: %w", err)
	}
	if err := checkScalar(proof.B, q); err != nil {
		return nil, fmt.Errorf("final b: %w", err)
	}

	out := make([]byte, 0, 64+33*2*rounds)
	out = append(out, proofMagic...)
	var nBytes [2]byte
	binary.BigEndian.PutUint16(nBytes[:], uint16(proof.N))
	out = append(out, nBytes[:]...)
	out = append(out, byte(rounds))

	for i := 0; i < rounds; i++ {
		l, err := canonicalPoint(ad, proof.L[i])
		if err != nil {
			return nil, fmt.Errorf("L[%d]: %w", i, err)
		}
		r, err := canonicalPoint(ad, proof.R[i])
		if err != nil {
			return nil, fmt.Errorf("R[%d]: %w", i, err)
		}
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(l)))
		out = append(out, length[:]...)
		out = append(out, l...)
		binary.BigEndian.PutUint32(length[:], uint32(len(r)))
		out = append(out, length[:]...)
		out = append(out, r...)
	}

	aBytes, err := encodeScalar(proof.A, q)
	if err != nil {
		return nil, fmt.Errorf("final a: %w", err)
	}
	bBytes, err := encodeScalar(proof.B, q)
	if err != nil {
		return nil, fmt.Errorf("final b: %w", err)
	}
	out = append(out, aBytes...)
	out = append(out, bBytes...)

	var tagLength [4]byte
	binary.BigEndian.PutUint32(tagLength[:], uint32(len(proof.contextTag)))
	out = append(out, tagLength[:]...)
	out = append(out, proof.contextTag...)
	return out, nil
}

func DecodeProof(ad Adapter, raw []byte) (*Proof, error) {
	if ad == nil {
		return nil, fmt.Errorf("%w: nil adapter", ErrAdapter)
	}
	q := ad.Order()
	if err := checkOrder(q); err != nil {
		return nil, err
	}
	if len(raw) < len(proofMagic)+3 {
		return nil, fmt.Errorf("%w: header", ErrTruncatedProof)
	}
	pos := 0
	magic := string(raw[pos : pos+len(proofMagic)])
	pos += len(proofMagic)
	if magic != proofMagic {
		return nil, fmt.Errorf("%w: unknown magic %q", ErrProofFormat, magic)
	}
	n := int(binary.BigEndian.Uint16(raw[pos : pos+2]))
	pos += 2
	if !ValidN(n) {
		return nil, fmt.Errorf("%w: %d", ErrN, n)
	}
	rounds, err := roundsForN(n)
	if err != nil {
		return nil, err
	}
	wireRounds := int(raw[pos])
	pos++
	if wireRounds != rounds {
		return nil, fmt.Errorf("%w: header says %d, n says %d", ErrWrongRoundCount, wireRounds, rounds)
	}

	proof := &Proof{N: n, L: make([]Point, rounds), R: make([]Point, rounds)}
	readUint32 := func() (int, error) {
		if pos+4 > len(raw) {
			return 0, fmt.Errorf("%w: length prefix at %d", ErrTruncatedProof, pos)
		}
		value := binary.BigEndian.Uint32(raw[pos : pos+4])
		pos += 4
		if uint64(value) > math.MaxInt {
			return 0, fmt.Errorf("%w: point length overflow", ErrProofFormat)
		}
		return int(value), nil
	}
	readPoint := func(field string) (Point, error) {
		length, err := readUint32()
		if err != nil {
			return nil, err
		}
		if length > len(raw)-pos {
			return nil, fmt.Errorf("%w: %s needs %d bytes", ErrTruncatedProof, field, length)
		}
		fieldRaw := raw[pos : pos+length]
		pos += length
		p, err := canonicalPoint(ad, fieldRaw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field, err)
		}
		return p, nil
	}

	for i := 0; i < rounds; i++ {
		l, err := readPoint(fmt.Sprintf("L[%d]", i))
		if err != nil {
			return nil, err
		}
		r, err := readPoint(fmt.Sprintf("R[%d]", i))
		if err != nil {
			return nil, err
		}
		proof.L[i] = l
		proof.R[i] = r
	}

	scalarLength := scalarByteLen(q)
	readScalar := func(field string) (*big.Int, error) {
		if scalarLength > len(raw)-pos {
			return nil, fmt.Errorf("%w: %s", ErrTruncatedProof, field)
		}
		fieldRaw := raw[pos : pos+scalarLength]
		pos += scalarLength
		s, err := parseScalar(fieldRaw, q, false)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field, err)
		}
		return s, nil
	}
	a, err := readScalar("final a")
	if err != nil {
		return nil, err
	}
	b, err := readScalar("final b")
	if err != nil {
		return nil, err
	}
	proof.A = a
	proof.B = b

	if pos+4 > len(raw) {
		return nil, fmt.Errorf("%w: context tag length", ErrTruncatedProof)
	}
	tagLength := int(binary.BigEndian.Uint32(raw[pos : pos+4]))
	pos += 4
	if tagLength > len(raw)-pos {
		return nil, fmt.Errorf("%w: context tag", ErrTruncatedProof)
	}
	if tagLength > 0 {
		proof.contextTag = append([]byte(nil), raw[pos:pos+tagLength]...)
	}
	pos += tagLength
	if pos != len(raw) {
		return nil, fmt.Errorf("%w: %d unexpected bytes", ErrTrailingBytes, len(raw)-pos)
	}
	return proof, nil
}

func IsVerificationError(err error) bool {
	return errors.Is(err, ErrVerificationFailed)
}

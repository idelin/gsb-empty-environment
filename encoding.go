package netsec

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

var proofMagic = [6]byte{'S', 'M', 'T', 'M', 'P', 0x01}

// MarshalBinary encodes a structurally valid proof. The trusted root is not
// part of the encoding.
func (p *MultiProof) MarshalBinary() ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.Grow(6 + 1 + 2 + 4 + len(p.Queries)*17 + len(p.Sides)*41)
	_, _ = buf.Write(proofMagic[:])
	_ = binary.Write(&buf, binary.BigEndian, p.Height)
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(p.Queries)))

	for _, query := range p.Queries {
		_ = binary.Write(&buf, binary.BigEndian, query.Key)
		if query.Present {
			_ = buf.WriteByte(1)
		} else {
			_ = buf.WriteByte(0)
		}
		_ = binary.Write(&buf, binary.BigEndian, uint64(len(query.Value)))
		_, _ = buf.Write(query.Value)
	}

	_ = binary.Write(&buf, binary.BigEndian, uint32(len(p.Sides)))
	for _, side := range p.Sides {
		_ = buf.WriteByte(side.Depth)
		_ = binary.Write(&buf, binary.BigEndian, side.Prefix)
		_, _ = buf.Write(side.Digest[:])
	}
	return buf.Bytes(), nil
}

// UnmarshalBinary strictly decodes and validates a proof. It rejects truncated,
// oversized, trailing, and semantically invalid encodings.
func (p *MultiProof) UnmarshalBinary(data []byte) error {
	if p == nil {
		return ErrNilProof
	}

	reader := bytes.NewReader(data)
	var magic [6]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil {
		return ErrMalformedProof
	}
	if magic != proofMagic {
		return fmt.Errorf("%w: bad magic", ErrMalformedProof)
	}

	var height uint8
	if err := binary.Read(reader, binary.BigEndian, &height); err != nil {
		return ErrMalformedProof
	}
	if !validHeight(height) {
		return ErrHeight
	}

	var queryCount uint16
	if err := binary.Read(reader, binary.BigEndian, &queryCount); err != nil {
		return ErrMalformedProof
	}
	if queryCount == 0 || int(queryCount) > MaxQueries {
		return fmt.Errorf("%w: got %d", ErrQueryCount, queryCount)
	}

	proof := MultiProof{
		Height:  height,
		Queries: make([]ProofQuery, int(queryCount)),
	}
	for index := range proof.Queries {
		var key uint64
		if err := binary.Read(reader, binary.BigEndian, &key); err != nil {
			return ErrMalformedProof
		}
		flag, err := reader.ReadByte()
		if err != nil {
			return ErrMalformedProof
		}
		if flag > 1 {
			return fmt.Errorf("%w: presence flag %d", ErrMalformedProof, flag)
		}
		var valueLength uint64
		if err := binary.Read(reader, binary.BigEndian, &valueLength); err != nil {
			return ErrMalformedProof
		}
		if valueLength > uint64(reader.Len()) {
			return fmt.Errorf("%w: value length %d", ErrMalformedProof, valueLength)
		}
		value := make([]byte, int(valueLength))
		if _, err := io.ReadFull(reader, value); err != nil {
			return ErrMalformedProof
		}
		proof.Queries[index] = ProofQuery{
			Key:     key,
			Present: flag == 1,
			Value:   value,
		}
	}

	var sideCount uint32
	if err := binary.Read(reader, binary.BigEndian, &sideCount); err != nil {
		return ErrMalformedProof
	}
	if int64(sideCount) > int64(len(proof.Queries))*int64(height) {
		return fmt.Errorf("%w: got %d", ErrTooManySides, sideCount)
	}
	proof.Sides = make([]ProofSide, int(sideCount))
	for index := range proof.Sides {
		depth, err := reader.ReadByte()
		if err != nil {
			return ErrMalformedProof
		}
		var prefix uint64
		if err := binary.Read(reader, binary.BigEndian, &prefix); err != nil {
			return ErrMalformedProof
		}
		var digest Digest
		if _, err := io.ReadFull(reader, digest[:]); err != nil {
			return ErrMalformedProof
		}
		proof.Sides[index] = ProofSide{
			Depth:  depth,
			Prefix: prefix,
			Digest: digest,
		}
	}

	if reader.Len() != 0 {
		return fmt.Errorf("%w: %d bytes", ErrTrailingBytes, reader.Len())
	}
	if err := proof.Validate(); err != nil {
		return err
	}
	*p = proof
	return nil
}

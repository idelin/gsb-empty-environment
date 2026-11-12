package sctpaddr

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"hash"
)

func hashFor(id uint16) (func() hash.Hash, error) {
	switch id {
	case HMACSHA1:
		return sha1.New, nil
	case HMACSHA256:
		return sha256.New, nil
	default:
		return nil, ErrUnknownHMAC
	}
}

// computeHMAC implements the RFC 4895 5.2 construction: a 12-byte
// pseudo-header of both ports followed by eight zero bytes, the common header
// with a zeroed checksum, and every chunk positioned before AUTH in unpadded
// form. Parameter padding and chunk padding never enter the MAC input.
func computeHMAC(key []byte, id uint16, hdr CommonHeader, before []Chunk) ([]byte, error) {
	newHash, err := hashFor(id)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(newHash, key)

	var pseudo [12]byte
	binary.BigEndian.PutUint16(pseudo[0:], hdr.SrcPort)
	binary.BigEndian.PutUint16(pseudo[2:], hdr.DstPort)
	mac.Write(pseudo[:])

	var common [12]byte
	binary.BigEndian.PutUint16(common[0:], hdr.SrcPort)
	binary.BigEndian.PutUint16(common[2:], hdr.DstPort)
	binary.BigEndian.PutUint32(common[4:], hdr.VerificationTag)
	mac.Write(common[:])

	for _, c := range before {
		mac.Write(marshalChunk(c))
	}
	return mac.Sum(nil), nil
}

func buildAUTHChunk(id uint16, mac []byte) Chunk {
	data := make([]byte, 2+len(mac))
	binary.BigEndian.PutUint16(data[0:], id)
	copy(data[2:], mac)
	return Chunk{Type: ChunkAUTH, Data: data}
}

// signPacket appends an AUTH chunk after all payload chunks and returns the
// complete datagram. AUTH is always the last chunk (RFC 4895 5.1), so the HMAC
// covers exactly the chunks preceding it.
func signPacket(key []byte, id uint16, hdr CommonHeader, chunks []Chunk) ([]byte, error) {
	mac, err := computeHMAC(key, id, hdr, chunks)
	if err != nil {
		return nil, err
	}
	all := make([]Chunk, 0, len(chunks)+1)
	all = append(all, chunks...)
	all = append(all, buildAUTHChunk(id, mac))
	return marshalPacket(hdr, all...), nil
}

// verifyAUTH requires exactly one AUTH chunk positioned last, and recomputes
// the HMAC over all preceding chunks. A valid ASCONF therefore has wire order
// ASCONF then AUTH: an attacker can neither append unauthenticated chunks
// after AUTH nor place the ASCONF outside the MAC scope.
func verifyAUTH(key []byte, hdr CommonHeader, chunks []Chunk) error {
	if len(chunks) < 2 || chunks[len(chunks)-1].Type != ChunkAUTH {
		return ErrMissingAUTH
	}
	for _, c := range chunks[:len(chunks)-1] {
		if c.Type == ChunkAUTH {
			return ErrMissingAUTH
		}
	}
	auth := chunks[len(chunks)-1]
	if len(auth.Data) < 2 {
		return ErrMalformed
	}
	id := binary.BigEndian.Uint16(auth.Data[0:])
	gotMAC := auth.Data[2:]

	wantMAC, err := computeHMAC(key, id, hdr, chunks[:len(chunks)-1])
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(gotMAC, wantMAC) != 1 {
		return ErrBadHMAC
	}
	return nil
}

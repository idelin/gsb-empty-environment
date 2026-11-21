package recovery

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"io"
)

const (
	frameNonceSize = 12
	frameTagSize   = 16
	frameLenSize   = 4
	frameOverhead  = frameNonceSize + frameTagSize + frameLenSize
)

var frameAADDomain = []byte("recovery/frame/v1\x00")

func signBytes(key [32]byte, payload []byte) [32]byte {
	mac := hmac.New(sha256.New, key[:])
	mac.Write(payload)
	var out [32]byte
	copy(out[:], mac.Sum(nil))
	return out
}

func hashBytes(chunks ...[]byte) Hash {
	h := sha256.New()
	for _, chunk := range chunks {
		h.Write(chunk)
	}
	var out Hash
	copy(out[:], h.Sum(nil))
	return out
}

func hashWriterSum(h hash.Hash) Hash {
	var out Hash
	copy(out[:], h.Sum(nil))
	return out
}

func txnCanonical(txn SignedTxn) []byte {
	buf := make([]byte, 0, 1+16+8+16+8+16)
	buf = append(buf, 1)
	buf = append(buf, txn.Tenant[:]...)
	buf = binary.BigEndian.AppendUint64(buf, txn.LeaseEpoch)
	buf = append(buf, txn.LeaseID[:]...)
	buf = binary.BigEndian.AppendUint64(buf, txn.BaseGeneration)
	buf = append(buf, txn.Nonce[:]...)
	buf = append(buf, txn.ID[:]...)
	return buf
}

func frameAAD(region Region, slot int, tenant TenantID) []byte {
	buf := make([]byte, 0, len(frameAADDomain)+1+4+16)
	buf = append(buf, frameAADDomain...)
	buf = append(buf, byte(region))
	buf = binary.BigEndian.AppendUint32(buf, uint32(slot))
	buf = append(buf, tenant[:]...)
	return buf
}

func newAEAD(key [32]byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func sealFrame(aead cipher.AEAD, rng io.Reader, region Region, slot int, tenant TenantID, payload []byte, frameSize int) ([]byte, error) {
	maxPayload := frameSize - frameOverhead
	if len(payload) > maxPayload {
		return nil, fmt.Errorf("%w: payload %d > %d", ErrBlockTooLarge, len(payload), maxPayload)
	}
	plain := make([]byte, frameLenSize+maxPayload)
	binary.BigEndian.PutUint32(plain[:frameLenSize], uint32(len(payload)))
	copy(plain[frameLenSize:], payload)

	frame := make([]byte, frameNonceSize, frameSize)
	if _, err := io.ReadFull(rng, frame[:frameNonceSize]); err != nil {
		return nil, fmt.Errorf("recovery: nonce: %w", err)
	}
	sealed := aead.Seal(nil, frame[:frameNonceSize], plain, frameAAD(region, slot, tenant))
	frame = append(frame, sealed...)
	if len(frame) != frameSize {
		return nil, fmt.Errorf("%w: sealed frame is %d bytes, want %d", ErrConfig, len(frame), frameSize)
	}
	return frame, nil
}

func openFrame(aead cipher.AEAD, region Region, slot int, tenant TenantID, frame []byte, frameSize int) ([]byte, error) {
	if len(frame) != frameSize {
		return nil, fmt.Errorf("recovery: frame length %d, want %d", len(frame), frameSize)
	}
	if frameSize <= frameOverhead {
		return nil, fmt.Errorf("%w: frame too small", ErrConfig)
	}
	plain, err := aead.Open(nil, frame[:frameNonceSize], frame[frameNonceSize:], frameAAD(region, slot, tenant))
	if err != nil {
		return nil, err
	}
	if len(plain) < frameLenSize {
		return nil, fmt.Errorf("recovery: short frame plaintext")
	}
	n := int(binary.BigEndian.Uint32(plain[:frameLenSize]))
	if n < 0 || n > len(plain)-frameLenSize {
		return nil, fmt.Errorf("recovery: invalid frame payload length")
	}
	for _, b := range plain[frameLenSize+n:] {
		if b != 0 {
			return nil, fmt.Errorf("recovery: non-zero frame padding")
		}
	}
	return plain[frameLenSize : frameLenSize+n], nil
}

func defaultRand() io.Reader { return rand.Reader }

func snapshotHash(blocks []Block) Hash {
	h := sha256.New()
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(blocks)))
	h.Write(lenBuf[:])
	for _, block := range blocks {
		h.Write(block.Tenant[:])
		h.Write(block.ID[:])
		var v [8]byte
		binary.BigEndian.PutUint64(v[:], block.Version)
		h.Write(v[:])
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(block.Data)))
		h.Write(lenBuf[:])
		h.Write(block.Data)
	}
	return hashWriterSum(h)
}

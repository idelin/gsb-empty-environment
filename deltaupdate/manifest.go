// Package delta implements content-defined block delta updates: a client
// holding an old file reconciles it against a server-provided manifest of
// the new file's block digests, reusing local blocks wherever their
// content (not their offset) matches.
package delta

import (
	"crypto/sha256"
	"io"
)

// DefaultBlockSize is used when a zero/negative block size is requested.
const DefaultBlockSize = 64 * 1024

// BlockMeta describes one block of the new file.
type BlockMeta struct {
	Index  int      `json:"index"`
	Offset int64    `json:"offset"`
	Length int      `json:"length"`
	Weak   uint32   `json:"weak"`
	Strong [32]byte `json:"strong"`
}

// Manifest is the server's summary of the new file.
type Manifest struct {
	BlockSize int         `json:"block_size"`
	Size      int64       `json:"size"`
	Digest    [32]byte    `json:"digest"` // whole-file SHA-256
	Blocks    []BlockMeta `json:"blocks"`
}

// weakSum is an rsync-style rolling checksum: low 16 bits are the plain
// byte sum, high 16 bits the position-weighted sum.
func weakSum(b []byte) uint32 {
	var a, c uint32
	for i, x := range b {
		a += uint32(x)
		c += uint32(len(b)-i) * uint32(x)
	}
	return (a & 0xffff) | ((c & 0xffff) << 16)
}

// roll slides a weak sum one byte forward: out leaves, in enters.
func roll(w uint32, out, in byte, n int) uint32 {
	a := w & 0xffff
	c := (w >> 16) & 0xffff
	a = a - uint32(out) + uint32(in)
	c = c - uint32(n)*uint32(out) + a
	return (a & 0xffff) | ((c & 0xffff) << 16)
}

// BuildManifest reads r and produces the block digest list plus the
// whole-file digest of the new file.
func BuildManifest(r io.Reader, blockSize int) (*Manifest, error) {
	if blockSize <= 0 {
		blockSize = DefaultBlockSize
	}
	m := &Manifest{BlockSize: blockSize}
	h := sha256.New()
	buf := make([]byte, blockSize)
	var off int64
	for {
		n, err := io.ReadFull(r, buf)
		if n > 0 {
			block := buf[:n]
			h.Write(block)
			m.Blocks = append(m.Blocks, BlockMeta{
				Index:  len(m.Blocks),
				Offset: off,
				Length: n,
				Weak:   weakSum(block),
				Strong: sha256.Sum256(block),
			})
			off += int64(n)
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	m.Size = off
	copy(m.Digest[:], h.Sum(nil))
	return m, nil
}

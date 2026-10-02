// Package blockindex implements a block index with cryptographic structural
// summaries and local membership / non-membership / range proofs.
//
// All integers are unsigned varints (encoding/binary). Two container
// encodings exist:
//
//	Block ("BLK1"): magic | version | count | firstKey |
//	                count * { keyDelta, valueLen, value }
//	Node  ("SUM1"): magic | version | level | nChild |
//	                nChild * { minKeyDelta, maxKeyDelta, entries, blocks, digest[32] }
//
// A block digest is SHA-256 over its exact serialized bytes. A summary node
// digest is SHA-256 over its exact serialized bytes, so every internal node
// binds child order, child key ranges, child entry/block counts and the
// format version. The root digest is the single commitment a verifier needs.
package blockindex

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// MaxVarintLen is the maximum encoded length of a uint64 varint.
	MaxVarintLen = 10
	// DigestLen is the byte length of a content digest.
	DigestLen = sha256.Size

	blockMagic = "BLK1"
	nodeMagic  = "SUM1"
)

func appendUvarint(dst []byte, v uint64) []byte {
	var tmp [MaxVarintLen]byte
	n := binary.PutUvarint(tmp[:], v)
	return append(dst, tmp[:n]...)
}

// decoder is a strict cursor over a byte slice; any failure latches err.
type decoder struct {
	buf []byte
	err error
}

func (d *decoder) uvarint() uint64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Uvarint(d.buf)
	if n <= 0 {
		d.err = errors.New("blockindex: malformed varint")
		return 0
	}
	d.buf = d.buf[n:]
	return v
}

func (d *decoder) fixed(n int) []byte {
	if d.err != nil {
		return nil
	}
	if len(d.buf) < n {
		d.err = errors.New("blockindex: truncated input")
		return nil
	}
	b := d.buf[:n]
	d.buf = d.buf[n:]
	return b
}

func (d *decoder) magic(want string) {
	got := d.fixed(4)
	if d.err != nil {
		return
	}
	if string(got) != want {
		d.err = fmt.Errorf("blockindex: bad magic %q, want %q", got, want)
	}
}

func (d *decoder) done() error {
	if d.err != nil {
		return d.err
	}
	if len(d.buf) != 0 {
		return fmt.Errorf("blockindex: %d trailing bytes", len(d.buf))
	}
	return nil
}

func digestOf(parts ...[]byte) [DigestLen]byte {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	var out [DigestLen]byte
	copy(out[:], h.Sum(nil))
	return out
}

// add64 adds two counters and rejects overflow so hostile proofs cannot
// wrap aggregate counts around.
func add64(a, b uint64) (uint64, error) {
	s := a + b
	if s < a {
		return 0, errors.New("blockindex: counter overflow")
	}
	return s, nil
}

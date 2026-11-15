package netsec

import (
	"bytes"
	"crypto/subtle"
)

// Verifier is used on the read path: it authenticates individual data
// blocks against the signed measurement before they are served.
type Verifier struct {
	desc     *Descriptor
	spec     hashSpec
	prefix   []byte
	levels   [][]byte
	nblocks  int64
	hashSize int
}

// NewVerifier validates the 256-byte descriptor against the signed
// measurement and the actual data size, then attaches the hash tree blob.
// It rejects any swap of salt or algorithm (measurement mismatch) and any
// truncation or append (size mismatch).
func NewVerifier(descBlob, measurement, treeBlob []byte, actualSize int64) (*Verifier, error) {
	desc, err := ParseDescriptor(descBlob)
	if err != nil {
		return nil, err
	}
	spec, _ := specFor(desc.Algorithm)
	if len(measurement) != spec.size {
		return nil, ErrMeasurementMismatch
	}
	want, err := Measurement(descBlob, desc.Algorithm)
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare(want, measurement) != 1 {
		return nil, ErrMeasurementMismatch
	}
	if actualSize < 0 || uint64(actualSize) != desc.DataSize {
		return nil, ErrSizeMismatch
	}
	levels, err := splitTreeBlob(desc, treeBlob)
	if err != nil {
		return nil, err
	}
	return &Verifier{
		desc:     desc,
		spec:     spec,
		prefix:   saltPrefix(spec, desc.Salt),
		levels:   levels,
		nblocks:  ceilDiv(int64(desc.DataSize), BlockSize),
		hashSize: spec.size,
	}, nil
}

// DataSize returns the authenticated data length from the descriptor.
func (v *Verifier) DataSize() int64 {
	return int64(v.desc.DataSize)
}

func (v *Verifier) hashOf(b []byte) []byte {
	h := v.spec.newHash()
	h.Write(v.prefix)
	h.Write(b)
	return h.Sum(nil)
}

// VerifyBlock authenticates block index (0-based) against the Merkle tree,
// walking the hash path all the way to the root. block must be exactly the
// bytes read for that block: BlockSize for every block except the last,
// which must be the exact remainder (zero-padded internally before hashing).
func (v *Verifier) VerifyBlock(index int64, block []byte) error {
	if index < 0 || index >= v.nblocks {
		return ErrBlockOutOfRange
	}
	want := int64(BlockSize)
	if index == v.nblocks-1 {
		want = int64(v.desc.DataSize) - index*BlockSize
	}
	if int64(len(block)) != want {
		return ErrSizeMismatch
	}

	var padded [BlockSize]byte
	copy(padded[:], block)
	digest := v.hashOf(padded[:])

	if len(v.levels) == 0 {
		if !bytes.Equal(digest, v.desc.RootHash) {
			return ErrHashMismatch
		}
		return nil
	}

	hashesPerBlock := BlockSize / v.hashSize
	idx := int(index)
	for _, level := range v.levels {
		blockIdx := idx / hashesPerBlock
		if (blockIdx+1)*BlockSize > len(level) {
			return ErrTreeCorrupt
		}
		off := blockIdx*BlockSize + (idx%hashesPerBlock)*v.hashSize
		if !bytes.Equal(digest, level[off:off+v.hashSize]) {
			return ErrHashMismatch
		}
		digest = v.hashOf(level[blockIdx*BlockSize : (blockIdx+1)*BlockSize])
		idx = blockIdx
	}
	if !bytes.Equal(digest, v.desc.RootHash) {
		return ErrHashMismatch
	}
	return nil
}

// VerifyAll authenticates the whole file contents block by block.
func (v *Verifier) VerifyAll(data []byte) error {
	if int64(len(data)) != int64(v.desc.DataSize) {
		return ErrSizeMismatch
	}
	for i := int64(0); i < v.nblocks; i++ {
		lo := i * BlockSize
		hi := lo + BlockSize
		if hi > int64(len(data)) {
			hi = int64(len(data))
		}
		if err := v.VerifyBlock(i, data[lo:hi]); err != nil {
			return err
		}
	}
	return nil
}

package blockpack

import (
	"bytes"
	"errors"
	"fmt"
)

var errRange = errors.New("blockpack: range out of bounds")

var magic = []byte("BPK1")

// Header commits to the package version, the exact object and block counts,
// the block size, and the root digest.
type Header struct {
	Version     uint32
	ObjectCount uint32
	BlockCount  uint32
	BlockSize   uint32
	Root        Digest
}

// Block is a group of consecutive objects plus its summary. The summary
// carries the object-level root digest of the whole package, so every block
// can be checked against the same root while streaming.
type Block struct {
	Index       int
	Digest      Digest
	SummaryRoot Digest
	Objects     []Object
}

// Package is a fully built or parsed package.
type Package struct {
	Header   Header
	Blocks   []Block
	objDigs  []Digest
	blkDigs  []Digest
	chain    Digest
	refGraph Digest
}

// blockDigest commits to the block index, its object count, and the ordered
// object digests inside the block.
func blockDigest(index int, digs []Digest) Digest {
	parts := [][]byte{[]byte("blk\x00"), u32(uint32(index)), u32(uint32(len(digs)))}
	for _, d := range digs {
		parts = append(parts, d[:])
	}
	return hashParts(parts...)
}

// chainDigest folds all object digests in order, seeded with the version and
// the total object count, so the result commits to object order and count.
func chainDigest(version, count uint32, digs []Digest) Digest {
	acc := hashParts([]byte("chain\x00"), u32(version), u32(count))
	for _, d := range digs {
		acc = hashParts([]byte("chain\x00"), acc[:], d[:])
	}
	return acc
}

// refGraphDigest commits to the full reference graph: for every object, its
// index and its ordered list of outgoing references.
func refGraphDigest(objs []Object) Digest {
	parts := [][]byte{[]byte("refs\x00")}
	for i, o := range objs {
		parts = append(parts, u32(uint32(i)), u32(uint32(len(o.Refs))))
		for _, r := range o.Refs {
			parts = append(parts, u32(r))
		}
	}
	return hashParts(parts...)
}

func computeRoot(h Header, chain, refGraph, merkle Digest) Digest {
	return hashParts(
		[]byte("root\x00"),
		u32(h.Version), u32(h.ObjectCount), u32(h.BlockCount), u32(h.BlockSize),
		chain[:], refGraph[:], merkle[:],
	)
}

// Build creates a package from objects, grouping them into blocks of
// blockSize objects (the last block may be smaller).
func Build(version uint32, blockSize int, objs []Object) (*Package, error) {
	if blockSize <= 0 {
		return nil, errors.New("blockpack: block size must be positive")
	}
	if len(objs) == 0 {
		return nil, errors.New("blockpack: package must contain at least one object")
	}
	if len(objs) > MaxObjects {
		return nil, fmt.Errorf("blockpack: too many objects: %d", len(objs))
	}
	digs := make([]Digest, len(objs))
	for i, o := range objs {
		for _, r := range o.Refs {
			if int(r) >= len(objs) {
				return nil, fmt.Errorf("blockpack: object %d references missing object %d", i, r)
			}
		}
		enc, err := EncodeObject(o)
		if err != nil {
			return nil, err
		}
		digs[i] = objectDigest(enc)
	}

	nBlocks := (len(objs) + blockSize - 1) / blockSize
	p := &Package{
		Header: Header{
			Version:     version,
			ObjectCount: uint32(len(objs)),
			BlockCount:  uint32(nBlocks),
			BlockSize:   uint32(blockSize),
		},
		objDigs:  digs,
		chain:    chainDigest(version, uint32(len(objs)), digs),
		refGraph: refGraphDigest(objs),
	}
	p.blkDigs = make([]Digest, nBlocks)
	p.Blocks = make([]Block, nBlocks)
	for b := 0; b < nBlocks; b++ {
		lo, hi := b*blockSize, (b+1)*blockSize
		if hi > len(objs) {
			hi = len(objs)
		}
		p.blkDigs[b] = blockDigest(b, digs[lo:hi])
		p.Blocks[b] = Block{Index: b, Digest: p.blkDigs[b], Objects: objs[lo:hi]}
	}
	p.Header.Root = computeRoot(p.Header, p.chain, p.refGraph, merkleRoot(p.blkDigs))
	for i := range p.Blocks {
		p.Blocks[i].SummaryRoot = p.Header.Root
	}
	return p, nil
}

// Serialize encodes the package into the wire format.
func Serialize(p *Package) []byte {
	buf := append([]byte(nil), magic...)
	buf = append(buf, u32(p.Header.Version)...)
	buf = append(buf, u32(p.Header.ObjectCount)...)
	buf = append(buf, u32(p.Header.BlockCount)...)
	buf = append(buf, u32(p.Header.BlockSize)...)
	buf = append(buf, p.Header.Root[:]...)
	for _, b := range p.Blocks {
		buf = append(buf, u32(uint32(b.Index))...)
		buf = append(buf, u32(uint32(len(b.Objects)))...)
		buf = append(buf, b.Digest[:]...)
		buf = append(buf, b.SummaryRoot[:]...)
		for _, o := range b.Objects {
			enc, err := EncodeObject(o)
			if err != nil {
				panic(err) // objects in a built package always encode
			}
			buf = append(buf, u32(uint32(len(enc)))...)
			buf = append(buf, enc...)
		}
	}
	return buf
}

// Parse decodes a serialized package. All counts and length prefixes are
// validated against the supported ranges and the remaining input before any
// allocation; decoding is pure data and never executes host code.
func Parse(data []byte) (*Package, error) {
	if len(data) < len(magic) || !bytes.Equal(data[:len(magic)], magic) {
		return nil, errors.New("blockpack: bad magic")
	}
	rest := data[len(magic):]
	var h Header
	var err error
	if h.Version, rest, err = takeU32(rest); err != nil {
		return nil, err
	}
	if h.ObjectCount, rest, err = takeU32(rest); err != nil {
		return nil, err
	}
	if h.BlockCount, rest, err = takeU32(rest); err != nil {
		return nil, err
	}
	if h.BlockSize, rest, err = takeU32(rest); err != nil {
		return nil, err
	}
	if h.ObjectCount == 0 || h.ObjectCount > MaxObjects ||
		h.BlockCount == 0 || h.BlockCount > MaxBlocks || h.BlockSize == 0 {
		return nil, errLength
	}
	if len(rest) < 32 {
		return nil, errTruncated
	}
	copy(h.Root[:], rest[:32])
	rest = rest[32:]

	p := &Package{Header: h}
	p.Blocks = make([]Block, 0, h.BlockCount)
	p.objDigs = make([]Digest, 0, h.ObjectCount)
	p.blkDigs = make([]Digest, 0, h.BlockCount)
	var allObjs []Object
	for bi := uint32(0); bi < h.BlockCount; bi++ {
		var idx, nObj uint32
		if idx, rest, err = takeU32(rest); err != nil {
			return nil, err
		}
		if nObj, rest, err = takeU32(rest); err != nil {
			return nil, err
		}
		if uint64(idx) != uint64(bi) || nObj == 0 || nObj > h.BlockSize ||
			uint64(nObj) > uint64(h.ObjectCount) {
			return nil, errLength
		}
		if len(rest) < 64 {
			return nil, errTruncated
		}
		var blk Block
		blk.Index = int(idx)
		copy(blk.Digest[:], rest[:32])
		copy(blk.SummaryRoot[:], rest[32:64])
		rest = rest[64:]
		for oi := uint32(0); oi < nObj; oi++ {
			var encLen uint32
			if encLen, rest, err = takeU32(rest); err != nil {
				return nil, err
			}
			if encLen == 0 || encLen > MaxObjectEnc || uint32(len(rest)) < encLen {
				return nil, errLength
			}
			enc := rest[:encLen]
			rest = rest[encLen:]
			o, remain, err := DecodeObject(enc)
			if err != nil {
				return nil, err
			}
			if len(remain) != 0 {
				return nil, errors.New("blockpack: trailing bytes in object encoding")
			}
			blk.Objects = append(blk.Objects, o)
			p.objDigs = append(p.objDigs, objectDigest(enc))
			allObjs = append(allObjs, o)
		}
		p.blkDigs = append(p.blkDigs, blk.Digest)
		p.Blocks = append(p.Blocks, blk)
	}
	if len(rest) != 0 {
		return nil, errors.New("blockpack: trailing bytes after last block")
	}
	if uint32(len(allObjs)) != h.ObjectCount {
		return nil, errors.New("blockpack: object count mismatch")
	}
	p.chain = chainDigest(h.Version, h.ObjectCount, p.objDigs)
	p.refGraph = refGraphDigest(allObjs)
	return p, nil
}

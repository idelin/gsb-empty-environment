package netsec

// Tree is the fs-verity style Merkle tree over a file's 4096-byte blocks.
// Levels[0] holds the packed hashes of the data blocks, Levels[1] the packed
// hashes of Levels[0]'s blocks, and so on; every level is zero-padded to a
// whole number of blocks. The root hash authenticates the top level (or the
// single data block, or the empty input).
type Tree struct {
	Algorithm byte
	Salt      []byte
	DataSize  int64
	RootHash  []byte
	Levels    [][]byte
}

func ceilDiv(n, d int64) int64 {
	return (n + d - 1) / d
}

// levelSizes returns the byte size of each tree level for a file of
// dataSize bytes, given the hash size. A nil slice means no tree levels
// (empty file or a single data block).
func levelSizes(dataSize uint64, hashSize int) []int64 {
	if dataSize == 0 {
		return nil
	}
	n := ceilDiv(int64(dataSize), BlockSize)
	var sizes []int64
	for n > 1 {
		n = ceilDiv(n, BlockSize/int64(hashSize))
		sizes = append(sizes, n*BlockSize)
	}
	return sizes
}

// BuildTree hashes data into a Merkle tree using 4096-byte blocks, a
// zero-padded final block, and level-by-level packing, exactly as fs-verity.
func BuildTree(data []byte, alg byte, salt []byte) (*Tree, error) {
	spec, err := specFor(alg)
	if err != nil {
		return nil, err
	}
	if len(salt) > MaxSaltSize {
		return nil, ErrInvalidDescriptor
	}
	prefix := saltPrefix(spec, salt)
	hashOf := func(b []byte) []byte {
		h := spec.newHash()
		h.Write(prefix)
		h.Write(b)
		return h.Sum(nil)
	}

	t := &Tree{
		Algorithm: alg,
		Salt:      append([]byte(nil), salt...),
		DataSize:  int64(len(data)),
	}

	nblocks := ceilDiv(int64(len(data)), BlockSize)
	if nblocks == 0 {
		t.RootHash = hashOf(nil)
		return t, nil
	}

	// Level 0: hash each data block, zero-padding the final partial block.
	cur := make([]byte, 0, nblocks*int64(spec.size))
	var padded [BlockSize]byte
	for i := int64(0); i < nblocks; i++ {
		lo := i * BlockSize
		hi := lo + BlockSize
		if hi > int64(len(data)) {
			hi = int64(len(data))
		}
		clear(padded[:])
		copy(padded[:], data[lo:hi])
		cur = append(cur, hashOf(padded[:])...)
	}
	if nblocks == 1 {
		t.RootHash = cur
		return t, nil
	}

	// Pack each level into 4096-byte blocks and hash them into the next
	// level until a single block remains; its hash is the root.
	for {
		nLevelBlocks := ceilDiv(int64(len(cur)), BlockSize)
		packed := make([]byte, nLevelBlocks*BlockSize)
		copy(packed, cur)
		t.Levels = append(t.Levels, packed)
		if nLevelBlocks == 1 {
			t.RootHash = hashOf(packed)
			return t, nil
		}
		next := make([]byte, 0, nLevelBlocks*int64(spec.size))
		for i := int64(0); i < nLevelBlocks; i++ {
			next = append(next, hashOf(packed[i*BlockSize:(i+1)*BlockSize])...)
		}
		cur = next
	}
}

// Descriptor returns the 256-byte integrity descriptor for this tree.
func (t *Tree) Descriptor() []byte {
	d := &Descriptor{
		Algorithm: t.Algorithm,
		Salt:      t.Salt,
		DataSize:  uint64(t.DataSize),
		RootHash:  t.RootHash,
	}
	return d.Marshal()
}

// Measurement returns the signed measurement: the hash of the descriptor.
func (t *Tree) Measurement() ([]byte, error) {
	return Measurement(t.Descriptor(), t.Algorithm)
}

// Serialize flattens the hash tree levels into a single blob. Level sizes
// are derivable from the descriptor, so no extra header is needed.
func (t *Tree) Serialize() []byte {
	var n int64
	for _, lvl := range t.Levels {
		n += int64(len(lvl))
	}
	blob := make([]byte, 0, n)
	for _, lvl := range t.Levels {
		blob = append(blob, lvl...)
	}
	return blob
}

// splitTreeBlob validates blob against the level layout implied by desc and
// returns the individual levels.
func splitTreeBlob(desc *Descriptor, blob []byte) ([][]byte, error) {
	spec, err := specFor(desc.Algorithm)
	if err != nil {
		return nil, err
	}
	sizes := levelSizes(desc.DataSize, spec.size)
	var total int64
	for _, s := range sizes {
		total += s
	}
	if int64(len(blob)) != total {
		return nil, ErrTreeCorrupt
	}
	levels := make([][]byte, len(sizes))
	off := int64(0)
	for i, s := range sizes {
		levels[i] = blob[off : off+s]
		off += s
	}
	return levels, nil
}

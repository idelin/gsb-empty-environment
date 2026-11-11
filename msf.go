package symbolcache

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// MSF 7.0 super-block layout, as used by modern PDB files.
// Reference: microsoft-pdb/PDB/msf/msf.cpp
const (
	msfMagicV7 = "Microsoft C/C++ MSF 7.00\r\n\x1aDS\x00\x00\x00"

	msfSuperBlockSize = 56
	msfMinBlockSize   = 512
	msfMaxBlockSize   = 4096
	msfInvalidBlock   = 0xffffffff
	msfInvalidStream  = 0xffffffff

	offBlockSize         = 32
	offNumBlocks         = 36
	offNumDirectoryBytes = 40
	offUnknown           = 44
	offBlockMapAddr      = 48
	offBlockMapSize      = 52
)

var (
	// ErrNotPDB means the bytes do not carry a recognizable MSF 7.0 super-block.
	ErrNotPDB = errors.New("symbolcache: not a valid MSF 7.0 PDB")
	// ErrCorruptPDB means the MSF container or its stream directory is malformed.
	ErrCorruptPDB = errors.New("symbolcache: corrupt PDB/MSF container")
	// ErrMismatch means the PDB identity (GUID and age) differs from the request.
	ErrMismatch = errors.New("symbolcache: PDB GUID/age does not match request")
)

// validatePDB fully parses the MSF 7.0 container of a freshly downloaded file
// and only succeeds when the stream directory is internally consistent and the
// PDB info stream (stream 1) carries the requested GUID and age. Nothing is
// allowed into the cache until this function returns nil.
func validatePDB(r io.ReaderAt, size int64, want Key) error {
	if size < msfSuperBlockSize {
		return fmt.Errorf("%w: file shorter than super-block", ErrNotPDB)
	}

	var sb [msfSuperBlockSize]byte
	if _, err := r.ReadAt(sb[:], 0); err != nil {
		return fmt.Errorf("%w: reading super-block: %v", ErrCorruptPDB, err)
	}
	if !bytes.Equal(sb[:len(msfMagicV7)], []byte(msfMagicV7)) {
		return fmt.Errorf("%w: bad MSF magic", ErrNotPDB)
	}

	blockSize := int(binary.LittleEndian.Uint32(sb[offBlockSize:]))
	if blockSize < msfMinBlockSize || blockSize > msfMaxBlockSize || blockSize&(blockSize-1) != 0 {
		return fmt.Errorf("%w: invalid block size %d", ErrCorruptPDB, blockSize)
	}
	numBlocks := int(binary.LittleEndian.Uint32(sb[offNumBlocks:]))
	if int64(numBlocks)*int64(blockSize) != size {
		return fmt.Errorf("%w: block count %d does not span file size %d",
			ErrCorruptPDB, numBlocks, size)
	}
	dirBytes := int(binary.LittleEndian.Uint32(sb[offNumDirectoryBytes:]))
	if binary.LittleEndian.Uint32(sb[offUnknown:]) != msfInvalidBlock {
		return fmt.Errorf("%w: unknown field must be 0xffffffff", ErrCorruptPDB)
	}
	blockMapAddr := int(binary.LittleEndian.Uint32(sb[offBlockMapAddr:]))
	if blockMapAddr <= 0 || blockMapAddr >= numBlocks {
		return fmt.Errorf("%w: block map address out of range", ErrCorruptPDB)
	}

	readBlock := func(index int) ([]byte, error) {
		if index < 0 || index >= numBlocks {
			return nil, fmt.Errorf("%w: block index %d out of range", ErrCorruptPDB, index)
		}
		buf := make([]byte, blockSize)
		if _, err := r.ReadAt(buf, int64(index)*int64(blockSize)); err != nil {
			return nil, fmt.Errorf("%w: reading block %d: %v", ErrCorruptPDB, index, err)
		}
		return buf, nil
	}

	// The block map lists the blocks that hold the stream directory.
	dirBlocks := ceilDiv(dirBytes, blockSize)
	blockMapBlocks := ceilDiv(dirBlocks*4, blockSize)
	if int(binary.LittleEndian.Uint32(sb[offBlockMapSize:])) != blockMapBlocks {
		return fmt.Errorf("%w: block map block count mismatch", ErrCorruptPDB)
	}
	if blockMapBlocks != 1 {
		// A PDB that large is never seen in practice; refuse rather than guess.
		return fmt.Errorf("%w: multi-block block maps unsupported", ErrCorruptPDB)
	}
	blockMap, err := readBlock(blockMapAddr)
	if err != nil {
		return err
	}

	dirBlockList := make([]int, dirBlocks)
	for i := range dirBlockList {
		idx := int(binary.LittleEndian.Uint32(blockMap[i*4:]))
		if idx < 0 || idx >= numBlocks {
			return fmt.Errorf("%w: directory block out of range", ErrCorruptPDB)
		}
		dirBlockList[i] = idx
	}

	// Read and concatenate the directory blocks.
	dir := make([]byte, 0, dirBlocks*blockSize)
	for _, idx := range dirBlockList {
		b, err := readBlock(idx)
		if err != nil {
			return err
		}
		dir = append(dir, b...)
	}
	if len(dir) < dirBytes {
		return fmt.Errorf("%w: truncated stream directory", ErrCorruptPDB)
	}
	dir = dir[:dirBytes]
	rd := &dirReader{data: dir}

	streamCount, err := rd.uint32()
	if err != nil {
		return err
	}
	if streamCount < 2 || streamCount > msfInvalidStream {
		return fmt.Errorf("%w: invalid stream count %d", ErrCorruptPDB, streamCount)
	}

	streamSizes := make([]int, streamCount)
	for i := range streamSizes {
		size32, err := rd.uint32()
		if err != nil {
			return err
		}
		if size32 == msfInvalidStream {
			streamSizes[i] = -1 // non-existent stream
			continue
		}
		streamSizes[i] = int(size32)
	}

	// Metadata blocks may be referenced by multiple header fields; real data
	// blocks may never appear in more than one stream.
	type blockOwner struct {
		kind     string
		metadata bool
	}
	used := make([]blockOwner, numBlocks)
	used[0] = blockOwner{kind: "superblock", metadata: true}
	used[blockMapAddr] = blockOwner{kind: "blockmap", metadata: true}
	for _, idx := range dirBlockList {
		used[idx] = blockOwner{kind: "directory", metadata: true}
	}

	readBlockList := func(n int) ([]int, error) {
		blocks := make([]int, n)
		for i := range blocks {
			v, err := rd.uint32()
			if err != nil {
				return nil, err
			}
			idx := int(v)
			if idx < 0 || idx >= numBlocks {
				return nil, fmt.Errorf("%w: stream block out of range", ErrCorruptPDB)
			}
			blocks[i] = idx
		}
		return blocks, nil
	}

	var pdbStreamBlocks []int
	for i, streamSize := range streamSizes {
		if streamSize < 0 {
			// Non-existent streams still carry an (empty) block list.
			continue
		}
		blocks, err := readBlockList(ceilDiv(streamSize, blockSize))
		if err != nil {
			return err
		}
		kind := fmt.Sprintf("stream%d", i)
		for _, idx := range blocks {
			if owner := used[idx]; owner.kind != "" {
				return fmt.Errorf("%w: block %d reused by %s and %s",
					ErrCorruptPDB, idx, kind, owner.kind)
			}
			used[idx] = blockOwner{kind: kind}
		}
		if i == 1 {
			pdbStreamBlocks = blocks
		}
	}
	if rd.remaining() != 0 {
		return fmt.Errorf("%w: %d trailing bytes in stream directory", ErrCorruptPDB, rd.remaining())
	}
	if streamSizes[1] < 28 {
		return fmt.Errorf("%w: PDB info stream missing", ErrCorruptPDB)
	}

	// Read the PDB info stream and verify the identity in its header.
	pdbStream := make([]byte, streamSizes[1])
	if err := readStream(pdbStream, r, pdbStreamBlocks, blockSize, numBlocks); err != nil {
		return err
	}
	age := binary.LittleEndian.Uint32(pdbStream[24:28])
	var guid [16]byte
	copy(guid[:], pdbStream[8:24])

	if age != want.Age || guid != want.GUID {
		return ErrMismatch
	}
	return nil
}

type dirReader struct {
	data []byte
	pos  int
}

func (d *dirReader) uint32() (uint32, error) {
	if d.pos+4 > len(d.data) {
		return 0, fmt.Errorf("%w: stream directory truncated", ErrCorruptPDB)
	}
	v := binary.LittleEndian.Uint32(d.data[d.pos:])
	d.pos += 4
	return v, nil
}

func (d *dirReader) remaining() int { return len(d.data) - d.pos }

func readStream(dst []byte, r io.ReaderAt, blocks []int, blockSize, numBlocks int) error {
	for i, idx := range blocks {
		if idx < 0 || idx >= numBlocks {
			return fmt.Errorf("%w: stream block %d out of range", ErrCorruptPDB, idx)
		}
		start := i * blockSize
		if start >= len(dst) {
			break
		}
		end := start + blockSize
		if end > len(dst) {
			end = len(dst)
		}
		if _, err := r.ReadAt(dst[start:end], int64(idx)*int64(blockSize)); err != nil {
			return fmt.Errorf("%w: reading stream block %d: %v", ErrCorruptPDB, idx, err)
		}
	}
	return nil
}

func ceilDiv(a, b int) int { return (a + b - 1) / b }

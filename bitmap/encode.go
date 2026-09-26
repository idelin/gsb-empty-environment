package bitmap

import (
	"encoding/binary"
	"errors"
	"io"
)

// On-disk encoding layout (all integers little-endian):
//
//	offset  size            field
//	0       4               magic "BMC1"
//	4       8               nbits   uint64
//	12      4               blocks  uint32  (directory length)
//	16      4               nonzero uint32  (non-empty blocks)
//	20      8 * blocks      directory: uint64 payload offset, 0 = empty block
//	20+8n   512 * nonzero   block payloads, in directory order
//
// Empty blocks cost exactly one 8-byte directory entry; a full 1M-bit
// bitmap with 3 dirty blocks encodes to 20 + 256*8 + 3*512 = 3604 bytes,
// not 128 KiB. EncodedSize reports this figure before anything is written.
const (
	magicBytes   = 4
	headerSize   = 20
	dirEntrySize = 8
)

var magic = [magicBytes]byte{'B', 'M', 'C', '1'}

// EncodedSize returns the exact number of bytes Encode will write.
// Note: like Count, it forces full materialization of a lazy node.
func (b *Bitmap) EncodedSize() int64 {
	nonzero := 0
	for i := range b.dir {
		if b.blockAt(i) != nil {
			nonzero++
		}
	}
	return int64(headerSize + len(b.dir)*dirEntrySize + nonzero*BlockBytes)
}

// Encode writes the bitmap in the layout documented above.
func (b *Bitmap) Encode(w io.Writer) error {
	nblocks := len(b.dir)
	blocks := make([]*block, nblocks)
	nonzero := 0
	for i := range b.dir {
		blocks[i] = b.blockAt(i)
		if blocks[i] != nil {
			nonzero++
		}
	}

	var hdr [headerSize]byte
	copy(hdr[:magicBytes], magic[:])
	binary.LittleEndian.PutUint64(hdr[4:12], uint64(b.nbits))
	binary.LittleEndian.PutUint32(hdr[12:16], uint32(nblocks))
	binary.LittleEndian.PutUint32(hdr[16:20], uint32(nonzero))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}

	off := uint64(headerSize + nblocks*dirEntrySize)
	var entry [dirEntrySize]byte
	for _, bl := range blocks {
		if bl == nil {
			binary.LittleEndian.PutUint64(entry[:], 0)
		} else {
			binary.LittleEndian.PutUint64(entry[:], off)
			off += BlockBytes
		}
		if _, err := w.Write(entry[:]); err != nil {
			return err
		}
	}

	var payload [BlockBytes]byte
	for _, bl := range blocks {
		if bl == nil {
			continue
		}
		for i, word := range bl.words {
			binary.LittleEndian.PutUint64(payload[i*8:], word)
		}
		if _, err := w.Write(payload[:]); err != nil {
			return err
		}
	}
	return nil
}

// Decode reads a bitmap produced by Encode. The result is a dense bitmap
// with refcount 1.
func Decode(r io.Reader) (*Bitmap, error) {
	var hdr [headerSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	if string(hdr[:magicBytes]) != string(magic[:]) {
		return nil, errors.New("bitmap: bad magic")
	}
	nbits := binary.LittleEndian.Uint64(hdr[4:12])
	nblocks := binary.LittleEndian.Uint32(hdr[12:16])
	if nbits > uint64(1)<<40 || uint64(nblocks) != uint64(numBlocks(int(nbits))) {
		return nil, errors.New("bitmap: corrupt header")
	}

	b := New(int(nbits))
	dir := make([]byte, int(nblocks)*dirEntrySize)
	if _, err := io.ReadFull(r, dir); err != nil {
		return nil, err
	}
	var payload [BlockBytes]byte
	for i := 0; i < int(nblocks); i++ {
		if binary.LittleEndian.Uint64(dir[i*dirEntrySize:]) == 0 {
			continue
		}
		if _, err := io.ReadFull(r, payload[:]); err != nil {
			return nil, err
		}
		bl := newBlock()
		for j := 0; j < BlockWords; j++ {
			bl.words[j] = binary.LittleEndian.Uint64(payload[j*8:])
		}
		b.dir[i] = bl
	}
	return b, nil
}

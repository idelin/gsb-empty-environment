package netsec

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"io/fs"
)

// Verifier authenticates a file against a trusted descriptor measurement and
// verifies every block it returns against the descriptor's Merkle root.
type Verifier struct {
	source      io.ReaderAt
	size        int64
	desc        *Descriptor
	measurement []byte
	tree        *merkleTree
}

// NewVerifier checks the descriptor measurement, the source length and the
// Merkle root before returning a reader. If size is omitted it is taken from
// the descriptor. When size is supplied it must match the descriptor.
func NewVerifier(source io.ReaderAt, d *Descriptor, measurement []byte, size ...int64) (*Verifier, error) {
	if source == nil {
		return nil, ErrEmptySource
	}
	if d == nil {
		return nil, ErrInvalidRootHash
	}
	if err := d.VerifyMeasurement(measurement); err != nil {
		return nil, err
	}

	expected := int64(d.DataSize)
	if d.DataSize > uint64(^uint64(0)>>1) {
		return nil, ErrSizeMismatch
	}
	if len(size) > 1 {
		return nil, fmt.Errorf("%w: multiple sizes supplied", ErrSizeMismatch)
	}
	if len(size) == 1 && (size[0] < 0 || size[0] != expected) {
		return nil, ErrSizeMismatch
	}
	if actual, ok, err := sourceSize(source); err != nil {
		return nil, err
	} else if ok && actual != expected {
		return nil, ErrSizeMismatch
	}

	tree, err := buildMerkleTree(d.DataSize, d.Algorithm, d.Salt, func(index int) ([]byte, error) {
		return readSourceBlock(source, d.DataSize, index)
	})
	if err != nil {
		return nil, err
	}
	if err := tree.verify(d); err != nil {
		return nil, err
	}

	v := &Verifier{
		source:      source,
		size:        expected,
		desc:        d.clone(),
		measurement: append([]byte(nil), measurement...),
		tree:        tree,
	}
	if err := v.checkCurrentSize(); err != nil {
		return nil, err
	}
	return v, nil
}

// NewVerifierWithData is a byte-slice convenience wrapper.
func NewVerifierWithData(data []byte, d *Descriptor, measurement []byte) (*Verifier, error) {
	return NewVerifier(bytesReaderAt(data), d, measurement, int64(len(data)))
}

// ReadBlock verifies and returns one block from a byte slice.
func ReadBlock(data []byte, index int, d *Descriptor, measurement []byte) ([]byte, error) {
	v, err := NewVerifierWithData(data, d, measurement)
	if err != nil {
		return nil, err
	}
	return v.ReadBlock(index)
}

// VerifyBlock verifies one block from a byte slice.
func VerifyBlock(data []byte, index int, d *Descriptor, measurement []byte) error {
	v, err := NewVerifierWithData(data, d, measurement)
	if err != nil {
		return err
	}
	return v.VerifyBlock(index)
}

// VerifyData verifies the trusted measurement, the exact data length and the
// complete Merkle tree in one pass.
func VerifyData(data []byte, d *Descriptor, measurement []byte) error {
	if d == nil {
		return ErrInvalidRootHash
	}
	if err := d.VerifyMeasurement(measurement); err != nil {
		return err
	}
	if uint64(len(data)) != d.DataSize {
		return ErrSizeMismatch
	}
	root, err := rootHashFromData(data, d.Salt, d.Algorithm)
	if err != nil {
		return err
	}
	if len(root) != len(d.RootHash) || subtle.ConstantTimeCompare(root, d.RootHash) != 1 {
		return ErrRootHashMismatch
	}
	return nil
}

// VerifyFile is the io.ReaderAt counterpart of VerifyData.
func VerifyFile(source io.ReaderAt, size int64, d *Descriptor, measurement []byte) error {
	if source == nil {
		return ErrEmptySource
	}
	if d == nil {
		return ErrInvalidRootHash
	}
	if err := d.VerifyMeasurement(measurement); err != nil {
		return err
	}
	if size < 0 || uint64(size) != d.DataSize {
		return ErrSizeMismatch
	}
	tree, err := buildMerkleTree(d.DataSize, d.Algorithm, d.Salt, func(index int) ([]byte, error) {
		return readSourceBlock(source, d.DataSize, index)
	})
	if err != nil {
		return err
	}
	if err := tree.verify(d); err != nil {
		return err
	}
	return probeEnd(source, d.DataSize)
}

// Descriptor returns a copy of the authenticated descriptor.
func (v *Verifier) Descriptor() *Descriptor {
	return v.desc.clone()
}

// Measurement returns the authenticated descriptor measurement.
func (v *Verifier) Measurement() []byte {
	return append([]byte(nil), v.measurement...)
}

// DataSize returns the expected data length.
func (v *Verifier) DataSize() int64 {
	return v.size
}

// NumBlocks returns the number of 4096-byte data blocks in the file.
func (v *Verifier) NumBlocks() int {
	return v.tree.numDataBlocks
}

// ReadBlock returns the requested data block after checking its hash and
// recomputing its path to the authenticated Merkle root. The last block is
// returned with its real length.
func (v *Verifier) ReadBlock(index int) ([]byte, error) {
	block, err := v.readVerifiedBlock(index)
	if err != nil {
		return nil, err
	}
	start := int64(index) * BlockSize
	remaining := v.size - start
	if int64(len(block)) > remaining {
		block = block[:remaining]
	}
	return append([]byte(nil), block...), nil
}

// ReadBlockPadded returns the requested data block padded to 4096 bytes with
// zeros.
func (v *Verifier) ReadBlockPadded(index int) ([]byte, error) {
	block, err := v.readVerifiedBlock(index)
	if err != nil {
		return nil, err
	}
	out := make([]byte, BlockSize)
	copy(out, block)
	return out, nil
}

// ReadAt implements io.ReaderAt. All touched blocks are authenticated before
// any of their bytes are returned.
func (v *Verifier) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, ErrBlockIndex
	}
	if err := v.checkCurrentSize(); err != nil {
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if off >= v.size {
		return 0, io.EOF
	}

	truncated := int64(len(p)) > v.size-off
	want := len(p)
	if truncated {
		want = int(v.size - off)
	}
	written := 0
	for written < want {
		abs := off + int64(written)
		index := int(abs / BlockSize)
		within := int(abs % BlockSize)
		block, err := v.readVerifiedBlock(index)
		if err != nil {
			return written, err
		}
		start := int64(index) * BlockSize
		remaining := int(v.size - start)
		if remaining < len(block) {
			block = block[:remaining]
		}
		if within >= len(block) {
			return written, ErrCorruptBlock
		}
		n := copy(p[written:want], block[within:])
		if n == 0 {
			return written, ErrCorruptBlock
		}
		written += n
	}
	if truncated {
		return written, io.EOF
	}
	return written, nil
}

// ReadAll verifies the complete file and returns its bytes.
func (v *Verifier) ReadAll() ([]byte, error) {
	if err := v.checkCurrentSize(); err != nil {
		return nil, err
	}
	out := make([]byte, v.size)
	if err := readAtFull(v.source, out, 0); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, ErrSizeMismatch
		}
		return nil, err
	}
	if err := VerifyData(out, v.desc, v.measurement); err != nil {
		return nil, err
	}
	if err := v.checkCurrentSize(); err != nil {
		return nil, err
	}
	return out, nil
}

// Verify re-reads the source and authenticates the complete Merkle tree. It is
// useful for detecting changes made after the Verifier was opened.
func (v *Verifier) Verify() error {
	if err := v.checkCurrentSize(); err != nil {
		return err
	}
	tree, err := buildMerkleTree(v.desc.DataSize, v.desc.Algorithm, v.desc.Salt, func(index int) ([]byte, error) {
		return readSourceBlock(v.source, v.desc.DataSize, index)
	})
	if err != nil {
		return err
	}
	if err := tree.verify(v.desc); err != nil {
		return err
	}
	return v.checkCurrentSize()
}

// VerifyBlock authenticates one block without returning its contents.
func (v *Verifier) VerifyBlock(index int) error {
	_, err := v.readVerifiedBlock(index)
	return err
}

func (v *Verifier) readVerifiedBlock(index int) ([]byte, error) {
	if err := v.checkCurrentSize(); err != nil {
		return nil, err
	}
	if index < 0 || index >= v.tree.numDataBlocks {
		return nil, fmt.Errorf("%w: %d", ErrBlockIndex, index)
	}
	if err := v.tree.verify(v.desc); err != nil {
		return nil, err
	}

	block, err := readSourceBlock(v.source, v.desc.DataSize, index)
	if err != nil {
		return nil, err
	}
	h, err := v.desc.Algorithm.newHash()
	if err != nil {
		return nil, err
	}
	got := hashBlock(h, v.tree.saltPrefix, block, v.desc.Algorithm.DigestSize())
	want := v.tree.levels[0][index]
	if len(got) != len(want) || subtle.ConstantTimeCompare(got, want) != 1 {
		return nil, fmt.Errorf("%w: block %d", ErrCorruptBlock, index)
	}
	if _, err := v.tree.verifyBlock(index); err != nil {
		return nil, err
	}
	return block, nil
}

func (v *Verifier) checkCurrentSize() error {
	if actual, ok, err := sourceSize(v.source); err != nil {
		return err
	} else if ok && actual != v.size {
		return ErrSizeMismatch
	}
	return probeEnd(v.source, uint64(v.size))
}

func sourceSize(source io.ReaderAt) (int64, bool, error) {
	if sizer, ok := source.(interface{ Size() int64 }); ok {
		return sizer.Size(), true, nil
	}
	if statter, ok := source.(interface {
		Stat() (fs.FileInfo, error)
	}); ok {
		info, err := statter.Stat()
		if err != nil {
			return 0, false, err
		}
		return info.Size(), true, nil
	}
	return 0, false, nil
}

func probeEnd(source io.ReaderAt, expected uint64) error {
	buf := make([]byte, 1)
	n, err := source.ReadAt(buf, int64(expected))
	if n > 0 {
		return ErrSizeMismatch
	}
	if err != nil && err != io.EOF {
		return err
	}
	return nil
}

func readSourceBlock(source io.ReaderAt, dataSize uint64, index int) ([]byte, error) {
	if index < 0 {
		return nil, fmt.Errorf("%w: %d", ErrBlockIndex, index)
	}
	start := uint64(index) * BlockSize
	if start >= dataSize {
		return nil, fmt.Errorf("%w: %d", ErrBlockIndex, index)
	}
	length := uint64(BlockSize)
	if remaining := dataSize - start; remaining < length {
		length = remaining
	}
	block := make([]byte, length)
	if err := readAtFull(source, block, int64(start)); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, ErrSizeMismatch
		}
		return nil, err
	}
	return block, nil
}

func (d *Descriptor) clone() *Descriptor {
	if d == nil {
		return nil
	}
	out := *d
	out.RootHash = append([]byte(nil), d.RootHash...)
	out.Salt = append([]byte(nil), d.Salt...)
	return &out
}

type byteReaderAt struct {
	data []byte
}

func bytesReaderAt(data []byte) io.ReaderAt {
	return &byteReaderAt{data: data}
}

func (r *byteReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, ErrBlockIndex
	}
	if off >= int64(len(r.data)) {
		return 0, io.EOF
	}
	n := copy(p, r.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (r *byteReaderAt) Size() int64 {
	return int64(len(r.data))
}

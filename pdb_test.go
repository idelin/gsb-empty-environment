package symbolcache

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"sync"
)

// buildPDB assembles a minimal but spec-correct MSF 7.0 PDB containing an
// empty stream 0 and the PDB info stream (stream 1). mutate can corrupt the
// finished image so tests exercise the rejection paths.
func buildPDB(guid [16]byte, age uint32, blockSize int, mutate func([]byte)) []byte {
	info := infoStream(guid, age)
	streams := [][]byte{nil, info}

	// The full directory (count + sizes + block lists) must fit in the blocks
	// whose count is derived before the layout is known, so iterate.
	dirSize := 4 + 4*len(streams)
	for _, data := range streams {
		if data != nil {
			dirSize += 4 * ceilDiv(len(data), blockSize)
		}
	}
	dirBlocks := ceilDiv(dirSize, blockSize)
	blockMapBlocks := ceilDiv(dirBlocks*4, blockSize)

	// Layout: block 0 super-block, directory blocks, block map block(s),
	// then the data blocks of every stream. The block map address is simply
	// the block immediately after the directory.
	blockMapAddr := 1 + dirBlocks
	next := blockMapAddr + blockMapBlocks
	streamBlocks := make([][]int, len(streams))
	for i, data := range streams {
		if data == nil {
			continue
		}
		for j := 0; j < ceilDiv(len(data), blockSize); j++ {
			streamBlocks[i] = append(streamBlocks[i], next)
			next++
		}
	}
	numBlocks := next

	dirBlocksList := make([]int, dirBlocks)
	for i := range dirBlocksList {
		dirBlocksList[i] = 1 + i
	}

	img := make([]byte, numBlocks*blockSize)

	// Super-block.
	copy(img[0:], []byte(msfMagicV7))
	put32(img, offBlockSize, uint32(blockSize))
	put32(img, offNumBlocks, uint32(numBlocks))
	put32(img, offNumDirectoryBytes, uint32(dirSize))
	put32(img, offUnknown, msfInvalidBlock)
	put32(img, offBlockMapAddr, uint32(blockMapAddr))
	put32(img, offBlockMapSize, uint32(blockMapBlocks))

	// Block map: indices of the blocks holding the directory.
	off := blockMapAddr * blockSize
	for _, idx := range dirBlocksList {
		put32(img, off, uint32(idx))
		off += 4
	}

	// Directory content: stream count, sizes, then block lists.
	var dir bytes.Buffer
	writeU32(&dir, uint32(len(streams)))
	for _, data := range streams {
		if data == nil {
			writeU32(&dir, msfInvalidStream)
		} else {
			writeU32(&dir, uint32(len(data)))
		}
	}
	for i := range streams {
		for _, idx := range streamBlocks[i] {
			writeU32(&dir, uint32(idx))
		}
	}
	dirBytes := dir.Bytes()
	if len(dirBytes) != dirSize {
		panic("directory size mismatch")
	}
	for i, b := range dirBytes {
		dirBlock := i / blockSize
		within := i % blockSize
		img[dirBlocksList[dirBlock]*blockSize+within] = b
	}

	// Stream data blocks.
	for i, data := range streams {
		for j, idx := range streamBlocks[i] {
			start := j * blockSize
			end := start + blockSize
			if end > len(data) {
				end = len(data)
			}
			copy(img[idx*blockSize:], data[start:end])
		}
	}

	if mutate != nil {
		mutate(img)
	}
	return img
}

func directorySize(streams [][]byte, blockSize int) int {
	size := 4 + 4*len(streams)
	for _, data := range streams {
		if data != nil {
			size += 4 * ceilDiv(len(data), blockSize)
		}
	}
	return size
}

// infoStream builds the 28-byte PDB info stream header.
func infoStream(guid [16]byte, age uint32) []byte {
	b := make([]byte, 28)
	put32(b, 0, 20000404) // PDB 7.0 version
	put32(b, 4, 0)        // signature
	copy(b[8:24], guid[:])
	put32(b, 24, age)
	return b
}

func put32(b []byte, off int, v uint32) { binary.LittleEndian.PutUint32(b[off:], v) }

func writeU32(w *bytes.Buffer, v uint32) {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], v)
	w.Write(buf[:])
}

// fakeMirror is a scriptable in-memory Mirror.
type fakeMirror struct {
	name string

	mu      sync.Mutex
	calls   int
	payload []byte

	// failAfter, when > 0, truncates the write after failAfter bytes and
	// returns an error, simulating a lossy mirror.
	failAfter int

	// started/release let tests drive and observe an in-flight download.
	started chan struct{}
	release chan struct{}
}

func (m *fakeMirror) FetchPDB(ctx context.Context, idx string, w io.Writer) error {
	m.mu.Lock()
	m.calls++
	payload := append([]byte(nil), m.payload...)
	failAfter := m.failAfter
	started, release := m.started, m.release
	m.mu.Unlock()

	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	data := payload
	if failAfter > 0 && failAfter < len(data) {
		data = data[:failAfter]
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if failAfter > 0 && failAfter < len(payload) {
		return errors.New("fakeMirror: connection reset by peer")
	}
	return ctx.Err()
}

func (m *fakeMirror) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

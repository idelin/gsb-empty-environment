package avroocf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
)

// NextBlock advances to the next object block. It returns the number of
// objects in the block, or io.EOF when the stream is cleanly exhausted.
func (r *Reader) NextBlock() (int64, error) {
	if r.closed {
		return 0, fmt.Errorf("%w: reader closed", ErrMalformed)
	}
	if err := r.finishBlock(); err != nil {
		return 0, err
	}

	peek := make([]byte, 1)
	if _, err := io.ReadFull(r.input, peek); err == io.EOF || err == io.ErrUnexpectedEOF {
		return 0, io.EOF
	} else if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	count, err := decodeCount(r.input, peek[0])
	if err != nil {
		return 0, fmt.Errorf("%w: block count: %v", ErrMalformed, err)
	}
	if count == 0 {
		return 0, fmt.Errorf("%w: zero-length block", ErrBlockCount)
	}

	var declaredSize int64
	if count < 0 {
		// Negative count: the absolute value is the object count and the
		// following long is the block's byte size, per the Avro spec.
		count = -count
		declaredSize, err = readLong(r.input)
		if err != nil {
			return 0, fmt.Errorf("%w: block size: %v", ErrMalformed, err)
		}
		if declaredSize < 0 {
			return 0, fmt.Errorf("%w: negative block size", ErrBlockCount)
		}
	}

	if count > r.config.MaxBlockObjects {
		return 0, fmt.Errorf("%w: %d objects exceed limit %d",
			ErrBlockCount, count, r.config.MaxBlockObjects)
	}

	if declaredSize > 0 {
		if declaredSize > r.config.MaxBlockBytes {
			return 0, fmt.Errorf("%w: block size %d exceeds limit %d",
				ErrBlockCount, declaredSize, r.config.MaxBlockBytes)
		}
		if declaredSize > r.input.remaining {
			return 0, fmt.Errorf("%w: declared block size %d exceeds %d remaining bytes",
				ErrBlockCount, declaredSize, r.input.remaining)
		}
	}

	availableForBlock := r.input.remaining - syncSize
	if availableForBlock < 0 {
		return 0, fmt.Errorf("%w: block truncated before sync marker", ErrBlockCount)
	}
	deflateEnabled := string(r.metadata["avro.codec"]) == "deflate"
	// Compressed blocks do not have a linear size/count relationship, so the
	// minimum-size cross check applies to raw blocks only; the decompression
	// cap protects the deflate path instead.
	if r.blockMinSize > 0 && !deflateEnabled && count > availableForBlock/r.blockMinSize {
		return 0, fmt.Errorf("%w: %d objects cannot fit in %d remaining bytes",
			ErrBlockCount, count, availableForBlock)
	}

	limit := availableForBlock
	if declaredSize > 0 {
		limit = declaredSize
	}
	rawBlock := r.input.child(limit)
	r.rawBlock = rawBlock
	r.block = rawBlock
	if deflateEnabled {
		zlibReader, err := zlib.NewReader(&limitedDecompressor{
			source: rawBlock,
			limit:  r.config.MaxDecompressedBlockBytes,
		})
		if err != nil {
			return 0, err
		}
		r.zlib = zlibReader
		r.block = &countedReader{
			source:    zlibReader,
			remaining: r.config.MaxDecompressedBlockBytes,
		}
	}

	r.blockObjects = count
	r.objectRead = 0
	return count, nil
}

func (r *Reader) finishBlock() error {
	if r.block == nil {
		return nil
	}
	if r.objectRead < r.blockObjects {
		return fmt.Errorf("%w: block advanced with %d of %d objects unread",
			ErrMalformed, r.blockObjects-r.objectRead, r.blockObjects)
	}
	if r.zlib != nil {
		if _, err := io.Copy(io.Discard, r.zlib); err != nil {
			return fmt.Errorf("%w: deflate tail: %v", ErrMalformed, err)
		}
		if err := r.zlib.Close(); err != nil {
			return err
		}
		r.zlib = nil
	}
	if _, err := io.Copy(io.Discard, r.block); err != nil {
		return fmt.Errorf("%w: draining block: %v", ErrMalformed, err)
	}
	if _, err := io.Copy(io.Discard, r.rawBlock); err != nil {
		return fmt.Errorf("%w: draining raw block: %v", ErrMalformed, err)
	}
	if r.rawBlock.remaining != 0 {
		return fmt.Errorf("%w: %d trailing block bytes after %d objects",
			ErrBlockCount, r.rawBlock.remaining, r.blockObjects)
	}

	marker, err := readFull(r.input, syncSize)
	if err != nil {
		return fmt.Errorf("%w: short sync marker: %v", ErrSyncMarker, err)
	}
	if !bytes.Equal(marker, r.sync[:]) {
		return ErrSyncMarker
	}
	r.block = nil
	r.rawBlock = nil
	r.blockObjects = 0
	r.objectRead = 0
	return nil
}

// limitedDecompressor turns a decompression bomb into a bounded error.
type limitError struct {
	err error
}

func (e limitError) Error() string {
	return e.err.Error()
}

func (e limitError) Unwrap() error {
	return e.err
}

func (e limitError) Is(target error) bool {
	return target == ErrBlockCount
}

type limitedDecompressor struct {
	source io.Reader
	limit  int64
	read   int64
}

func (l *limitedDecompressor) Read(p []byte) (int, error) {
	if l.read >= l.limit {
		return 0, limitError{err: fmt.Errorf("%w: decompressed block exceeds %d bytes",
			ErrBlockCount, l.limit)}
	}
	if int64(len(p)) > l.limit-l.read {
		p = p[:l.limit-l.read]
	}
	n, err := l.source.Read(p)
	l.read += int64(n)
	return n, err
}

package netsec

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// copyBufferSize matches the standard library's default and keeps one
// partition's memory footprint bounded regardless of declared size.
const copyBufferSize = 32 * 1024

// stagePartition decompresses one partition into a private file inside the
// staging directory, enforcing the exact declared length while streaming and
// verifying the digest before returning. The target device is never involved:
// the decompressor only receives a writer wrapping the staging file.
func stagePartition(ctx context.Context, stagingDir string, part Partition, decompress Decompressor) (*stagedItem, error) {
	payload, err := part.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: partition %q: %v", ErrPayloadOpen, part.Name, err)
	}

	stream, err := decompress(payload)
	if err != nil {
		_ = payload.Close()
		return nil, fmt.Errorf("%w: partition %q: %v", ErrDecompressor, part.Name, err)
	}

	path := filepath.Join(stagingDir, sanitizeName(part.Name))
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = stream.Close()
		_ = payload.Close()
		return nil, fmt.Errorf("%w: partition %q: %v", ErrStaging, part.Name, err)
	}

	item := &stagedItem{part: part, path: path, file: file}

	hasher, err := newHasher(part.HashAlgo)
	if err != nil {
		item.abort(stream, payload)
		return nil, fmt.Errorf("%w: partition %q: %v", err, part.Name, err)
	}

	// The limited writer makes it impossible for a malicious or broken stream
	// to emit more than Dest.Length bytes into staging.
	limited := &limitedWriter{
		remaining: part.Dest.Length,
		dst:       io.MultiWriter(file, hasher),
	}
	written, copyErr := copyWithContext(ctx, limited, stream)

	// Drain-close surfaces trailer errors (gzip/zlib CRC and truncation) that
	// Read may not have reported yet.
	closeErr := stream.Close()
	payloadErr := payload.Close()

	if copyErr != nil {
		item.abort(nil, nil)
		return nil, wrapStageError(part.Name, copyErr)
	}
	if closeErr != nil {
		item.abort(nil, nil)
		return nil, fmt.Errorf("%w: partition %q: %v", ErrDecompressor, part.Name, closeErr)
	}
	if payloadErr != nil {
		item.abort(nil, nil)
		return nil, fmt.Errorf("%w: partition %q: %v", ErrPayloadOpen, part.Name, payloadErr)
	}

	if written != part.Dest.Length {
		item.abort(nil, nil)
		return nil, fmt.Errorf("%w: partition %q: got %d bytes, want %d",
			ErrLengthMismatch, part.Name, written, part.Dest.Length)
	}

	actual := hasher.Sum(nil)
	if subtle.ConstantTimeCompare(actual, part.ExpectedHash) != 1 {
		item.abort(nil, nil)
		return nil, fmt.Errorf("%w: partition %q", ErrDigestMismatch, part.Name)
	}

	if err := file.Sync(); err != nil {
		item.abort(nil, nil)
		return nil, fmt.Errorf("%w: partition %q: %v", ErrStaging, part.Name, err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		item.abort(nil, nil)
		return nil, fmt.Errorf("%w: partition %q: %v", ErrStaging, part.Name, err)
	}

	item.size = written
	item.hash = actual
	return item, nil
}

func (item *stagedItem) abort(extra ...io.Closer) {
	_ = item.file.Close()
	_ = os.Remove(item.path)
	for _, closer := range extra {
		if closer != nil {
			_ = closer.Close()
		}
	}
}

func wrapStageError(name string, err error) error {
	// Length violations raised by limitedWriter keep their sentinel so callers
	// can distinguish them from generic decompressor failures.
	if errors.Is(err, ErrLengthMismatch) {
		return fmt.Errorf("%w: partition %q", ErrLengthMismatch, name)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w: partition %q: %v", ErrDecompressor, name, err)
}

// copyWithContext streams src into dst in bounded chunks while honoring
// context cancellation. It returns the exact number of bytes written.
func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, copyBufferSize)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			written, writeErr := dst.Write(buf[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return total, nil
			}
			return total, readErr
		}
	}
}

// sanitizeName keeps staging file names flat and free of path separators, so a
// manifest-provided name can never escape the staging directory.
func sanitizeName(name string) string {
	out := make([]byte, 0, len(name))
	for index := 0; index < len(name); index++ {
		ch := name[index]
		switch {
		case ch >= 'a' && ch <= 'z',
			ch >= 'A' && ch <= 'Z',
			ch >= '0' && ch <= '9',
			ch == '-', ch == '_', ch == '.':
			out = append(out, ch)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "partition"
	}
	return string(out)
}

// Package archive implements the archive importer that expands
// RLE-compressed entries through the streaming rle decoder.
package archive

import (
	"context"
	"errors"
	"io"

	"example.com/archive/rle"
)

// ErrEntryTooLarge reports a decoded entry exceeding the allowed size.
var ErrEntryTooLarge = errors.New("archive: decoded entry exceeds maximum size")

// ImportEntry decodes one RLE entry from src and returns the expanded
// payload. The decoded size is capped at maxSize bytes; the cap is
// enforced incrementally, so an oversized or corrupt count never causes
// a matching allocation. Cancelling ctx aborts the import promptly.
func ImportEntry(ctx context.Context, src io.Reader, maxSize uint64) ([]byte, error) {
	dec := rle.NewDecoder(ctx, src, rle.Options{
		MaxOutput: maxSize,
		MaxBuffer: 64 << 10,
	})
	var out []byte
	buf := make([]byte, 64<<10)
	for {
		n, err := dec.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		switch err {
		case nil:
		case io.EOF:
			return out, nil
		case rle.ErrOutputLimit:
			return nil, ErrEntryTooLarge
		default:
			return nil, err
		}
	}
}

package blkx

import (
	"fmt"
	"io"
	"os"
)

// Transcode streams a format-A source into a format-B target without
// materializing the whole payload: each validated source block is written
// out immediately, so memory stays bounded to one block and backpressure
// propagates naturally from dst to src.
//
// Metadata and the declared length are carried over verbatim. The target's
// complete marker is written only after the source has been fully
// validated (per-block checksums, final checksum, declared length), so a
// bad input never publishes a complete marker.
func Transcode(dst io.Writer, src io.Reader) error {
	ar, err := NewAReader(src)
	if err != nil {
		return err
	}
	bw, err := NewBWriter(dst, ar.Meta, ar.DeclaredLen)
	if err != nil {
		return err
	}
	for {
		blk, err := ar.NextBlock()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if err := bw.WriteBlock(blk); err != nil {
			return err
		}
	}
	return bw.Finish()
}

// TranscodeFile transcodes src into dstPath atomically: the stream is
// written to a temporary sibling file and renamed into place only after
// the complete marker is durably written. On any failure the uncommitted
// tail (the temp file) is removed and dstPath is left untouched.
func TranscodeFile(dstPath string, src io.Reader) (err error) {
	tmp := dstPath + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("blkx: creating temp target: %w", err)
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if err = Transcode(f, src); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return fmt.Errorf("blkx: syncing target: %w", err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("blkx: closing target: %w", err)
	}
	if err = os.Rename(tmp, dstPath); err != nil {
		return fmt.Errorf("blkx: committing target: %w", err)
	}
	return nil
}

package rangedl

import "os"

// preallocate guarantees the partial file can receive writes at offsets up to
// size-1. Sparse truncation is portable and sufficient for crash-safe range
// downloads because every byte is verified before finalization.
func preallocate(f *os.File, size int64) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() >= size {
		return nil
	}
	return f.Truncate(size)
}

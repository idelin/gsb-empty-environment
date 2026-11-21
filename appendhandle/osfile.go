package appendhandle

import (
	"errors"
	"io"
	"os"
)

var errHoleUnsupported = errors.New("appendhandle: hole punching unsupported on this platform")

// OSFile adapts an *os.File to the [File] contract. On platforms without a
// real hole-punching primitive PunchHole zero-fills the range so reads still
// observe explicit zeros and offsets are never reused.
type OSFile struct {
	*os.File
}

// NewOSFile wraps an open regular file.
func NewOSFile(f *os.File) *OSFile { return &OSFile{File: f} }

// PunchHole deallocates [off, off+length) without changing the file size.
func (f *OSFile) PunchHole(off, length int64) error {
	if off < 0 || length < 0 {
		return ErrNegativeOffset
	}
	if length == 0 {
		return nil
	}
	if err := punchHole(f.File.Fd(), off, length); err != nil {
		if !holeUnsupported(err) {
			return err
		}
		return zeroFill(f.File, off, length)
	}
	return nil
}

// OpenVolume opens (or creates) path and treats its current size as the
// committed prefix. The file is opened O_RDWR so hole punches and syncs are
// available.
func OpenVolume(path string, opts *Options) (*Volume, *OSFile, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	merged := Options{SyncOnCommit: true, InitialSize: st.Size()}
	if opts != nil {
		merged.SyncOnCommit = opts.SyncOnCommit
		if opts.InitialSize > 0 {
			merged.InitialSize = opts.InitialSize
		}
	}
	if merged.InitialSize > st.Size() {
		_ = f.Close()
		return nil, nil, ErrNegativeOffset
	}
	v, err := Open(NewOSFile(f), &merged)
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return v, NewOSFile(f), nil
}

func zeroFill(f *os.File, off, length int64) error {
	zeros := make([]byte, 64*1024)
	remaining := length
	for remaining > 0 {
		chunk := int64(len(zeros))
		if remaining < chunk {
			chunk = remaining
		}
		if _, err := f.WriteAt(zeros[:chunk], off); err != nil {
			return err
		}
		off += chunk
		remaining -= chunk
	}
	return nil
}

var _ io.WriterAt = (*OSFile)(nil)

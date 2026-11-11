package safezip

import (
	"errors"
	"io"
)

// Store is the object-storage abstraction required by Open. Object stores
// that support ranged GETs naturally implement io.ReaderAt; Size reports the
// authoritative object length used for all bounds checks.
type Store interface {
	io.ReaderAt
	Size() int64
}

// BytesStore adapts an in-memory byte slice to Store. It is handy for tests
// and for callers that already hold a full snapshot of an object.
type BytesStore struct {
	B []byte
}

func (s BytesStore) Size() int64 { return int64(len(s.B)) }

func (s BytesStore) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, ErrFormat
	}
	if off >= int64(len(s.B)) {
		return 0, io.EOF
	}
	n := copy(p, s.B[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// readFullAt fills p entirely from st at off, mapping short reads to
// ErrTruncated so callers never silently accept partial metadata.
func readFullAt(st io.ReaderAt, off int64, p []byte) error {
	if off < 0 {
		return ErrFormat
	}
	view := &sectionView{r: st, off: off, p: p}
	if _, err := io.ReadFull(view, p); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return ErrTruncated
		}
		return err
	}
	return nil
}

// sectionView issues a single ranged ReadAt per Read call over a fixed
// window, so fixed-size metadata can be loaded without a SectionReader.
type sectionView struct {
	r   io.ReaderAt
	off int64
	p   []byte
}

func (v *sectionView) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(p) > len(v.p) {
		p = p[:len(v.p)]
	}
	n, err := v.r.ReadAt(p, v.off)
	v.off += int64(n)
	v.p = v.p[n:]
	return n, err
}

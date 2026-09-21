package recordio

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
)

// Index layout:
//
//	"IDX1" ; digest sha256[32] ; count u32 LE ; offset u64 LE * count
//
// The digest covers the whole indexed source file, so an index can never be
// silently used against a modified or different file.
const indexMagic = "IDX1"

// Index maps record ordinal -> byte offset in the source file.
type Index struct {
	Digest  [32]byte // SHA-256 of the indexed source file
	Offsets []uint64
}

// Len returns the number of indexed records.
func (i *Index) Len() int { return len(i.Offsets) }

// BuildIndex scans r in fixed read chunks, hashes every byte and records the
// offset of each frame. A header-only source yields a valid empty index; a
// zero-byte or truncated source is bad data.
func BuildIndex(r io.Reader, maxRecordSize int) (*Index, error) {
	h := sha256.New()
	p := NewParser(maxRecordSize)
	idx := &Index{}

	buf := make([]byte, 32*1024)
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
			p.Feed(buf[:n])
			err := p.Drain(func(_ *Record, offset int64) error {
				idx.Offsets = append(idx.Offsets, uint64(offset))
				return nil
			})
			if err != ErrNeedMoreData && err != nil {
				return nil, err
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, rerr
		}
	}

	if !p.Done() {
		if !p.started && p.err == nil {
			return nil, fmt.Errorf("%w: empty source has no stream magic", ErrBadData)
		}
		return nil, fmt.Errorf("%w: stream ends mid-record (not on a frame boundary)", ErrBadData)
	}
	idx.Digest = [32]byte(h.Sum(nil))
	return idx, nil
}

// DigestOf streams r and returns its SHA-256. Use it with VerifySource.
func DigestOf(r io.Reader) ([32]byte, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return [32]byte{}, err
	}
	return [32]byte(h.Sum(nil)), nil
}

// VerifySource rejects an index whose stored digest differs from digest.
func (i *Index) VerifySource(digest [32]byte) error {
	if i.Digest != digest {
		return fmt.Errorf("%w: index does not match source (digest mismatch)", ErrBadData)
	}
	return nil
}

// Save writes the index in the on-disk format.
func (i *Index) Save(w io.Writer) error {
	hdr := make([]byte, 0, 4+sha256.Size+4)
	hdr = append(hdr, indexMagic...)
	hdr = append(hdr, i.Digest[:]...)
	var countBuf [4]byte
	binary.LittleEndian.PutUint32(countBuf[:], uint32(len(i.Offsets)))
	hdr = append(hdr, countBuf[:]...)
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	var offBuf [8]byte
	for _, off := range i.Offsets {
		binary.LittleEndian.PutUint64(offBuf[:], off)
		if _, err := w.Write(offBuf[:]); err != nil {
			return err
		}
	}
	return nil
}

// LoadIndex parses an index previously written with Save. The entry count is
// sanity-checked before allocation; entries are then read incrementally.
func LoadIndex(r io.Reader) (*Index, error) {
	hdr := make([]byte, 4+sha256.Size+4)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, fmt.Errorf("recordio: reading index header: %w", err)
	}
	if string(hdr[:4]) != indexMagic {
		return nil, fmt.Errorf("%w: not an index file (magic %q)", ErrBadData, string(hdr[:4]))
	}
	idx := &Index{}
	copy(idx.Digest[:], hdr[4:4+sha256.Size])
	count := binary.LittleEndian.Uint32(hdr[4+sha256.Size:])
	if uint64(count) > maxIndexEntries {
		return nil, fmt.Errorf("%w: index claims %d entries, limit is %d",
			ErrBadData, count, maxIndexEntries)
	}
	idx.Offsets = make([]uint64, 0, min(uint64(count), 1024))
	var offBuf [8]byte
	for n := uint32(0); n < count; n++ {
		if _, err := io.ReadFull(r, offBuf[:]); err != nil {
			return nil, fmt.Errorf("recordio: reading index entry %d: %w", n, err)
		}
		idx.Offsets = append(idx.Offsets, binary.LittleEndian.Uint64(offBuf[:]))
	}
	return idx, nil
}

// ReadRecordAt random-accesses record n via idx. The bytes at the indexed
// offset must independently parse into one complete frame ending exactly on
// its declared boundary, so an index hit can never land inside another
// record.
func ReadRecordAt(rs io.ReaderAt, idx *Index, n, maxRecordSize int) (*Record, error) {
	if n < 0 || n >= len(idx.Offsets) {
		return nil, fmt.Errorf("recordio: record ordinal %d out of range [0,%d)",
			n, len(idx.Offsets))
	}
	if maxRecordSize <= 0 {
		maxRecordSize = DefaultMaxRecordSize
	}

	off := int64(idx.Offsets[n])
	var lenBuf [lenSize]byte
	if _, err := rs.ReadAt(lenBuf[:], off); err != nil {
		return nil, fmt.Errorf("recordio: reading length at offset %d: %w", off, err)
	}
	bodyLen := int(binary.LittleEndian.Uint32(lenBuf[:]))
	if bodyLen < recMetaSize || bodyLen > maxRecordSize {
		return nil, fmt.Errorf("%w: invalid record length %d at indexed offset %d",
			ErrBadData, bodyLen, off)
	}
	body := make([]byte, bodyLen)
	if _, err := rs.ReadAt(body, off+lenSize); err != nil {
		return nil, fmt.Errorf("recordio: reading body at offset %d: %w", off, err)
	}
	rec, err := parseBody(body)
	if err != nil {
		return nil, fmt.Errorf("index hit at offset %d is not a valid record boundary: %w",
			off, err)
	}
	return rec, nil
}

// WriteStream writes the magic followed by the already-encoded records.
func WriteStream(w io.Writer, records ...[]byte) (int64, error) {
	n, err := io.WriteString(w, Magic)
	if err != nil {
		return int64(n), err
	}
	total := int64(n)
	for _, rec := range records {
		k, err := w.Write(rec)
		total += int64(k)
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

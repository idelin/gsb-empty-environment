package pagecheck

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

var fileMagic = [4]byte{'P', 'G', 'F', '1'}

// WriteFile serializes f in the PGF1 binary format. The summary fields
// are written as stored, so a caller may deliberately persist a summary
// that disagrees with the page contents; ReadFile plus Check will
// recover and report that disagreement.
func WriteFile(w io.Writer, f *File) error {
	buf := new(bytes.Buffer)
	buf.Write(fileMagic[:])
	writeUint32(buf, uint32(len(f.Pages)))
	for i := range f.Pages {
		p := &f.Pages[i]
		writeInt64(buf, int64(p.ID))
		writeInt64(buf, p.Summary.MinKey)
		writeInt64(buf, p.Summary.MaxKey)
		writeUint32(buf, p.Summary.Count)
		writeInt64(buf, int64(p.Summary.NextID))
		writeUint32(buf, uint32(len(p.Records)))
		for _, rec := range p.Records {
			writeInt64(buf, rec.Key)
			writeUint32(buf, rec.Seq)
			writeUint32(buf, uint32(len(rec.Value)))
			buf.WriteString(rec.Value)
		}
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// ReadFile parses a PGF1 file. It performs no validation beyond
// structural decoding; use Check to validate the result.
func ReadFile(r io.Reader) (*File, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	rd := &reader{buf: data}
	magic, err := rd.bytes(4)
	if err != nil || [4]byte(magic) != fileMagic {
		return nil, fmt.Errorf("pagecheck: bad magic")
	}
	pageCount, err := rd.uint32()
	if err != nil {
		return nil, err
	}
	f := &File{}
	for i := uint32(0); i < pageCount; i++ {
		var p Page
		id, err := rd.int64()
		if err != nil {
			return nil, err
		}
		p.ID = PageID(id)
		if p.Summary.MinKey, err = rd.int64(); err != nil {
			return nil, err
		}
		if p.Summary.MaxKey, err = rd.int64(); err != nil {
			return nil, err
		}
		if p.Summary.Count, err = rd.uint32(); err != nil {
			return nil, err
		}
		next, err := rd.int64()
		if err != nil {
			return nil, err
		}
		p.Summary.NextID = PageID(next)
		recCount, err := rd.uint32()
		if err != nil {
			return nil, err
		}
		for j := uint32(0); j < recCount; j++ {
			var rec Record
			if rec.Key, err = rd.int64(); err != nil {
				return nil, err
			}
			if rec.Seq, err = rd.uint32(); err != nil {
				return nil, err
			}
			n, err := rd.uint32()
			if err != nil {
				return nil, err
			}
			b, err := rd.bytes(int(n))
			if err != nil {
				return nil, err
			}
			rec.Value = string(b)
			p.Records = append(p.Records, rec)
		}
		f.Pages = append(f.Pages, p)
	}
	return f, nil
}

func writeInt64(buf *bytes.Buffer, v int64) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(v))
	buf.Write(b[:])
}

func writeUint32(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

type reader struct {
	buf []byte
	off int
}

func (r *reader) bytes(n int) ([]byte, error) {
	if n < 0 || r.off+n > len(r.buf) {
		return nil, fmt.Errorf("pagecheck: unexpected end of file")
	}
	b := r.buf[r.off : r.off+n]
	r.off += n
	return b, nil
}

func (r *reader) uint32() (uint32, error) {
	b, err := r.bytes(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

func (r *reader) int64() (int64, error) {
	b, err := r.bytes(8)
	if err != nil {
		return 0, err
	}
	return int64(binary.LittleEndian.Uint64(b)), nil
}

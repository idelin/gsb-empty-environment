// Package carc implements a small custom archive format (create/extract)
// with a security-focused extractor: relative paths only, no traversal,
// no symlink escapes, and hardlinks restricted to verified internal files.
//
// Archive layout:
//   magic: "CARC1\n"
//   entry: type(1) nameLen(u16 LE) name
//     'f': size(u64 LE) content[size]
//     's': targetLen(u16 LE) target
//     'h': targetLen(u16 LE) target
//     'd': (nothing)
package carc

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
)

const (
	TypeFile     = 'f'
	TypeDir      = 'd'
	TypeSymlink  = 's'
	TypeHardlink = 'h'
)

var magic = []byte{'C', 'A', 'R', 'C', '1', '\n'}

// Header describes a single archive entry.
type Header struct {
	Type     byte
	Name     string // slash-separated, relative
	Size     int64  // files only
	Linkname string // symlinks and hardlinks only
}

// Writer serializes entries into an archive stream.
type Writer struct {
	w io.Writer
}

func NewWriter(w io.Writer) (*Writer, error) {
	if _, err := w.Write(magic); err != nil {
		return nil, err
	}
	return &Writer{w: w}, nil
}

func (w *Writer) writeName(t byte, name string) error {
	if len(name) == 0 || len(name) > 0xffff {
		return fmt.Errorf("carc: invalid name length %d", len(name))
	}
	var hdr [3]byte
	hdr[0] = t
	binary.LittleEndian.PutUint16(hdr[1:], uint16(len(name)))
	if _, err := w.w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.w.Write([]byte(name))
	return err
}

func (w *Writer) writeString(s string) error {
	if len(s) > 0xffff {
		return fmt.Errorf("carc: string too long")
	}
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], uint16(len(s)))
	if _, err := w.w.Write(b[:]); err != nil {
		return err
	}
	_, err := w.w.Write([]byte(s))
	return err
}

func (w *Writer) AddDir(name string) error {
	return w.writeName(TypeDir, name)
}

func (w *Writer) AddSymlink(name, target string) error {
	if err := w.writeName(TypeSymlink, name); err != nil {
		return err
	}
	return w.writeString(target)
}

func (w *Writer) AddHardlink(name, target string) error {
	if err := w.writeName(TypeHardlink, name); err != nil {
		return err
	}
	return w.writeString(target)
}

func (w *Writer) AddFile(name string, data []byte) error {
	if err := w.writeName(TypeFile, name); err != nil {
		return err
	}
	var sz [8]byte
	binary.LittleEndian.PutUint64(sz[:], uint64(len(data)))
	if _, err := w.w.Write(sz[:]); err != nil {
		return err
	}
	_, err := w.w.Write(data)
	return err
}

// Reader parses an archive stream entry by entry.
type Reader struct {
	r      io.Reader
	remain int64 // unread content bytes of the current file entry
}

func NewReader(r io.Reader) (*Reader, error) {
	m := make([]byte, len(magic))
	if _, err := io.ReadFull(r, m); err != nil {
		return nil, fmt.Errorf("carc: invalid archive: %v", err)
	}
	if !bytes.Equal(m, magic) {
		return nil, fmt.Errorf("carc: invalid archive: bad magic")
	}
	return &Reader{r: r}, nil
}

// Next advances to the next entry, returning io.EOF at the end.
// For file entries, exactly Header.Size bytes must then be read from rd.
func (rd *Reader) Next() (*Header, error) {
	if rd.remain > 0 {
		if _, err := io.CopyN(ioutil.Discard, rd.r, rd.remain); err != nil {
			return nil, fmt.Errorf("carc: invalid archive: truncated file content")
		}
		rd.remain = 0
	}
	var t [1]byte
	if _, err := io.ReadFull(rd.r, t[:]); err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("carc: invalid archive: %v", err)
	}
	name, err := rd.readString()
	if err != nil {
		return nil, err
	}
	h := &Header{Type: t[0], Name: name}
	switch t[0] {
	case TypeDir:
	case TypeFile:
		var sz [8]byte
		if _, err := io.ReadFull(rd.r, sz[:]); err != nil {
			return nil, fmt.Errorf("carc: invalid archive: truncated size")
		}
		h.Size = int64(binary.LittleEndian.Uint64(sz[:]))
		if h.Size < 0 {
			return nil, fmt.Errorf("carc: invalid archive: file size overflow")
		}
		rd.remain = h.Size
	case TypeSymlink, TypeHardlink:
		if h.Linkname, err = rd.readString(); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("carc: invalid archive: unknown entry type %#x", t[0])
	}
	return h, nil
}

func (rd *Reader) readString() (string, error) {
	var b [2]byte
	if _, err := io.ReadFull(rd.r, b[:]); err != nil {
		return "", fmt.Errorf("carc: invalid archive: truncated string")
	}
	buf := make([]byte, binary.LittleEndian.Uint16(b[:]))
	if _, err := io.ReadFull(rd.r, buf); err != nil {
		return "", fmt.Errorf("carc: invalid archive: truncated string")
	}
	return string(buf), nil
}

// Read reads content bytes of the current file entry.
func (rd *Reader) Read(b []byte) (int, error) {
	if rd.remain == 0 {
		return 0, io.EOF
	}
	if int64(len(b)) > rd.remain {
		b = b[:rd.remain]
	}
	n, err := rd.r.Read(b)
	rd.remain -= int64(n)
	return n, err
}

// Package pagefile implements an ordered page file format together with an
// independent checker. Each page carries a summary header (min key, max key,
// record count, next page ID) and a sequence of (key, value) records whose
// keys must be non-decreasing. Duplicate keys are legal as long as they
// appear in the explicitly written order.
//
// Framing is driven by RecordBytes, not by Count: Count is pure summary
// metadata, so a corrupt Count can be detected by the checker instead of
// silently desynchronizing the parser.
package pagefile

import (
	"encoding/binary"
	"fmt"
	"io"
)

// NilPage terminates the next-page chain.
const NilPage uint32 = 0xFFFFFFFF

// headerSize: pageID, minKey, maxKey, count, nextPage, recordBytes.
const headerSize = 4 + 8 + 8 + 4 + 4 + 4

// Header is the per-page summary. RecordBytes is framing (bytes occupied by
// the record section); the other fields are checkable summary metadata.
type Header struct {
	PageID      uint32
	MinKey      int64
	MaxKey      int64
	Count       uint32
	NextPage    uint32
	RecordBytes uint32
}

// Record is a single key/value entry.
type Record struct {
	Key   int64
	Value string
}

// Page is a decoded page: its summary header plus its records.
type Page struct {
	Header  Header
	Records []Record
}

// File is a decoded page file: the head of the chain plus all pages indexed
// by page ID, in file order.
type File struct {
	Head  uint32
	Pages []*Page
	byID  map[uint32]*Page
}

// PageByID returns the page with the given ID, or nil.
func (f *File) PageByID(id uint32) *Page { return f.byID[id] }

// EncodePage serializes one page. The summary fields are written exactly as
// stated in h (RecordBytes is always recomputed for framing); callers are
// free to write headers that disagree with the records — that is exactly
// what the checker is for.
func EncodePage(w io.Writer, h Header, records []Record) error {
	var body []byte
	for _, r := range records {
		var kb [8]byte
		binary.BigEndian.PutUint64(kb[:], uint64(r.Key))
		body = append(body, kb[:]...)
		var lb [4]byte
		binary.BigEndian.PutUint32(lb[:], uint32(len(r.Value)))
		body = append(body, lb[:]...)
		body = append(body, r.Value...)
	}
	h.RecordBytes = uint32(len(body))
	buf := make([]byte, headerSize)
	binary.BigEndian.PutUint32(buf[0:4], h.PageID)
	binary.BigEndian.PutUint64(buf[4:12], uint64(h.MinKey))
	binary.BigEndian.PutUint64(buf[12:20], uint64(h.MaxKey))
	binary.BigEndian.PutUint32(buf[20:24], h.Count)
	binary.BigEndian.PutUint32(buf[24:28], h.NextPage)
	binary.BigEndian.PutUint32(buf[28:32], h.RecordBytes)
	if _, err := w.Write(buf); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}

// EncodeFile serializes a whole file: a 4-byte head page ID followed by the
// pages in the given order.
func EncodeFile(w io.Writer, head uint32, pages []Page) error {
	var hb [4]byte
	binary.BigEndian.PutUint32(hb[:], head)
	if _, err := w.Write(hb[:]); err != nil {
		return err
	}
	for _, p := range pages {
		if err := EncodePage(w, p.Header, p.Records); err != nil {
			return err
		}
	}
	return nil
}

// DecodeFile parses a page file produced by EncodeFile.
func DecodeFile(r io.Reader) (*File, error) {
	var hb [4]byte
	if _, err := io.ReadFull(r, hb[:]); err != nil {
		return nil, fmt.Errorf("read head: %v", err)
	}
	f := &File{Head: binary.BigEndian.Uint32(hb[:]), byID: map[uint32]*Page{}}
	for {
		p, err := decodePage(r)
		if err == io.EOF {
			return f, nil
		}
		if err != nil {
			return nil, err
		}
		if _, dup := f.byID[p.Header.PageID]; dup {
			return nil, fmt.Errorf("duplicate page ID %d", p.Header.PageID)
		}
		f.Pages = append(f.Pages, p)
		f.byID[p.Header.PageID] = p
	}
}

func decodePage(r io.Reader) (*Page, error) {
	buf := make([]byte, headerSize)
	if _, err := io.ReadFull(r, buf); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, io.EOF
		}
		return nil, err
	}
	p := &Page{Header: Header{
		PageID:      binary.BigEndian.Uint32(buf[0:4]),
		MinKey:      int64(binary.BigEndian.Uint64(buf[4:12])),
		MaxKey:      int64(binary.BigEndian.Uint64(buf[12:20])),
		Count:       binary.BigEndian.Uint32(buf[20:24]),
		NextPage:    binary.BigEndian.Uint32(buf[24:28]),
		RecordBytes: binary.BigEndian.Uint32(buf[28:32]),
	}}
	body := make([]byte, p.Header.RecordBytes)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, fmt.Errorf("page %d records: %v", p.Header.PageID, err)
	}
	for len(body) > 0 {
		if len(body) < 12 {
			return nil, fmt.Errorf("page %d: truncated record section", p.Header.PageID)
		}
		key := int64(binary.BigEndian.Uint64(body[0:8]))
		n := binary.BigEndian.Uint32(body[8:12])
		body = body[12:]
		if uint32(len(body)) < n {
			return nil, fmt.Errorf("page %d: truncated record value", p.Header.PageID)
		}
		p.Records = append(p.Records, Record{Key: key, Value: string(body[:n])})
		body = body[n:]
	}
	return p, nil
}

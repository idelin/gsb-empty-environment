package layerpack

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"io"
)

// Codec is a named compression transform identified by a single byte in the
// container format. Only the Go standard library is used.
type Codec struct {
	ID   byte
	Name string
	enc  func(w io.Writer) (io.WriteCloser, error)
	dec  func(r io.Reader) (io.Reader, error)
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

var (
	// Store passes data through unchanged; useful for testing and for
	// already-compressed payloads.
	Store = Codec{
		ID:   0,
		Name: "store",
		enc:  func(w io.Writer) (io.WriteCloser, error) { return nopWriteCloser{w}, nil },
		dec:  func(r io.Reader) (io.Reader, error) { return r, nil },
	}
	// Flate is raw DEFLATE (RFC 1951) via compress/flate.
	Flate = Codec{
		ID:   1,
		Name: "flate",
		enc:  func(w io.Writer) (io.WriteCloser, error) { return flate.NewWriter(w, flate.DefaultCompression) },
		dec:  func(r io.Reader) (io.Reader, error) { return flate.NewReader(r), nil },
	}
	// Gzip is gzip-wrapped DEFLATE via compress/gzip.
	Gzip = Codec{
		ID:   2,
		Name: "gzip",
		enc:  func(w io.Writer) (io.WriteCloser, error) { return gzip.NewWriter(w), nil },
		dec:  func(r io.Reader) (io.Reader, error) { return gzip.NewReader(r) },
	}
)

var codecsByID = map[byte]Codec{
	Store.ID: Store,
	Flate.ID: Flate,
	Gzip.ID:  Gzip,
}

func codecByID(id byte) (Codec, bool) {
	c, ok := codecsByID[id]
	return c, ok
}

func (c Codec) compress(src []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := c.enc(&buf)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(src); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// decompressInto expands src into dst, refusing to produce more than
// maxLen bytes so decode output stays bounded.
func (c Codec) decompressInto(dst *bytes.Buffer, src []byte, maxLen int64) error {
	r, err := c.dec(bytes.NewReader(src))
	if err != nil {
		return err
	}
	n, err := io.Copy(dst, io.LimitReader(r, maxLen+1))
	if err != nil {
		return err
	}
	if n > maxLen {
		return ErrFormat
	}
	return nil
}

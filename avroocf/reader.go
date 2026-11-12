package avroocf

import (
	"fmt"
	"io"
)

const syncSize = 16

var magic = [4]byte{'O', 'b', 'j', 1}

// sizeReader is a stream that can report the total number of bytes that
// remain available. *bytes.Reader and *bytes.Buffer satisfy it via Len.
type sizeReader interface {
	io.Reader
	Len() int
}

type countedReader struct {
	parent    *countedReader
	source    io.Reader
	remaining int64
}

func (r *countedReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.source.Read(p)
	r.remaining -= int64(n)
	return n, err
}

func (r *countedReader) child(limit int64) *countedReader {
	if limit > r.remaining {
		limit = r.remaining
	}
	return &countedReader{parent: r, source: r, remaining: limit}
}

func readFull(r io.Reader, size int) ([]byte, error) {
	buffer := make([]byte, size)
	if _, err := io.ReadFull(r, buffer); err != nil {
		return nil, err
	}
	return buffer, nil
}

// Reader streams one Avro object container file under the resource limits of
// a Config. The typical call sequence is Open, then repeated NextBlock /
// Decode, finally Close.
type Reader struct {
	config   Config
	input    *countedReader
	schema   *Schema
	sync     [syncSize]byte
	metadata map[string][]byte

	block        *countedReader
	rawBlock     *countedReader
	blockObjects int64
	objectRead   int64
	zlib         io.ReadCloser
	blockMinSize int64
	closed       bool
}

// Open validates the header of an Avro container file. r must report its
// remaining length via Len (for example *bytes.Reader or *bytes.Buffer).
func Open(r sizeReader, config Config) (*Reader, error) {
	return openReader(r, int64(r.Len()), config)
}

// OpenWithSize is Open for streams without Len, when the caller knows how
// many bytes remain. A regular file can be wrapped with its current size.
func OpenWithSize(r io.Reader, size int64, config Config) (*Reader, error) {
	if size < 0 {
		return nil, fmt.Errorf("%w: negative stream size", ErrMalformed)
	}
	return openReader(r, size, config)
}

func openReader(source io.Reader, size int64, config Config) (*Reader, error) {
	config = config.withDefaults()
	reader := &Reader{
		config: config,
		input:  &countedReader{source: source, remaining: size},
	}
	if err := reader.readHeader(); err != nil {
		return nil, err
	}
	return reader, nil
}

// Schema returns the validated writer schema.
func (r *Reader) Schema() *Schema {
	return r.schema
}

// Close releases any active decompressor.
func (r *Reader) Close() error {
	r.closed = true
	if r.zlib != nil {
		err := r.zlib.Close()
		r.zlib = nil
		return err
	}
	return nil
}

package blkconv

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"io"
)

// teeReader copies everything read into the running stream checksum.
type teeReader struct {
	r *bufio.Reader
	w hash.Hash
}

func (t *teeReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	t.w.Write(p[:n])
	return n, err
}

func (t *teeReader) ReadByte() (byte, error) {
	b, err := t.r.ReadByte()
	if err == nil {
		t.w.Write([]byte{b})
	}
	return b, err
}

// Encoder writes a block-encoded stream. It is an io.Writer: plaintext
// goes in, framed blocks come out. Memory use is bounded by BlockSize.
// The commit marker is emitted by Close only after the declared original
// length and digest have been verified against the actual bytes written.
type Encoder struct {
	raw     io.Writer
	tee     io.Writer
	f       Format
	sum     hash.Hash
	sha     hash.Hash
	buf     []byte
	blocks  uint64
	total   uint64
	wantLen uint64
	wantSum [32]byte
	err     error
	closed  bool
}

// NewEncoder writes the header immediately. origLen and digest are the
// declared plaintext length and SHA-256; Close refuses to publish the
// commit marker if the actual stream does not match them.
func NewEncoder(dst io.Writer, f Format, meta map[string]string, origLen uint64, digest [32]byte) (*Encoder, error) {
	e := &Encoder{
		raw:     dst,
		f:       f,
		sum:     f.newSum(),
		sha:     sha256.New(),
		wantLen: origLen,
		wantSum: digest,
	}
	e.tee = io.MultiWriter(dst, e.sum)
	h := &Header{Format: f, Meta: meta, OrigLen: origLen, Digest: digest}
	if err := writeHeader(e.tee, h); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *Encoder) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	if e.closed {
		return 0, fmt.Errorf("blkconv: write on closed encoder")
	}
	written := len(p)
	e.sha.Write(p)
	e.total += uint64(len(p))
	for len(p) > 0 {
		n := BlockSize - len(e.buf)
		if n > len(p) {
			n = len(p)
		}
		e.buf = append(e.buf, p[:n]...)
		p = p[n:]
		if len(e.buf) == BlockSize {
			if err := e.flushBlock(e.buf); err != nil {
				e.err = err
				return 0, err
			}
			e.buf = e.buf[:0]
		}
	}
	return written, nil
}

func (e *Encoder) flushBlock(raw []byte) error {
	enc := encodeBlock(e.f, nil, raw)
	if err := writeBlock(e.tee, e.f, raw, enc); err != nil {
		return err
	}
	e.blocks++
	return nil
}

// Close flushes the final partial block, verifies the declared length and
// digest against the actual stream, and only then writes the commit
// marker. On any mismatch no marker is emitted.
func (e *Encoder) Close() error {
	if e.err != nil {
		return e.err
	}
	if e.closed {
		return nil
	}
	e.closed = true
	if len(e.buf) > 0 {
		if err := e.flushBlock(e.buf); err != nil {
			e.err = err
			return err
		}
		e.buf = nil
	}
	if e.total != e.wantLen {
		e.err = fmt.Errorf("%w: declared %d, got %d", ErrLengthMismatch, e.wantLen, e.total)
		return e.err
	}
	if got := e.sha.Sum(nil); !bytes.Equal(got, e.wantSum[:]) {
		e.err = ErrDigestMismatch
		return e.err
	}
	if err := writeTrailer(e.tee, e.raw, e.f, e.sum, e.blocks); err != nil {
		e.err = err
		return err
	}
	return nil
}

// Decoder reads a block-encoded stream as an io.Reader of plaintext.
// Every block is length- and checksum-verified, total output is bounded
// by a budget, and the trailing commit marker (block count + stream
// checksum) must verify before EOF is reported.
type Decoder struct {
	br     *bufio.Reader
	tr     *teeReader
	f      Format
	hdr    *Header
	sum    hash.Hash
	sha    hash.Hash
	block  []byte
	pos    int
	blocks uint64
	total  uint64
	max    uint64
	done   bool
	err    error
}

// NewDecoder parses the stream header. maxBytes bounds total decoded
// output; 0 selects DefaultMaxDecode.
func NewDecoder(src io.Reader, maxBytes uint64) (*Decoder, error) {
	br, ok := src.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(src)
	}
	h, raw, err := readHeader(br)
	if err != nil {
		return nil, err
	}
	if maxBytes == 0 {
		maxBytes = DefaultMaxDecode
	}
	sum := h.Format.newSum()
	sum.Write(raw)
	d := &Decoder{
		br:  br,
		f:   h.Format,
		hdr: h,
		sum: sum,
		sha: sha256.New(),
		max: maxBytes,
	}
	d.tr = &teeReader{r: br, w: sum}
	return d, nil
}

// Header returns the parsed stream header.
func (d *Decoder) Header() Header { return *d.hdr }

func (d *Decoder) Read(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	for d.pos >= len(d.block) {
		if d.done {
			return 0, io.EOF
		}
		if err := d.next(); err != nil {
			d.err = err
			return 0, err
		}
	}
	n := copy(p, d.block[d.pos:])
	d.pos += n
	return n, nil
}

// next consumes exactly one record: a block or the commit trailer.
func (d *Decoder) next() error {
	tag, err := d.tr.ReadByte()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNoCommit, err)
	}
	switch tag {
	case tagBlock:
		return d.readBlock()
	case tagTrailer:
		return d.readTrailer()
	default:
		return fmt.Errorf("%w: unknown record tag 0x%02x", ErrCorrupt, tag)
	}
}

func (d *Decoder) readBlock() error {
	var compLen, rawLen uint64
	var want []byte
	switch d.f {
	case FormatA:
		var b [12]byte
		if _, err := io.ReadFull(d.tr, b[:]); err != nil {
			return fmt.Errorf("%w: block header: %v", ErrNoCommit, err)
		}
		compLen = uint64(binary.BigEndian.Uint32(b[0:4]))
		rawLen = uint64(binary.BigEndian.Uint32(b[4:8]))
		want = b[8:12]
	case FormatB:
		var err error
		if compLen, err = binary.ReadUvarint(d.tr); err != nil {
			return fmt.Errorf("%w: block header: %v", ErrNoCommit, err)
		}
		if rawLen, err = binary.ReadUvarint(d.tr); err != nil {
			return fmt.Errorf("%w: block header: %v", ErrNoCommit, err)
		}
		want = make([]byte, 8)
		if _, err := io.ReadFull(d.tr, want); err != nil {
			return fmt.Errorf("%w: block header: %v", ErrNoCommit, err)
		}
	}
	if rawLen == 0 || rawLen > BlockSize {
		return fmt.Errorf("%w: block raw length %d out of bounds", ErrCorrupt, rawLen)
	}
	if compLen == 0 || compLen > maxWireLen(d.f) {
		return fmt.Errorf("%w: block wire length %d out of bounds", ErrCorrupt, compLen)
	}
	if d.total+rawLen > d.max {
		return fmt.Errorf("%w: limit %d bytes", ErrBudget, d.max)
	}
	payload := make([]byte, compLen)
	if _, err := io.ReadFull(d.tr, payload); err != nil {
		return fmt.Errorf("%w: block payload: %v", ErrNoCommit, err)
	}
	raw, err := decodeBlock(d.f, nil, payload, int(rawLen))
	if err != nil {
		if err == errOverflow {
			return fmt.Errorf("%w: block expands past declared length", ErrCorrupt)
		}
		return err
	}
	if uint64(len(raw)) != rawLen {
		return fmt.Errorf("%w: block decoded to %d bytes, declared %d", ErrCorrupt, len(raw), rawLen)
	}
	switch d.f {
	case FormatA:
		if crc := crc32sum(raw); !bytes.Equal(crc, want) {
			return fmt.Errorf("%w: block checksum mismatch", ErrCorrupt)
		}
	case FormatB:
		if crc := crc64sum(raw); !bytes.Equal(crc, want) {
			return fmt.Errorf("%w: block checksum mismatch", ErrCorrupt)
		}
	}
	d.sha.Write(raw)
	d.total += rawLen
	d.blocks++
	d.block = raw
	d.pos = 0
	return nil
}

func (d *Decoder) readTrailer() error {
	marker := make([]byte, 4)
	if _, err := io.ReadFull(d.tr, marker); err != nil {
		return fmt.Errorf("%w: trailer: %v", ErrNoCommit, err)
	}
	m := d.f.marker()
	if !bytes.Equal(marker, m[:]) {
		return fmt.Errorf("%w: bad commit marker", ErrCorrupt)
	}
	var blocks uint64
	if d.f == FormatA {
		var b [4]byte
		if _, err := io.ReadFull(d.tr, b[:]); err != nil {
			return fmt.Errorf("%w: trailer: %v", ErrNoCommit, err)
		}
		blocks = uint64(binary.BigEndian.Uint32(b[:]))
	} else {
		var err error
		if blocks, err = binary.ReadUvarint(d.tr); err != nil {
			return fmt.Errorf("%w: trailer: %v", ErrNoCommit, err)
		}
	}
	wantSum := d.sum.Sum(nil)
	gotSum := make([]byte, len(wantSum))
	if _, err := io.ReadFull(d.br, gotSum); err != nil {
		return fmt.Errorf("%w: trailer checksum: %v", ErrNoCommit, err)
	}
	if !bytes.Equal(gotSum, wantSum) {
		return fmt.Errorf("%w: stream checksum mismatch", ErrCorrupt)
	}
	if blocks != d.blocks {
		return fmt.Errorf("%w: trailer counts %d blocks, decoded %d", ErrCorrupt, blocks, d.blocks)
	}
	if d.total != d.hdr.OrigLen {
		return fmt.Errorf("%w: declared %d, decoded %d", ErrLengthMismatch, d.hdr.OrigLen, d.total)
	}
	if got := d.sha.Sum(nil); !bytes.Equal(got, d.hdr.Digest[:]) {
		return ErrDigestMismatch
	}
	d.done = true
	d.block = nil
	return nil
}

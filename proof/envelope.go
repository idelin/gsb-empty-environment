package proof

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// Envelope is the single-file serialization:
//
//	payload bytes
//	canonical manifest bytes
//	u64 manifest length
//	[32]byte root summary
//	u64 payload length
//	[8]byte magic "PRFENV01"
//
// total trailer = 4+32+8+8 = 52 bytes. Nothing in the trailer is trusted on
// its own: a verifier recomputes and compares the root.
var envelopeMagic = [8]byte{'P', 'R', 'F', 'E', 'N', 'V', '0', '1'}

const envelopeTrailerLen = 8 + 32 + 8 + 8

var ErrEnvelope = errors.New("proof: bad envelope")

func WriteEnvelopeFile(path string, p *Package) error {
	mb := p.Manifest.EncodeCanonical()
	var hdr [envelopeTrailerLen]byte
	pos := 0
	putU64(hdr[pos:], uint64(len(mb)))
	pos += 8
	copy(hdr[pos:], p.Root[:])
	pos += 32
	putU64(hdr[pos:], uint64(len(p.Payload)))
	pos += 8
	copy(hdr[pos:], envelopeMagic[:])

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(p.Payload); err != nil {
		return err
	}
	if _, err := f.Write(mb); err != nil {
		return err
	}
	_, err = f.Write(hdr[:])
	return err
}

// Envelope is a random-access handle to an on-disk package.
type Envelope struct {
	file          *os.File
	payloadLength uint64
	manifestBytes []byte
	manifest      *Manifest
	advertised    RootSummary
}

// OpenEnvelope reads the trailer and manifest, validating lengths against the
// manifest scope BEFORE allocating the manifest buffer. The manifest is decoded
// and geometry-checked; the advertised root is stored for an independent
// verifier to compare against the trusted root.
func OpenEnvelope(path string) (*Envelope, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	size := st.Size()
	if size < envelopeTrailerLen {
		f.Close()
		return nil, fmt.Errorf("%w: file too short", ErrEnvelope)
	}

	var trailer [envelopeTrailerLen]byte
	if _, err := f.ReadAt(trailer[:], size-envelopeTrailerLen); err != nil {
		f.Close()
		return nil, fmt.Errorf("%w: %v", ErrEnvelope, err)
	}
	pos := 0
	manifestLen := int64(binaryU64(trailer[pos:]))
	pos += 8
	var advertised RootSummary
	copy(advertised[:], trailer[pos:pos+32])
	pos += 32
	payloadLen := int64(binaryU64(trailer[pos:]))
	pos += 8
	if [8]byte(trailer[pos:pos+8]) != envelopeMagic {
		f.Close()
		return nil, fmt.Errorf("%w: bad magic", ErrEnvelope)
	}

	// Length validated for the manifest scope before allocation.
	if err := CheckScope(ScopeManifest, manifestLen); err != nil {
		f.Close()
		return nil, fmt.Errorf("%w: %w", ErrEnvelope, err)
	}
	if payloadLen < 0 || manifestLen < 0 || payloadLen+manifestLen+envelopeTrailerLen != size {
		f.Close()
		return nil, fmt.Errorf("%w: inconsistent lengths", ErrEnvelope)
	}

	mb := make([]byte, manifestLen)
	if _, err := io.ReadFull(io.NewSectionReader(f, payloadLen, manifestLen), mb); err != nil {
		f.Close()
		return nil, fmt.Errorf("%w: %v", ErrEnvelope, err)
	}
	m, err := DecodeManifest(ScopeManifest, mb)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%w: %w", ErrEnvelope, err)
	}
	if err := m.Validate(); err != nil {
		f.Close()
		return nil, fmt.Errorf("%w: %w", ErrEnvelope, err)
	}
	if m.PayloadLength() != uint64(payloadLen) {
		f.Close()
		return nil, fmt.Errorf("%w: payload length disagrees with manifest", ErrEnvelope)
	}

	return &Envelope{
		file:          f,
		payloadLength: uint64(payloadLen),
		manifestBytes: mb,
		manifest:      m,
		advertised:    advertised,
	}, nil
}

func (e *Envelope) Close() error { return e.file.Close() }

func (e *Envelope) Manifest() *Manifest         { return e.manifest }
func (e *Envelope) AdvertisedRoot() RootSummary { return e.advertised }

// FetchChunk reads one chunk by index from the payload region using the
// manifest's geometry. Offset comes from chunk sizes, never from untrusted
// per-chunk offset fields.
func (e *Envelope) FetchChunk(index uint32) ([]byte, error) {
	if int(index) >= len(e.manifest.Chunks) {
		return nil, fmt.Errorf("%w: %d", ErrChunkIndex, index)
	}
	var off uint64
	for _, c := range e.manifest.Chunks[:index] {
		off += uint64(c.Bytes)
	}
	n := e.manifest.Chunks[index].Bytes
	if off+uint64(n) > e.payloadLength {
		return nil, fmt.Errorf("%w: chunk %d past payload", ErrBadManifest, index)
	}
	buf := make([]byte, n)
	if err := readFullAt(e.file, buf, int64(off)); err != nil {
		return nil, err
	}
	return buf, nil
}

func readFullAt(r io.ReaderAt, buf []byte, off int64) error {
	for len(buf) > 0 {
		n, err := r.ReadAt(buf, off)
		if err == io.EOF && n == len(buf) {
			return nil
		}
		if err != nil && err != io.EOF {
			return err
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
		buf = buf[n:]
		off += int64(n)
	}
	return nil
}

func binaryU64(b []byte) uint64 {
	return uint64(b[0])<<56 | uint64(b[1])<<48 | uint64(b[2])<<40 | uint64(b[3])<<32 |
		uint64(b[4])<<24 | uint64(b[5])<<16 | uint64(b[6])<<8 | uint64(b[7])
}

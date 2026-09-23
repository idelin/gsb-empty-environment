package rlecomp

import (
	"encoding/binary"
	"errors"
	"io"
)

var (
	ErrBadMagic      = errors.New("rlecomp: bad magic")
	ErrBadVersion    = errors.New("rlecomp: unsupported format version")
	ErrBadChecksum   = errors.New("rlecomp: checksum mismatch")
	ErrCorrupt       = errors.New("rlecomp: corrupt stream")
	ErrQuotaExceeded = errors.New("rlecomp: decoded output exceeds quota")
)

// DecodeAll decodes every frame in r until EOF and returns the
// concatenated output and the number of frames seen. A zero-byte stream
// yields zero frames and nil error; an empty frame yields one frame with
// zero output bytes. The total decoded output is bounded by quota bytes;
// exceeding it fails with ErrQuotaExceeded. Block lengths, frame lengths,
// version and checksum are all validated.
func DecodeAll(r io.Reader, quota int64) ([]byte, int, error) {
	if quota < 0 {
		quota = 0
	}
	br := &byteReader{r: r}
	var out []byte
	frames := 0
	for {
		var hdr [3]byte
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			if err == io.EOF {
				return out, frames, nil
			}
			return nil, frames, ErrCorrupt
		}
		if hdr[0] != magic[0] || hdr[1] != magic[1] {
			return nil, frames, ErrBadMagic
		}
		if hdr[2] != FormatVersion {
			return nil, frames, ErrBadVersion
		}
		payloadLen, err := binary.ReadUvarint(br)
		if err != nil || payloadLen > MaxFrameLen {
			return nil, frames, ErrCorrupt
		}
		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(br, payload); err != nil {
			return nil, frames, ErrCorrupt
		}
		var sum [4]byte
		if _, err := io.ReadFull(br, sum[:]); err != nil {
			return nil, frames, ErrCorrupt
		}
		if binary.BigEndian.Uint32(sum[:]) != crc32Of(payload) {
			return nil, frames, ErrBadChecksum
		}
		var err2 error
		out, err2 = decodePayload(payload, out, quota)
		if err2 != nil {
			return nil, frames, err2
		}
		frames++
	}
}

// decodePayload decodes all blocks in payload, appending to out.
func decodePayload(payload, out []byte, quota int64) ([]byte, error) {
	pos := 0
	for pos < len(payload) {
		tag := payload[pos]
		pos++
		v, n := binary.Uvarint(payload[pos:])
		if n <= 0 || v == 0 || v > MaxBlockLen {
			return nil, ErrCorrupt
		}
		pos += n
		switch tag {
		case tagLiteral:
			if len(payload)-pos < int(v) {
				return nil, ErrCorrupt
			}
			if int64(len(out))+int64(v) > quota {
				return nil, ErrQuotaExceeded
			}
			out = append(out, payload[pos:pos+int(v)]...)
			pos += int(v)
		case tagRepeat:
			if pos >= len(payload) {
				return nil, ErrCorrupt
			}
			val := payload[pos]
			pos++
			if int64(len(out))+int64(v) > quota {
				return nil, ErrQuotaExceeded
			}
			for range v {
				out = append(out, val)
			}
		default:
			return nil, ErrCorrupt
		}
	}
	return out, nil
}

// byteReader adapts an io.Reader to io.ByteReader.
type byteReader struct {
	r   io.Reader
	buf [1]byte
}

func (b *byteReader) Read(p []byte) (int, error) { return b.r.Read(p) }

func (b *byteReader) ReadByte() (byte, error) {
	_, err := io.ReadFull(b.r, b.buf[:])
	return b.buf[0], err
}

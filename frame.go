package framecomp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// Version is the on-disk format version written into every frame header.
const Version = 1

// Compression methods stored in the frame header.
const (
	MethodStore = 0
	MethodLZ    = 1
)

// HeaderSize is the fixed frame header length in bytes.
const HeaderSize = 4 + 1 + 1 + 4 + 4 + 4

var magic = [4]byte{'F', 'C', 'M', 'P'}

var (
	// ErrSyncLost reports that the scan budget was exhausted while more
	// unscanned data remains. Trailing garbage within budget is skipped
	// silently and counted in Stats.SkippedBytes.
	ErrSyncLost = errors.New("framecomp: no frame marker within scan budget")
	// ErrOutputQuota reports that decoded output exceeded the total quota.
	ErrOutputQuota = errors.New("framecomp: decode output quota exceeded")
)

// Limits bounds the decoder's work and output. All fields are hard caps.
type Limits struct {
	// MaxScanBytes caps how many bytes may be skipped while searching for
	// the next frame marker during resynchronization.
	MaxScanBytes int
	// MaxFrameCompLen caps a single frame's compressed payload. A header
	// claiming more is rejected as fake without reading its payload.
	MaxFrameCompLen int
	// MaxFrameOrigLen caps a single frame's decompressed size.
	MaxFrameOrigLen int
	// MaxTotalOutput caps the sum of all decoded frame payloads.
	MaxTotalOutput int
}

// DefaultLimits returns limits suitable for small to medium archives.
func DefaultLimits() Limits {
	return Limits{
		MaxScanBytes:    1 << 20,
		MaxFrameCompLen: 8 << 20,
		MaxFrameOrigLen: 16 << 20,
		MaxTotalOutput:  64 << 20,
	}
}

// AppendFrame compresses p and appends one self-contained frame to dst.
// Falls back to the store method when compression does not shrink the data.
func AppendFrame(dst, p []byte) []byte {
	comp := lzCompress(p)
	method := byte(MethodLZ)
	payload := comp
	if len(comp) >= len(p) {
		method = MethodStore
		payload = p
	}
	var hdr [HeaderSize]byte
	copy(hdr[0:4], magic[:])
	hdr[4] = Version
	hdr[5] = method
	binary.LittleEndian.PutUint32(hdr[6:10], uint32(len(payload)))
	binary.LittleEndian.PutUint32(hdr[10:14], uint32(len(p)))
	binary.LittleEndian.PutUint32(hdr[14:18], crc32.ChecksumIEEE(p))
	dst = append(dst, hdr[:]...)
	return append(dst, payload...)
}

// Stats summarizes a DecodeAll run.
type Stats struct {
	Frames        int // frames successfully decoded and emitted
	BadCandidates int // candidate headers rejected (corrupt or fake)
	SkippedBytes  int // bytes skipped while resynchronizing
	OutputBytes   int // total decoded bytes emitted
}

// DecodeAll decodes every intact frame in data and returns the concatenated
// original payloads in order. Corrupted regions are skipped without emitting
// any bytes for them: a candidate frame is only emitted after its payload is
// fully present, structurally decodable to exactly origLen bytes, and the
// CRC-32 of that complete original matches the header. A claimed compLen is
// trusted only up to lim.MaxFrameCompLen and the bytes actually remaining,
// so fake lengths cannot cause unbounded reads; resynchronization scans at
// most lim.MaxScanBytes ahead.
func DecodeAll(data []byte, lim Limits) ([]byte, Stats, error) {
	var out []byte
	var st Stats
	pos := 0
	for pos < len(data) {
		window := data[pos:]
		if len(window) > lim.MaxScanBytes {
			window = window[:lim.MaxScanBytes]
		}
		idx := bytes.Index(window, magic[:])
		if idx < 0 {
			st.SkippedBytes += len(data) - pos
			if len(data)-pos > lim.MaxScanBytes {
				return out, st, ErrSyncLost
			}
			// Scan reached end of stream within budget: trailing
			// garbage or a truncated tail, nothing more to recover.
			return out, st, nil
		}
		st.SkippedBytes += idx
		pos += idx
		if len(data)-pos < HeaderSize {
			// Remaining bytes can never hold a full header; drop them.
			st.SkippedBytes += len(data) - pos
			return out, st, nil
		}
		hdr := data[pos : pos+HeaderSize]
		compLen := binary.LittleEndian.Uint32(hdr[6:10])
		origLen := binary.LittleEndian.Uint32(hdr[10:14])
		crc := binary.LittleEndian.Uint32(hdr[14:18])
		if hdr[4] == Version &&
			(hdr[5] == MethodStore || hdr[5] == MethodLZ) &&
			compLen <= uint32(lim.MaxFrameCompLen) &&
			origLen <= uint32(lim.MaxFrameOrigLen) &&
			uint64(compLen) <= uint64(len(data)-pos-HeaderSize) {
			payload := data[pos+HeaderSize : pos+HeaderSize+int(compLen)]
			raw, ok := decodePayload(hdr[5], payload, int(origLen))
			if ok && crc32.ChecksumIEEE(raw) == crc {
				if st.OutputBytes+int(origLen) > lim.MaxTotalOutput {
					return out, st, ErrOutputQuota
				}
				out = append(out, raw...)
				st.OutputBytes += int(origLen)
				st.Frames++
				pos += HeaderSize + int(compLen)
				continue
			}
		}
		// Corrupt or fake candidate: emit nothing, rescan one byte later.
		st.BadCandidates++
		pos++
	}
	return out, st, nil
}

// decodePayload fully decodes one frame payload. ok is false unless the
// result is exactly origLen bytes; truncated or malformed data never
// produces a partial result.
func decodePayload(method byte, payload []byte, origLen int) ([]byte, bool) {
	switch method {
	case MethodStore:
		if len(payload) != origLen {
			return nil, false
		}
		return payload, true
	case MethodLZ:
		raw, err := lzDecompress(payload, origLen)
		if err != nil || len(raw) != origLen {
			return nil, false
		}
		return raw, true
	}
	return nil, false
}

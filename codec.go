// Package erasure implements systematic block erasure coding over
// GF(256) with the explicit polynomial x^8+x^4+x^3+x^2+1 (0x11D).
//
// Encoding splits data into K data shards and generates M parity
// shards with a Vandermonde generator matrix, so any K of the K+M
// shards suffice to recover the original data.
//
// Shard binary format (all integers little-endian):
//
//	offset 0   magic "EC"          2 bytes
//	offset 2   format version      1 byte  (currently 1)
//	offset 3   K (data shards)     1 byte
//	offset 4   M (parity shards)   1 byte
//	offset 5   shard ID            1 byte  (0..K+M-1)
//	offset 6   shard payload size  4 bytes
//	offset 10  original data size  8 bytes
//	offset 18  CRC-32 (IEEE)       4 bytes, over header[0:18]+payload
//	offset 22  payload             shardSize bytes
package erasure

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"sort"
)

// Format constants.
const (
	Magic0        = 'E'
	Magic1        = 'C'
	FormatVersion = 1
	HeaderSize    = 22
)

// Errors returned by Encode and Decode.
var (
	ErrInvalidParams      = errors.New("erasure: invalid shard parameters")
	ErrMalformedShard     = errors.New("erasure: malformed shard")
	ErrUnsupportedVersion = errors.New("erasure: unsupported format version")
	ErrChecksumMismatch   = errors.New("erasure: shard checksum mismatch")
	ErrDuplicateShard     = errors.New("erasure: duplicate shard ID")
	ErrInconsistentHeader = errors.New("erasure: inconsistent shard headers")
	ErrInsufficientShards = errors.New("erasure: insufficient valid shards")
	ErrOutputLimit        = errors.New("erasure: decoded length exceeds output limit")
)

type shardHeader struct {
	k, m      int
	id        int
	shardSize int
	origLen   int64
}

type decodedShard struct {
	payload []byte
}

// Encode splits data into k data shards and m parity shards and returns
// k+m self-describing shards. Any k of them recover data via Decode.
func Encode(data []byte, k, m int) ([][]byte, error) {
	if k < 1 || m < 1 || k+m > 256 {
		return nil, ErrInvalidParams
	}
	shardSize := (len(data) + k - 1) / k
	payloads := make([][]byte, k+m)
	for i := 0; i < k; i++ {
		p := make([]byte, shardSize)
		start := i * shardSize
		if start < len(data) {
			end := start + shardSize
			if end > len(data) {
				end = len(data)
			}
			copy(p, data[start:end])
		}
		payloads[i] = p
	}
	for j := 0; j < m; j++ {
		row := vandermondeRow(j, k)
		p := make([]byte, shardSize)
		for c := 0; c < k; c++ {
			coef := row[c]
			if coef == 0 {
				continue
			}
			src := payloads[c]
			if coef == 1 {
				for i := range p {
					p[i] ^= src[i]
				}
			} else {
				for i := range p {
					p[i] ^= gfMul(coef, src[i])
				}
			}
		}
		payloads[k+j] = p
	}
	shards := make([][]byte, k+m)
	for id := 0; id < k+m; id++ {
		shards[id] = marshalShard(k, m, id, shardSize, int64(len(data)), payloads[id])
	}
	return shards, nil
}

// Decode recovers the original data from any sufficient set of shards
// produced by Encode. Shards failing their checksum are rejected and never
// participate in solving; duplicate shard IDs are rejected with an error.
// Decoding fails with ErrInsufficientShards when fewer than k valid
// shards remain, and with ErrOutputLimit when the declared original
// length exceeds maxOut (maxOut < 0 means no limit). The returned slice
// has exactly the declared original length.
func Decode(blobs [][]byte, maxOut int64) ([]byte, error) {
	valid := make(map[int]decodedShard)
	var base *shardHeader
	for _, blob := range blobs {
		h, payload, err := parseShard(blob)
		if err != nil {
			if err == ErrChecksumMismatch {
				continue // corrupt shard: rejected, never used in equations
			}
			return nil, err
		}
		if base == nil {
			cp := *h
			base = &cp
		} else if h.k != base.k || h.m != base.m ||
			h.shardSize != base.shardSize || h.origLen != base.origLen {
			return nil, ErrInconsistentHeader
		}
		if _, dup := valid[h.id]; dup {
			return nil, ErrDuplicateShard
		}
		valid[h.id] = decodedShard{payload: payload}
	}
	if base == nil {
		return nil, ErrInsufficientShards
	}
	if base.origLen < 0 || base.origLen > int64(base.k)*int64(base.shardSize) {
		return nil, ErrMalformedShard
	}
	if maxOut >= 0 && base.origLen > maxOut {
		return nil, ErrOutputLimit
	}
	if len(valid) < base.k {
		return nil, ErrInsufficientShards
	}

	// Select exactly k valid shards (lowest IDs, deterministically) and
	// assemble their generator-matrix rows.
	ids := make([]int, 0, len(valid))
	for id := range valid {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	ids = ids[:base.k]
	mat := make([][]byte, base.k)
	for r, id := range ids {
		mat[r] = generatorRow(id, base.k)
	}
	inv, err := invertMatrix(mat)
	if err != nil {
		return nil, err
	}

	// Rebuild data shards. Available data shards are used directly; each
	// missing data shard d is a row-d combination of available payloads.
	out := make([]byte, int(base.origLen))
	for d := 0; d < base.k; d++ {
		var data []byte
		if s, ok := valid[d]; ok {
			data = s.payload
		} else {
			data = make([]byte, base.shardSize)
			row := inv[d]
			for c, id := range ids {
				coef := row[c]
				if coef == 0 {
					continue
				}
				src := valid[id].payload
				if coef == 1 {
					for i := range data {
						data[i] ^= src[i]
					}
				} else {
					for i := range data {
						data[i] ^= gfMul(coef, src[i])
					}
				}
			}
		}
		dst := d * base.shardSize
		n := int64(base.origLen) - int64(dst)
		if n <= 0 {
			break
		}
		if int(n) > base.shardSize {
			n = int64(base.shardSize)
		}
		copy(out[dst:dst+int(n)], data[:n])
	}
	return out, nil
}

func marshalShard(k, m, id, shardSize int, origLen int64, payload []byte) []byte {
	blob := make([]byte, HeaderSize+len(payload))
	blob[0] = Magic0
	blob[1] = Magic1
	blob[2] = FormatVersion
	blob[3] = byte(k)
	blob[4] = byte(m)
	blob[5] = byte(id)
	binary.LittleEndian.PutUint32(blob[6:10], uint32(shardSize))
	binary.LittleEndian.PutUint64(blob[10:18], uint64(origLen))
	copy(blob[HeaderSize:], payload)
	sum := crc32.ChecksumIEEE(append(blob[0:18:18], payload...))
	binary.LittleEndian.PutUint32(blob[18:22], sum)
	return blob
}

func parseShard(blob []byte) (*shardHeader, []byte, error) {
	if len(blob) < HeaderSize {
		return nil, nil, ErrMalformedShard
	}
	if blob[0] != Magic0 || blob[1] != Magic1 {
		return nil, nil, ErrMalformedShard
	}
	if blob[2] != FormatVersion {
		return nil, nil, ErrUnsupportedVersion
	}
	h := &shardHeader{
		k:         int(blob[3]),
		m:         int(blob[4]),
		id:        int(blob[5]),
		shardSize: int(binary.LittleEndian.Uint32(blob[6:10])),
		origLen:   int64(binary.LittleEndian.Uint64(blob[10:18])),
	}
	if h.k < 1 || h.m < 1 || h.k+h.m > 256 || h.id >= h.k+h.m {
		return nil, nil, ErrMalformedShard
	}
	if int64(h.k)*int64(h.shardSize) < h.origLen || h.origLen < 0 {
		return nil, nil, ErrMalformedShard
	}
	if len(blob) != HeaderSize+h.shardSize {
		return nil, nil, ErrMalformedShard
	}
	payload := blob[HeaderSize:]
	stored := binary.LittleEndian.Uint32(blob[18:22])
	if crc32.ChecksumIEEE(append(blob[0:18:18], payload...)) != stored {
		return nil, nil, ErrChecksumMismatch
	}
	return h, payload, nil
}

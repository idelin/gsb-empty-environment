// Package erasure implements a systematic block erasure code over GF(256)
// using only the Go standard library. Data is split into K data blocks and
// M parity blocks; any K of the K+M blocks suffice to recover the original.
package erasure

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// FormatVersion is the on-the-wire block format version.
const FormatVersion = 1

// headerSize is the fixed block header length in bytes:
// version(1) k(1) m(1) id(1) origLen(4) shardLen(4) crc32(4).
const headerSize = 16

// HeaderSize is the fixed serialized block header length in bytes.
const HeaderSize = headerSize

// MaxShards bounds K+M: GF(256) provides at most 255 distinct
// evaluation points.
const MaxShards = 255

var (
	ErrVersion      = errors.New("erasure: unsupported format version")
	ErrTruncated    = errors.New("erasure: truncated block")
	ErrChecksum     = errors.New("erasure: block checksum mismatch")
	ErrDuplicateID  = errors.New("erasure: duplicate block id")
	ErrInvalidID    = errors.New("erasure: block id out of range")
	ErrInconsistent = errors.New("erasure: inconsistent block headers")
	ErrInsufficient = errors.New("erasure: insufficient valid blocks")
	ErrOutputLimit  = errors.New("erasure: decoded length exceeds output limit")
	ErrBadParams    = errors.New("erasure: invalid k/m parameters")
)

// Block is one encoded shard in decoded (structured) form.
type Block struct {
	ID      int    // shard index in [0, K+M)
	K       int    // number of data shards
	M       int    // number of parity shards
	OrigLen int    // length of the original payload in bytes
	Data    []byte // shard payload, ShardLen bytes
}

// Marshal serializes a block with version, lengths and CRC-32 checksum.
func (b *Block) Marshal() []byte {
	out := make([]byte, headerSize+len(b.Data))
	out[0] = FormatVersion
	out[1] = byte(b.K)
	out[2] = byte(b.M)
	out[3] = byte(b.ID)
	binary.BigEndian.PutUint32(out[4:8], uint32(b.OrigLen))
	binary.BigEndian.PutUint32(out[8:12], uint32(len(b.Data)))
	binary.BigEndian.PutUint32(out[12:16], crc32.ChecksumIEEE(b.Data))
	copy(out[headerSize:], b.Data)
	return out
}

// UnmarshalBlock parses and verifies one serialized block. Blocks with a
// bad version, bad length, out-of-range id or checksum mismatch are
// rejected with a descriptive error.
func UnmarshalBlock(raw []byte) (*Block, error) {
	if len(raw) < headerSize {
		return nil, ErrTruncated
	}
	if raw[0] != FormatVersion {
		return nil, ErrVersion
	}
	k, m, id := int(raw[1]), int(raw[2]), int(raw[3])
	origLen := int(binary.BigEndian.Uint32(raw[4:8]))
	shardLen := int(binary.BigEndian.Uint32(raw[8:12]))
	crc := binary.BigEndian.Uint32(raw[12:16])
	if len(raw) != headerSize+shardLen {
		return nil, ErrTruncated
	}
	if k < 1 || m < 1 || k+m > MaxShards {
		return nil, ErrBadParams
	}
	if id >= k+m {
		return nil, ErrInvalidID
	}
	payload := raw[headerSize:]
	if crc32.ChecksumIEEE(payload) != crc {
		return nil, ErrChecksum
	}
	data := make([]byte, shardLen)
	copy(data, payload)
	return &Block{ID: id, K: k, M: m, OrigLen: origLen, Data: data}, nil
}

// buildGenerator returns the systematic (K+M)xK coding matrix: a
// Vandermonde matrix over evaluation points 0..K+M-1, right-multiplied by
// the inverse of its top KxK submatrix. The top K rows become the identity
// (systematic form) and any K rows remain linearly independent (MDS).
func buildGenerator(k, m int) ([][]byte, error) {
	rows := k + m
	v := make([][]byte, rows)
	for r := 0; r < rows; r++ {
		v[r] = make([]byte, k)
		for c := 0; c < k; c++ {
			v[r][c] = gfPow(byte(r), c)
		}
	}
	top := make([][]byte, k)
	for i := 0; i < k; i++ {
		top[i] = make([]byte, k)
		copy(top[i], v[i])
	}
	topInv, err := gfMatrixInvert(top)
	if err != nil {
		return nil, err
	}
	gen := make([][]byte, rows)
	for r := 0; r < rows; r++ {
		gen[r] = make([]byte, k)
		for c := 0; c < k; c++ {
			var acc byte
			for t := 0; t < k; t++ {
				acc ^= gfMul(v[r][t], topInv[t][c])
			}
			gen[r][c] = acc
		}
	}
	return gen, nil
}

// Encode splits data into k data shards plus m parity shards (systematic
// Reed-Solomon style: data shards are the padded input itself).
func Encode(data []byte, k, m int) ([]*Block, error) {
	if k < 1 || m < 1 || k+m > MaxShards {
		return nil, ErrBadParams
	}
	if len(data) == 0 {
		return nil, errors.New("erasure: empty input")
	}
	gen, err := buildGenerator(k, m)
	if err != nil {
		return nil, err
	}
	shardLen := (len(data) + k - 1) / k
	padded := make([]byte, k*shardLen)
	copy(padded, data)

	shards := make([][]byte, k+m)
	for i := 0; i < k; i++ {
		shards[i] = padded[i*shardLen : (i+1)*shardLen]
	}
	for j := 0; j < m; j++ {
		row := gen[k+j]
		parity := make([]byte, shardLen)
		for c := 0; c < k; c++ {
			if row[c] == 0 {
				continue
			}
			for i := 0; i < shardLen; i++ {
				parity[i] ^= gfMul(row[c], shards[c][i])
			}
		}
		shards[k+j] = parity
	}

	blocks := make([]*Block, k+m)
	for i := range shards {
		blocks[i] = &Block{ID: i, K: k, M: m, OrigLen: len(data), Data: shards[i]}
	}
	return blocks, nil
}

// Decode recovers the original payload from serialized blocks. Any subset
// of at least K valid blocks suffices. Blocks with checksum errors are
// rejected and never used to solve the linear system; duplicate ids are
// rejected outright. If fewer than K valid blocks remain, ErrInsufficient
// is returned. The decoded output is bounded: if the original length
// exceeds outputLimit, ErrOutputLimit is returned before decoding.
func Decode(rawBlocks [][]byte, outputLimit int) ([]byte, error) {
	blocks := make([]*Block, 0, len(rawBlocks))
	for _, raw := range rawBlocks {
		b, err := UnmarshalBlock(raw)
		if err == ErrChecksum {
			continue // corrupted block: rejected, excluded from solving
		}
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, b)
	}
	if len(blocks) == 0 {
		return nil, ErrInsufficient
	}

	k, m, origLen := blocks[0].K, blocks[0].M, blocks[0].OrigLen
	shardLen := len(blocks[0].Data)
	seen := make([]bool, k+m)
	for _, b := range blocks {
		if b.K != k || b.M != m || b.OrigLen != origLen || len(b.Data) != shardLen {
			return nil, ErrInconsistent
		}
		if seen[b.ID] {
			return nil, ErrDuplicateID
		}
		seen[b.ID] = true
	}
	if origLen > outputLimit {
		return nil, ErrOutputLimit
	}
	if len(blocks) < k {
		return nil, ErrInsufficient
	}

	gen, err := buildGenerator(k, m)
	if err != nil {
		return nil, err
	}

	// Use the first K valid blocks to solve for the data shards.
	sel := blocks[:k]
	mat := make([][]byte, k)
	for r := 0; r < k; r++ {
		mat[r] = make([]byte, k)
		copy(mat[r], gen[sel[r].ID])
	}
	inv, err := gfMatrixInvert(mat)
	if err != nil {
		return nil, err
	}

	out := make([]byte, 0, k*shardLen)
	for d := 0; d < k; d++ {
		dataShard := make([]byte, shardLen)
		for r := 0; r < k; r++ {
			coef := inv[d][r]
			if coef == 0 {
				continue
			}
			for i := 0; i < shardLen; i++ {
				dataShard[i] ^= gfMul(coef, sel[r].Data[i])
			}
		}
		out = append(out, dataShard...)
	}
	return out[:origLen], nil
}

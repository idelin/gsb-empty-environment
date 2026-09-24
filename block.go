package dod

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// FormatVersion is the current on-disk format version.
const FormatVersion = 1

// Magic identifies a dod archive, followed by one format version byte.
var Magic = []byte{'D', 'O', 'D', '1'}

var (
	ErrFormat     = errors.New("dod: malformed data")
	ErrChecksum   = errors.New("dod: checksum mismatch")
	ErrTruncated  = errors.New("dod: truncated data")
	ErrTooLarge   = errors.New("dod: decode output exceeds bound")
	ErrOverflow   = errors.New("dod: integer overflow")
	ErrRegression = errors.New("dod: time regression rejected by policy")
	ErrVersion    = errors.New("dod: unsupported format version")
)

// Block is one independently decodable unit with an explicit epoch anchor.
type Block struct {
	Epoch  uint64
	Times  []int64
	Values []int64
}

// Block layout on disk:
//   uvarint totalLen (payload + 4 crc bytes)
//   payload:
//     byte  format version
//     byte  flags (must be 0)
//     uvarint epoch          -- explicit reset epoch
//     uvarint count
//     svarint t0, v0         -- anchor
//     [count>=2] svarint dt0, dv0        -- first deltas
//     [count>=3] uvarint len + time dod stream
//                uvarint len + value dod stream
//   uint32 crc32-IEEE(payload)

func encodeBlock(epoch uint64, times, values []int64) ([]byte, error) {
	n := len(times)
	if n == 0 || n != len(values) {
		return nil, ErrFormat
	}
	p := []byte{FormatVersion, 0}
	p = binary.AppendUvarint(p, epoch)
	p = binary.AppendUvarint(p, uint64(n))
	p = binary.AppendVarint(p, times[0])
	p = binary.AppendVarint(p, values[0])
	if n >= 2 {
		dt, ok := checkedSub(times[1], times[0])
		if !ok {
			return nil, ErrOverflow
		}
		dv, ok := checkedSub(values[1], values[0])
		if !ok {
			return nil, ErrOverflow
		}
		p = binary.AppendVarint(p, dt)
		p = binary.AppendVarint(p, dv)
		if n >= 3 {
			td := make([]int64, n-2)
			vd := make([]int64, n-2)
			dT, dV := dt, dv
			for i := 2; i < n; i++ {
				nt, ok := checkedSub(times[i], times[i-1])
				if !ok {
					return nil, ErrOverflow
				}
				dd, ok := checkedSub(nt, dT)
				if !ok {
					return nil, ErrOverflow
				}
				td[i-2], dT = dd, nt
				nv, ok := checkedSub(values[i], values[i-1])
				if !ok {
					return nil, ErrOverflow
				}
				dd, ok = checkedSub(nv, dV)
				if !ok {
					return nil, ErrOverflow
				}
				vd[i-2], dV = dd, nv
			}
			ts := encodeStream(td)
			vs := encodeStream(vd)
			p = binary.AppendUvarint(p, uint64(len(ts)))
			p = append(p, ts...)
			p = binary.AppendUvarint(p, uint64(len(vs)))
			p = append(p, vs...)
		}
	}
	total := len(p) + 4
	out := binary.AppendUvarint(nil, uint64(total))
	out = append(out, p...)
	var c [4]byte
	binary.LittleEndian.PutUint32(c[:], crc32.ChecksumIEEE(p))
	return append(out, c[:]...), nil
}

// decodeBlock decodes one block from buf, returning the block and the number
// of bytes consumed. maxCount bounds the decoded output length.
func decodeBlock(buf []byte, maxCount int) (*Block, int, error) {
	total64, k := binary.Uvarint(buf)
	if k <= 0 {
		return nil, 0, ErrFormat
	}
	if total64 < 5 || total64 > uint64(len(buf)-k) {
		return nil, 0, ErrTruncated
	}
	consumed := k + int(total64)
	payload := buf[k:consumed]
	body := payload[:len(payload)-4]
	if crc32.ChecksumIEEE(body) != binary.LittleEndian.Uint32(payload[len(payload)-4:]) {
		return nil, 0, ErrChecksum
	}
	if body[0] != FormatVersion {
		return nil, 0, ErrVersion
	}
	if body[1] != 0 {
		return nil, 0, ErrFormat
	}
	rest := body[2:]
	epoch, n1 := binary.Uvarint(rest)
	if n1 <= 0 {
		return nil, 0, ErrFormat
	}
	rest = rest[n1:]
	cnt, n2 := binary.Uvarint(rest)
	if n2 <= 0 {
		return nil, 0, ErrFormat
	}
	rest = rest[n2:]
	if cnt == 0 || cnt > uint64(maxCount) {
		return nil, 0, ErrTooLarge
	}
	t0, k3 := binary.Varint(rest)
	if k3 <= 0 {
		return nil, 0, ErrFormat
	}
	rest = rest[k3:]
	v0, k4 := binary.Varint(rest)
	if k4 <= 0 {
		return nil, 0, ErrFormat
	}
	rest = rest[k4:]
	n := int(cnt)
	times := make([]int64, n)
	values := make([]int64, n)
	times[0], values[0] = t0, v0
	var dt0, dv0 int64
	if n >= 2 {
		var k5, k6 int
		dt0, k5 = binary.Varint(rest)
		if k5 <= 0 {
			return nil, 0, ErrFormat
		}
		rest = rest[k5:]
		dv0, k6 = binary.Varint(rest)
		if k6 <= 0 {
			return nil, 0, ErrFormat
		}
		rest = rest[k6:]
		var ok bool
		if times[1], ok = checkedAdd(t0, dt0); !ok {
			return nil, 0, ErrOverflow
		}
		if values[1], ok = checkedAdd(v0, dv0); !ok {
			return nil, 0, ErrOverflow
		}
	}
	if n >= 3 {
		tsLen, k7 := binary.Uvarint(rest)
		if k7 <= 0 || tsLen > uint64(len(rest)-k7) {
			return nil, 0, ErrTruncated
		}
		td, err := decodeStream(rest[k7:k7+int(tsLen)], maxCount)
		if err != nil {
			return nil, 0, err
		}
		rest = rest[k7+int(tsLen):]
		vsLen, k8 := binary.Uvarint(rest)
		if k8 <= 0 || vsLen > uint64(len(rest)-k8) {
			return nil, 0, ErrTruncated
		}
		vd, err := decodeStream(rest[k8:k8+int(vsLen)], maxCount)
		if err != nil {
			return nil, 0, err
		}
		if len(td) != n-2 || len(vd) != n-2 {
			return nil, 0, ErrFormat
		}
		dT, dV := dt0, dv0
		for i := 2; i < n; i++ {
			var ok bool
			if dT, ok = checkedAdd(dT, td[i-2]); !ok {
				return nil, 0, ErrOverflow
			}
			if times[i], ok = checkedAdd(times[i-1], dT); !ok {
				return nil, 0, ErrOverflow
			}
			if dV, ok = checkedAdd(dV, vd[i-2]); !ok {
				return nil, 0, ErrOverflow
			}
			if values[i], ok = checkedAdd(values[i-1], dV); !ok {
				return nil, 0, ErrOverflow
			}
		}
	}
	return &Block{Epoch: epoch, Times: times, Values: values}, consumed, nil
}

// blockSize computes the exact encoded byte length of a block, matching
// encodeBlock byte-for-byte. It is the cost function of the boundary DP.
func blockSize(epoch uint64, n int, t0, v0, dt0, dv0 int64, st, sv *streamDP) int {
	p := 2 + uvlen(epoch) + uvlen(uint64(n)) + svlen(t0) + svlen(v0)
	if n >= 2 {
		p += svlen(dt0) + svlen(dv0)
	}
	if n >= 3 {
		m := n - 2
		ts := uvlen(uint64(st.nseg[m])) + int((st.bits[m]+7)/8)
		vs := uvlen(uint64(sv.nseg[m])) + int((sv.bits[m]+7)/8)
		p += uvlen(uint64(ts)) + ts + uvlen(uint64(vs)) + vs
	}
	total := p + 4
	return uvlen(uint64(total)) + total
}

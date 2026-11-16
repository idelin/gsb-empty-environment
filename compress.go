package netsec

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// WindowSize is the maximum backward distance, in bytes, addressable by a
	// match.
	WindowSize = 4096
	// MinMatch is the shortest sequence encoded as a match.
	MinMatch = 3
	// MaxMatch is the longest sequence representable by one match token.
	MaxMatch = 255
)

var (
	// ErrInvalidEncoding reports a malformed or truncated LZ token stream.
	ErrInvalidEncoding = errors.New("netsec: invalid LZ encoding")
)

// Compress encodes data using dictionary as the initial LZ history. The input
// data itself is also added to the history while it is encoded.
//
// The output is a sequence of:
//   - literal: tag 0 followed by the original byte;
//   - match: tag 1, a two-byte big-endian distance, and a one-byte length.
//
// Matches use the preceding 4096 bytes, may overlap the bytes they produce,
// are at least 3 and at most 255 bytes long, and prefer the longest match with
// the smallest distance on ties.
func Compress(dictionary, data []byte) []byte {
	out, err := compressContext(context.Background(), dictionary, data)
	if err != nil {
		// A background context cannot be cancelled.
		panic(err)
	}
	return out
}

// Decompress decodes an LZ token stream using dictionary as the initial
// history. dictionary must be the same snapshot used by Compress.
func Decompress(dictionary, encoded []byte) ([]byte, error) {
	return decompressContext(context.Background(), dictionary, encoded)
}

// CompressContext is Compress with cancellation support.
func CompressContext(ctx context.Context, dictionary, data []byte) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return compressContext(ctx, dictionary, data)
}

// DecompressContext is Decompress with cancellation support.
func DecompressContext(ctx context.Context, dictionary, encoded []byte) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return decompressContext(ctx, dictionary, encoded)
}

// Encode is an alias for Compress.
func Encode(dictionary, data []byte) []byte {
	return Compress(dictionary, data)
}

// Decode is an alias for Decompress.
func Decode(dictionary, encoded []byte) ([]byte, error) {
	return Decompress(dictionary, encoded)
}

func compressContext(ctx context.Context, dictionary, data []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	dict := tailWindow(dictionary, WindowSize)
	if len(data) == 0 {
		return nil, nil
	}

	// Work in one contiguous buffer so dictionary and newly encoded bytes can
	// be used uniformly as match history, including overlapping matches.
	combined := make([]byte, 0, len(dict)+len(data))
	combined = append(combined, dict...)
	dictLen := len(combined)
	combined = append(combined, data...)

	outCap := len(data)
	if outCap <= int(^uint(0)>>1)/2 {
		outCap *= 2
	}
	out := make([]byte, 0, outCap)

	const (
		hashBuckets = 1 << 16
		none        = -1
	)
	head := make([]int32, hashBuckets)
	for i := range head {
		head[i] = none
	}
	prev := make([]int32, len(combined))
	for i := range prev {
		prev[i] = none
	}

	insert := func(pos int) {
		if pos < 0 || pos+MinMatch > len(combined) {
			return
		}
		h := hash3(combined[pos], combined[pos+1], combined[pos+2])
		prev[pos] = head[h]
		head[h] = int32(pos)
	}

	// Positions in the initial dictionary can start overlapping matches whose
	// first three bytes extend into the new input.
	for pos := 0; pos < dictLen; pos++ {
		insert(pos)
	}

	for pos := 0; pos < len(data); {
		if pos&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}

		abs := dictLen + pos
		bestLen, bestDist := 0, 0
		if abs+MinMatch <= len(combined) {
			h := hash3(combined[abs], combined[abs+1], combined[abs+2])
			lower := abs - WindowSize
			if lower < 0 {
				lower = 0
			}
			limit := len(combined) - abs
			if limit > MaxMatch {
				limit = MaxMatch
			}

			for candidate := int(head[h]); candidate >= lower; candidate = int(prev[candidate]) {
				if candidate < lower {
					break
				}
				// Hash collisions and a candidate that only matches one or two
				// bytes are not matches.
				if combined[candidate] != combined[abs] ||
					combined[candidate+1] != combined[abs+1] ||
					combined[candidate+2] != combined[abs+2] {
					continue
				}

				n := MinMatch
				for n < limit && combined[candidate+n] == combined[abs+n] {
					n++
				}
				dist := abs - candidate
				if n > bestLen || (n == bestLen && dist < bestDist) {
					bestLen, bestDist = n, dist
					if n == MaxMatch {
						// Newer candidates are visited first, so this is also
						// the smallest distance for the maximum length.
						break
					}
				}
			}
		}

		if bestLen >= MinMatch {
			if bestDist <= 0 || bestDist > WindowSize || bestLen > MaxMatch {
				return nil, fmt.Errorf("%w: invalid internal match", ErrInvalidEncoding)
			}
			out = append(out, 1)
			out = binary.BigEndian.AppendUint16(out, uint16(bestDist))
			out = append(out, byte(bestLen))
			for i := 0; i < bestLen; i++ {
				insert(abs + i)
			}
			pos += bestLen
		} else {
			out = append(out, 0, data[pos])
			insert(abs)
			pos++
		}
	}

	return out, nil
}

func decompressContext(ctx context.Context, dictionary, encoded []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	window := append([]byte(nil), tailWindow(dictionary, WindowSize)...)
	out := make([]byte, 0, len(encoded))
	appendByte := func(b byte) {
		out = append(out, b)
		window = append(window, b)
		if len(window) > WindowSize {
			copy(window, window[len(window)-WindowSize:])
			window = window[:WindowSize]
		}
	}

	for pos := 0; pos < len(encoded); {
		if pos&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}

		switch encoded[pos] {
		case 0:
			if len(encoded)-pos < 2 {
				return nil, fmt.Errorf("%w at byte %d: truncated literal", ErrInvalidEncoding, pos)
			}
			appendByte(encoded[pos+1])
			pos += 2
		case 1:
			if len(encoded)-pos < 4 {
				return nil, fmt.Errorf("%w at byte %d: truncated match", ErrInvalidEncoding, pos)
			}
			dist := int(binary.BigEndian.Uint16(encoded[pos+1 : pos+3]))
			length := int(encoded[pos+3])
			if dist < 1 || dist > WindowSize {
				return nil, fmt.Errorf("%w at byte %d: distance %d", ErrInvalidEncoding, pos, dist)
			}
			if length < MinMatch {
				return nil, fmt.Errorf("%w at byte %d: length %d", ErrInvalidEncoding, pos, length)
			}
			if dist > len(window) {
				return nil, fmt.Errorf("%w at byte %d: distance %d exceeds history %d", ErrInvalidEncoding, pos, dist, len(window))
			}
			for i := 0; i < length; i++ {
				src := len(window) - dist
				appendByte(window[src])
			}
			pos += 4
		default:
			return nil, fmt.Errorf("%w at byte %d: tag %d", ErrInvalidEncoding, pos, encoded[pos])
		}
	}

	return out, nil
}

func hash3(a, b, c byte) uint32 {
	v := uint32(a)<<16 | uint32(b)<<8 | uint32(c)
	return (v * 2654435761) >> 16
}

func tailWindow(data []byte, limit int) []byte {
	if len(data) <= limit {
		return data
	}
	return data[len(data)-limit:]
}

package netsec

import (
	"context"
	"fmt"
)

const (
	matchHashBits = 14
	matchHashSize = 1 << matchHashBits
	checkEvery    = 1024
)

func hash3(a, b, c byte) int {
	v := uint32(a)<<16 | uint32(b)<<8 | uint32(c)
	return int((v * 2654435761) >> (32 - matchHashBits))
}

// Compress encodes src using dictionary as history. It is the stateless
// codec primitive; service code should normally use Store.Compress so that
// tenant, label, cancellation, and dictionary lifecycle rules are enforced.
func Compress(dictionary, src []byte) []byte {
	out, _ := compress(context.Background(), dictionary, src, nil)
	return out
}

// Decompress decodes a token stream using dictionary as the initial history.
// It enforces the wire-format, window, and match-length limits.
func Decompress(dictionary, encoded []byte) ([]byte, error) {
	return decompress(context.Background(), dictionary, encoded, nil)
}

func compress(ctx context.Context, dictionary, src []byte, abort func() error) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(src) == 0 {
		return []byte{}, nil
	}

	base := tailWindow(dictionary)
	total := len(base) + len(src)
	all := make([]byte, 0, total)
	all = append(all, base...)
	all = append(all, src...)

	head := make([]int, matchHashSize)
	for i := range head {
		head[i] = -1
	}
	prev := make([]int, total)
	for i := range prev {
		prev[i] = -1
	}

	insert := func(p int) {
		if p+MinMatch > total {
			return
		}
		h := hash3(all[p], all[p+1], all[p+2])
		prev[p] = head[h]
		head[h] = p
	}

	// Prime the index with the dictionary window. A candidate may begin in
	// the dictionary and run into input bytes; that is a valid overlapping
	// match and the byte comparison below handles it.
	for p := 0; p < len(base); p++ {
		insert(p)
	}

	out := make([]byte, 0, len(src)+len(src)/8)
	nextCheck := 0
	pos := len(base)

	for pos < total {
		if pos >= nextCheck {
			if err := checkAbort(ctx, abort); err != nil {
				return nil, err
			}
			nextCheck = pos + checkEvery
		}

		bestLen := 0
		bestDist := 0
		maxLen := total - pos
		if maxLen > MaxMatch {
			maxLen = MaxMatch
		}

		if maxLen >= MinMatch {
			h := hash3(all[pos], all[pos+1], all[pos+2])
			limit := pos - WindowSize
			if limit < 0 {
				limit = 0
			}

			// Positions are inserted in increasing order and prepended to
			// the chain, so distances are nondecreasing along the chain.
			for cand := head[h]; cand >= 0; cand = prev[cand] {
				if cand < limit {
					break
				}
				dist := pos - cand
				if dist <= 0 || dist > WindowSize {
					break
				}
				n := matchLen(all, cand, pos, maxLen)
				if n > bestLen || (n == bestLen && n >= MinMatch && dist < bestDist) {
					bestLen = n
					bestDist = dist
					if n == maxLen {
						break
					}
				}
			}
		}

		if bestLen >= MinMatch {
			out = append(out, 1, byte(bestDist>>8), byte(bestDist), byte(bestLen))
			for k := 0; k < bestLen; k++ {
				insert(pos + k)
			}
			pos += bestLen
		} else {
			out = append(out, 0, all[pos])
			insert(pos)
			pos++
		}
	}

	if err := checkAbort(ctx, abort); err != nil {
		return nil, err
	}
	return out, nil
}

func matchLen(all []byte, cand, pos, maxLen int) int {
	n := 0
	for n < maxLen && all[cand+n] == all[pos+n] {
		n++
	}
	return n
}

func checkAbort(ctx context.Context, abort func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if abort != nil {
		return abort()
	}
	return nil
}

func decompress(ctx context.Context, dictionary, encoded []byte, abort func() error) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	base := tailWindow(dictionary)
	out := make([]byte, 0, len(encoded))
	nextCheck := 0

	for pos := 0; pos < len(encoded); {
		if pos >= nextCheck || len(out) >= nextCheck {
			if err := checkAbort(ctx, abort); err != nil {
				return nil, err
			}
			nextCheck = pos + checkEvery
			if len(out) >= nextCheck {
				nextCheck = len(out) + checkEvery
			}
		}

		switch encoded[pos] {
		case 0:
			if pos+2 > len(encoded) {
				return nil, fmt.Errorf("%w at byte %d: truncated literal", ErrCorrupt, pos)
			}
			out = append(out, encoded[pos+1])
			pos += 2

		case 1:
			if pos+4 > len(encoded) {
				return nil, fmt.Errorf("%w at byte %d: truncated match", ErrCorrupt, pos)
			}
			dist := int(encoded[pos+1])<<8 | int(encoded[pos+2])
			length := int(encoded[pos+3])
			if dist < 1 || dist > WindowSize {
				return nil, fmt.Errorf("%w at byte %d: distance %d outside window", ErrCorrupt, pos, dist)
			}
			if length < MinMatch {
				return nil, fmt.Errorf("%w at byte %d: match length %d below minimum", ErrCorrupt, pos, length)
			}
			if dist > len(base)+len(out) {
				return nil, fmt.Errorf("%w at byte %d: distance %d exceeds history", ErrCorrupt, pos, dist)
			}
			for k := 0; k < length; k++ {
				idx := len(base) + len(out) - dist
				if idx < len(base) {
					out = append(out, base[idx])
				} else {
					out = append(out, out[idx-len(base)])
				}
			}
			pos += 4

		default:
			return nil, fmt.Errorf("%w at byte %d: token %d", ErrCorrupt, pos, encoded[pos])
		}
	}

	if err := checkAbort(ctx, abort); err != nil {
		return nil, err
	}
	return out, nil
}
